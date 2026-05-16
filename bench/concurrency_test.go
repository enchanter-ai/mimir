// concurrency_test.go fires 100 simultaneous requests against the issuer
// and asserts uniqueness + signature correctness under load.
//
// Run with:
//
//	go test -race -run TestConcurrent -v .
//
// The issuer must be running on :8080 (or ISSUER_ADDR env var).
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/enchanter-ai/mimir/issuer/canonicalize"
	"github.com/enchanter-ai/mimir/issuer/types"
	"github.com/oasisprotocol/curve25519-voi/primitives/ed25519"
)

const defaultAddr = "http://localhost:8080"

func issuerAddr() string {
	if a := os.Getenv("ISSUER_ADDR"); a != "" {
		return a
	}
	return defaultAddr
}

// waitHealthy polls /v1/healthz up to 20 s. Returns false if the issuer is not up.
func waitHealthy(t *testing.T) bool {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(issuerAddr() + "/v1/healthz")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return true
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// fetchPublicKey retrieves the issuer's JWK public key from /v1/key.
func fetchPublicKey(t *testing.T) (ed25519.PublicKey, string) {
	t.Helper()
	resp, err := http.Get(issuerAddr() + "/v1/key")
	if err != nil {
		t.Fatalf("fetchPublicKey: GET /v1/key: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var jwk types.JWKPublicKey
	if err := json.Unmarshal(body, &jwk); err != nil {
		t.Fatalf("fetchPublicKey: unmarshal JWK: %v", err)
	}
	pubBytes, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		t.Fatalf("fetchPublicKey: decode X: %v", err)
	}
	return ed25519.PublicKey(pubBytes), jwk.Kid
}

// attestResult holds one concurrent request outcome.
type attestResult struct {
	StatusCode int
	Envelope   *types.Envelope
	Err        error
}

// sendAttest sends a single POST /v1/attest and returns the parsed response envelope.
func sendAttest(client *http.Client, payload []byte) attestResult {
	resp, err := client.Post(issuerAddr()+"/v1/attest", "application/json", bytes.NewReader(payload))
	if err != nil {
		return attestResult{Err: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return attestResult{StatusCode: resp.StatusCode, Err: fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)}
	}

	var ar types.AttestResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return attestResult{StatusCode: resp.StatusCode, Err: fmt.Errorf("unmarshal: %w", err)}
	}
	return attestResult{StatusCode: resp.StatusCode, Envelope: ar.Envelope}
}

// TestConcurrentRequests fires 100 simultaneous requests and asserts:
//  1. All return HTTP 200.
//  2. All envelopes have a unique tool_call_id (UUID collision under concurrency).
//  3. All signatures verify against the same public key (no key-race corruption).
func TestConcurrentRequests(t *testing.T) {
	if !waitHealthy(t) {
		t.Skip("issuer not reachable at " + issuerAddr() + " — start with: cd ../issuer && go run .")
	}

	pubKey, keyID := fetchPublicKey(t)
	t.Logf("issuer public key fetched — kid=%s", keyID)

	payloads := GeneratePayloads(100)
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 200,
		},
	}

	const concurrency = 100
	results := make([]attestResult, concurrency)
	var wg sync.WaitGroup
	wg.Add(concurrency)

	start := time.Now()
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			p := payloads[idx%len(payloads)]
			results[idx] = sendAttest(client, p.Body)
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	t.Logf("all %d concurrent requests completed in %s", concurrency, elapsed)

	// --- Assertion 1: all returned HTTP 200 ---
	failCount := 0
	for i, r := range results {
		if r.Err != nil || r.StatusCode != http.StatusOK {
			t.Errorf("[%d] expected 200, got %d: %v", i, r.StatusCode, r.Err)
			failCount++
		}
	}
	if failCount > 0 {
		t.Fatalf("CONCURRENCY BUG: %d/%d requests failed — see errors above", failCount, concurrency)
	}
	t.Logf("PASS: all %d requests returned HTTP 200", concurrency)

	// --- Assertion 2: all tool_call_id values are unique ---
	seen := make(map[string]int, concurrency)
	for i, r := range results {
		if r.Envelope == nil {
			t.Errorf("[%d] nil envelope", i)
			continue
		}
		id := r.Envelope.ToolCallID
		if prev, dup := seen[id]; dup {
			t.Errorf("CONCURRENCY BUG: tool_call_id collision — request %d and %d share id %q", i, prev, id)
		}
		seen[id] = i
	}
	if !t.Failed() {
		t.Logf("PASS: all %d tool_call_id values are unique", concurrency)
	}

	// --- Assertion 3: all signatures verify against the same public key ---
	sigFailures := 0
	for i, r := range results {
		if r.Envelope == nil || r.Envelope.Signature == nil {
			continue
		}
		env := r.Envelope

		// Reconstruct the signed payload: clear signature.value (omitempty drops it from JSON)
		envCopy := *env
		sigCopy := *env.Signature
		sigCopy.Value = ""
		envCopy.Signature = &sigCopy

		canonical, err := canonicalize.Canonicalize(&envCopy)
		if err != nil {
			t.Errorf("[%d] canonicalize for verify: %v", i, err)
			sigFailures++
			continue
		}

		sigBytes, err := base64.RawURLEncoding.DecodeString(env.Signature.Value)
		if err != nil {
			t.Errorf("[%d] decode signature: %v", i, err)
			sigFailures++
			continue
		}

		if !ed25519.Verify(pubKey, canonical, sigBytes) {
			t.Errorf("CONCURRENCY BUG: [%d] signature verification FAILED — possible key race", i)
			sigFailures++
		}
	}
	if sigFailures > 0 {
		t.Fatalf("CONCURRENCY BUG: %d/%d signature verifications failed", sigFailures, concurrency)
	}
	t.Logf("PASS: all %d signatures verify against key kid=%s", concurrency, keyID)
}

// TestConcurrentStress is a heavier version: 500 goroutines, mixed payloads.
func TestConcurrentStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in -short mode")
	}
	if !waitHealthy(t) {
		t.Skip("issuer not reachable at " + issuerAddr())
	}

	pubKey, keyID := fetchPublicKey(t)

	payloads := GeneratePayloads(500)
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 600,
		},
	}

	const concurrency = 500
	results := make([]attestResult, concurrency)
	var wg sync.WaitGroup
	wg.Add(concurrency)

	start := time.Now()
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			p := payloads[idx%len(payloads)]
			results[idx] = sendAttest(client, p.Body)
		}(i)
	}
	wg.Wait()
	t.Logf("stress: %d goroutines finished in %s", concurrency, time.Since(start))

	fails := 0
	sigFails := 0
	seen := make(map[string]bool, concurrency)
	dupIDs := 0

	for i, r := range results {
		if r.Err != nil || r.StatusCode != 200 {
			fails++
			continue
		}
		if r.Envelope == nil {
			fails++
			continue
		}
		if seen[r.Envelope.ToolCallID] {
			dupIDs++
		}
		seen[r.Envelope.ToolCallID] = true

		env := r.Envelope
		envCopy := *env
		sigCopy := *env.Signature
		sigCopy.Value = ""
		envCopy.Signature = &sigCopy
		canonical, err := canonicalize.Canonicalize(&envCopy)
		if err != nil {
			sigFails++
			continue
		}
		sigBytes, _ := base64.RawURLEncoding.DecodeString(env.Signature.Value)
		if !ed25519.Verify(pubKey, canonical, sigBytes) {
			t.Errorf("[%d] signature FAILED — possible key race (kid=%s)", i, keyID)
			sigFails++
		}
	}

	t.Logf("stress results: fails=%d dupIDs=%d sigFails=%d", fails, dupIDs, sigFails)
	if fails > 0 {
		t.Errorf("CONCURRENCY BUG: %d/%d requests failed", fails, concurrency)
	}
	if dupIDs > 0 {
		t.Errorf("CONCURRENCY BUG: %d duplicate tool_call_id values detected", dupIDs)
	}
	if sigFails > 0 {
		t.Errorf("CONCURRENCY BUG: %d signature verifications failed", sigFails)
	}
}

// BenchmarkSignEnvelope is a zero-network micro-benchmark of the signing path (pure CPU).
// Run with: go test -bench=BenchmarkSignEnvelope -benchtime=5s -benchmem ./...
func BenchmarkSignEnvelope(b *testing.B) {
	if !waitHealthyB(b) {
		b.Skip("issuer not reachable — start with: cd ../issuer && go run .")
	}

	payloads := GeneratePayloads(10)
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{MaxIdleConnsPerHost: 20},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p := payloads[i%len(payloads)]
		r := sendAttest(client, p.Body)
		if r.Err != nil {
			b.Fatalf("request failed: %v", r.Err)
		}
	}
}

func waitHealthyB(b *testing.B) bool {
	b.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(issuerAddr() + "/v1/healthz")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return true
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
