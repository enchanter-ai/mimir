package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/enchanter-ai/mimir/issuer/keystore"
	"github.com/enchanter-ai/mimir/issuer/kms"
	"github.com/enchanter-ai/mimir/issuer/telemetry"
	"github.com/enchanter-ai/mimir/issuer/types"
	"github.com/gorilla/mux"
)

// buildDPoPProof constructs a valid DPoP JWT for the given HTTP method + URI.
// Returns the proof and the expected JWK thumbprint (RFC 7638 base64url-no-pad).
func buildDPoPProof(t *testing.T, method, uri string) (proof, thumbprint string) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}

	xB64 := base64.RawURLEncoding.EncodeToString(pub)
	hdr := map[string]any{
		"typ": "dpop+jwt",
		"alg": "EdDSA",
		"jwk": map[string]any{
			"kty": "OKP",
			"crv": "Ed25519",
			"x":   xB64,
		},
	}
	payload := map[string]any{
		"htm": method,
		"htu": uri,
		"iat": time.Now().Unix(),
		"jti": "test-jti-" + uri,
	}
	hb, _ := json.Marshal(hdr)
	pb, _ := json.Marshal(payload)
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	sig := ed25519.Sign(priv, []byte(signingInput))
	proof = signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	canonical := []byte(fmt.Sprintf(`{"crv":"Ed25519","kty":"OKP","x":%q}`, xB64))
	h := sha256.Sum256(canonical)
	thumbprint = base64.RawURLEncoding.EncodeToString(h[:])
	return proof, thumbprint
}

func newTestServer(t *testing.T) (*httptest.Server, kms.Signer) {
	t.Helper()
	t.Setenv("ISSUER_HISTORICAL_KEYS_FILE", t.TempDir()+"/none.json")

	signer := kms.NewMockKMS()
	ks, err := keystore.New(signer.KeyID(), signer.PublicKey())
	if err != nil {
		t.Fatalf("keystore.New: %v", err)
	}
	srv := &server{signer: signer, keystore: ks, tracer: telemetry.NoOpTracer{}}
	r := mux.NewRouter()
	r.HandleFunc("/v1/attest", srv.handleAttest).Methods(http.MethodPost)
	r.HandleFunc("/v1/attest-mcp", srv.handleAttestMCP).Methods(http.MethodPost)
	r.HandleFunc("/v1/key", srv.handleKey).Methods(http.MethodGet)
	r.HandleFunc("/v1/keys", srv.handleKeys).Methods(http.MethodGet)
	return httptest.NewServer(r), signer
}

// TestDPoPHeaderEndToEnd posts an attest request with a DPoP HTTP header.
// The envelope's invoked_by MUST be did:jwk:<thumbprint> of the proof key.
func TestDPoPHeaderEndToEnd(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	target := ts.URL + "/v1/attest"
	proof, thumb := buildDPoPProof(t, "POST", target)

	body := []byte(`{
        "request":  {"name":"fetch","arguments":{"url":"https://x"}},
        "result":   {"content":[{"type":"text","text":"hi"}]},
        "tool_id":  "did:web:example.com:tools:fetch",
        "tool_version": "1.0.0"
    }`)
	req, _ := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DPoP", proof)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var resp types.AttestResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ValidationLevel != "trust_anchored" {
		t.Errorf("validation_level: got %q, want trust_anchored", resp.ValidationLevel)
	}
	expectedDID := "did:jwk:" + thumb
	if resp.Envelope.InvokedBy != expectedDID {
		t.Errorf("invoked_by:\n  got  %q\n  want %q", resp.Envelope.InvokedBy, expectedDID)
	}
}

// TestDPoPInBodyAlsoWorks posts the proof in the body field instead of the
// header. Same result.
func TestDPoPInBodyEndToEnd(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	target := ts.URL + "/v1/attest"
	proof, thumb := buildDPoPProof(t, "POST", target)

	body := map[string]any{
		"request":               map[string]any{"name": "x", "arguments": map[string]any{"k": "v"}},
		"result":                map[string]any{"content": []map[string]any{{"type": "text", "text": "y"}}},
		"tool_id":               "did:web:example.com:tools:x",
		"tool_version":          "1.0.0",
		"client_identity_proof": proof,
	}
	bb, _ := json.Marshal(body)
	res, err := http.Post(target, "application/json", bytes.NewReader(bb))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var resp types.AttestResponse
	_ = json.NewDecoder(res.Body).Decode(&resp)
	if resp.Envelope.InvokedBy != "did:jwk:"+thumb {
		t.Errorf("invoked_by:\n  got  %q\n  want did:jwk:%s", resp.Envelope.InvokedBy, thumb)
	}
}

// TestDPoPAbsentLeavesUnverifiedIdentity confirms the path without a proof
// still works (validation level falls back to cryptographically_valid).
func TestDPoPAbsentLeavesUnverifiedIdentity(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	body := []byte(`{
        "request":  {"name":"x","arguments":{}},
        "result":   {"content":[{"type":"text","text":"y"}]},
        "tool_id":  "did:web:example.com:tools:x",
        "tool_version": "1.0.0"
    }`)
	res, err := http.Post(ts.URL+"/v1/attest", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var resp types.AttestResponse
	_ = json.NewDecoder(res.Body).Decode(&resp)
	if resp.ValidationLevel != "cryptographically_valid" {
		t.Errorf("validation_level without proof: got %q, want cryptographically_valid", resp.ValidationLevel)
	}
	// The fallback stub identity must NOT look like a DPoP-derived did:jwk —
	// confirms we didn't silently fabricate one.
	if strings.HasPrefix(resp.Envelope.InvokedBy, "did:jwk:") {
		t.Errorf("invoked_by leaked did:jwk without a proof: %q", resp.Envelope.InvokedBy)
	}
}

// TestDPoPRejectsTamperedProof confirms that a malformed proof (here:
// wrong method commit) causes a 400 with a clear error, NOT a silent
// fallback to unverified identity.
func TestDPoPRejectsTamperedProof(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	target := ts.URL + "/v1/attest"
	// proof commits to GET, request is POST → must reject.
	proof, _ := buildDPoPProof(t, "GET", target)

	body := []byte(`{
        "request":  {"name":"x","arguments":{}},
        "result":   {"content":[{"type":"text","text":"y"}]},
        "tool_id":  "did:web:example.com:tools:x",
        "tool_version": "1.0.0"
    }`)
	req, _ := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DPoP", proof)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("tampered proof: expected 400, got %d", res.StatusCode)
	}
	rb, _ := readAll(res.Body)
	if !strings.Contains(rb, "client_identity_proof rejected") {
		t.Errorf("error body should explain rejection; got %q", rb)
	}
}

func readAll(rc interface{ Read([]byte) (int, error) }) (string, error) {
	buf := make([]byte, 0, 2048)
	tmp := make([]byte, 1024)
	for {
		n, err := rc.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(buf), nil
}
