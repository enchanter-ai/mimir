package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enchanter-ai/mimir/issuer/canonicalize"
	"github.com/enchanter-ai/mimir/issuer/keystore"
	"github.com/enchanter-ai/mimir/issuer/kms"
	"github.com/enchanter-ai/mimir/issuer/types"
	"github.com/gorilla/mux"
)

// TestServerEndToEndWithAWSFake proves the issuer's full HTTP pipeline works
// when configured with the AWS KMS-backed Signer. The fake KMS API is
// wire-faithful (DER pub key, raw 64-byte sig, real validation rules), so
// success here means the production AWS code path is correct modulo cloud
// credentials.
//
// Steps:
//  1. Construct AWSKMSFake.
//  2. Wrap it in NewAWSSignerWithAPI → AWSSigner.
//  3. Wire AWSSigner into a server and mount real routes.
//  4. POST to /v1/attest with a sample tools/call payload.
//  5. Externally verify the returned envelope's signature against the fake's
//     public key (proves the signing happened through the AWS path).
//  6. GET /v1/key and confirm the JWK's `x` matches the fake's public key.
func TestServerEndToEndWithAWSFake(t *testing.T) {
	const arn = "arn:aws:kms:us-east-1:123456789012:key/integration-test"
	ctx := context.Background()

	fake, err := kms.NewAWSKMSFake(arn)
	if err != nil {
		t.Fatalf("NewAWSKMSFake: %v", err)
	}

	signer, err := kms.NewAWSSignerWithAPI(ctx, fake, arn)
	if err != nil {
		t.Fatalf("NewAWSSignerWithAPI: %v", err)
	}

	// Steer the keystore away from a stray historical-keys file in the test
	// working dir.
	t.Setenv("ISSUER_HISTORICAL_KEYS_FILE", t.TempDir()+"/none.json")
	ks, err := keystore.New(signer.KeyID(), signer.PublicKey())
	if err != nil {
		t.Fatalf("keystore.New: %v", err)
	}
	srv := &server{signer: signer, keystore: ks}
	r := mux.NewRouter()
	r.HandleFunc("/v1/attest", srv.handleAttest).Methods(http.MethodPost)
	r.HandleFunc("/v1/key", srv.handleKey).Methods(http.MethodGet)
	r.HandleFunc("/v1/keys", srv.handleKeys).Methods(http.MethodGet)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// --- 1. Post an attest request ---
	body := []byte(`{
        "request": {"name":"fetch_document","arguments":{"url":"https://example.com/doc"}},
        "result":  {"content":[{"type":"text","text":"Hello from the integration test"}]},
        "tool_id": "did:web:example.com:tools:fetch",
        "tool_version": "1.0.0"
    }`)
	resp, err := http.Post(ts.URL+"/v1/attest", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/attest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var attestResp types.AttestResponse
	if err := json.NewDecoder(resp.Body).Decode(&attestResp); err != nil {
		t.Fatalf("decode attest response: %v", err)
	}
	env := attestResp.Envelope
	if env == nil || env.Signature == nil || env.Signature.Value == "" {
		t.Fatal("envelope missing signature")
	}

	// --- 2. External verification against the fake's public key ---
	// Strip signature.value, canonicalize, decode sig, verify with Ed25519.
	canonicalEnv := *env
	sigCopy := *env.Signature
	sigCopy.Value = ""
	canonicalEnv.Signature = &sigCopy

	canonical, err := canonicalize.Canonicalize(&canonicalEnv)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(env.Signature.Value)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}

	if !ed25519.Verify(fake.PublicKey(), canonical, sigBytes) {
		t.Error("signature does not verify against fake KMS public key")
	}

	// --- 3. Confirm /v1/key returns the same public key ---
	keyResp, err := http.Get(ts.URL + "/v1/key")
	if err != nil {
		t.Fatalf("GET /v1/key: %v", err)
	}
	defer keyResp.Body.Close()

	var jwk types.JWKPublicKey
	if err := json.NewDecoder(keyResp.Body).Decode(&jwk); err != nil {
		t.Fatalf("decode jwk: %v", err)
	}
	if jwk.Kty != "OKP" || jwk.Crv != "Ed25519" {
		t.Errorf("unexpected JWK shape: kty=%q crv=%q", jwk.Kty, jwk.Crv)
	}
	if !strings.HasPrefix(jwk.Kid, "arn:aws:kms:") {
		t.Errorf("Kid should be the KMS ARN, got %q", jwk.Kid)
	}
	rawPub, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		t.Fatalf("decode jwk.x: %v", err)
	}
	if !bytes.Equal(rawPub, fake.PublicKey()) {
		t.Error("JWK pub key does not match fake's underlying pub key")
	}

	// --- 4. Confirm the fake actually serviced the calls ---
	if fake.SignCalls < 1 {
		t.Error("fake.SignCalls == 0 — issuer didn't route through AWS path")
	}
	if fake.GetPublicKeyCalls != 1 {
		t.Errorf("fake.GetPublicKeyCalls = %d, want 1 (caching broken)", fake.GetPublicKeyCalls)
	}

	t.Logf("KMS path verified: %d Sign calls, %d GetPublicKey calls, %d-byte sig",
		fake.SignCalls, fake.GetPublicKeyCalls, len(sigBytes))
}
