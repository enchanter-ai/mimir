package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	"github.com/enchanter-ai/mimir/issuer/canonicalize"
	"github.com/enchanter-ai/mimir/issuer/envelope"
	"github.com/enchanter-ai/mimir/issuer/kms"
)

// newTestSigner returns a deterministic mock Signer for tests.
func newTestSigner(t *testing.T) kms.Signer {
	t.Helper()
	signer, err := kms.NewEphemeralSigner("test-" + t.Name())
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return signer
}

// TestEnvelopeRoundtrip builds an envelope and verifies the Ed25519 signature externally.
func TestEnvelopeRoundtrip(t *testing.T) {
	signer := newTestSigner(t)

	request := map[string]interface{}{
		"name":      "fetch_document",
		"arguments": map[string]interface{}{"url": "https://example.com/doc.txt"},
	}
	result := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "Hello, World!"},
		},
	}

	env, err := envelope.BuildEnvelope(
		request, result,
		"did:web:example.com:tools:fetch",
		"1.0.0",
		signer,
	)
	if err != nil {
		t.Fatalf("BuildEnvelope: %v", err)
	}

	if env.Signature == nil || env.Signature.Value == "" {
		t.Fatal("envelope has no signature value")
	}

	envForVerify := *env
	sigCopy := *env.Signature
	sigCopy.Value = ""
	envForVerify.Signature = &sigCopy

	canonical, err := canonicalize.Canonicalize(&envForVerify)
	if err != nil {
		t.Fatalf("canonicalize for verify: %v", err)
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(env.Signature.Value)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}

	if !ed25519.Verify(signer.PublicKey(), canonical, sigBytes) {
		t.Error("signature verification FAILED")
	}
}

// TestRequestDigestDeterministic confirms identical input produces identical digests.
func TestRequestDigestDeterministic(t *testing.T) {
	signer := newTestSigner(t)

	request := map[string]interface{}{
		"name":      "search",
		"arguments": map[string]interface{}{"query": "determinism test", "limit": float64(10)},
	}
	result := map[string]interface{}{"content": []interface{}{}}

	env1, err := envelope.BuildEnvelope(request, result, "did:web:example.com", "1.0.0", signer)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	env2, err := envelope.BuildEnvelope(request, result, "did:web:example.com", "1.0.0", signer)
	if err != nil {
		t.Fatalf("second build: %v", err)
	}

	if env1.RequestDigest != env2.RequestDigest {
		t.Errorf("non-deterministic request digest: %q vs %q", env1.RequestDigest, env2.RequestDigest)
	}
	if env1.ResultDigest != env2.ResultDigest {
		t.Errorf("non-deterministic result digest: %q vs %q", env1.ResultDigest, env2.ResultDigest)
	}
}

// TestCanonicalFormOrderingIndependent confirms {a:1,b:2} and {b:2,a:1} produce identical bytes.
func TestCanonicalFormOrderingIndependent(t *testing.T) {
	a := map[string]interface{}{"a": float64(1), "b": float64(2)}
	b := map[string]interface{}{"b": float64(2), "a": float64(1)}

	ca, err := canonicalize.Canonicalize(a)
	if err != nil {
		t.Fatalf("canonicalize a: %v", err)
	}
	cb, err := canonicalize.Canonicalize(b)
	if err != nil {
		t.Fatalf("canonicalize b: %v", err)
	}

	if string(ca) != string(cb) {
		t.Errorf("canonical forms differ: %q vs %q", string(ca), string(cb))
	}

	expected := `{"a":1,"b":2}`
	if string(ca) != expected {
		t.Errorf("canonical form: got %q, want %q", string(ca), expected)
	}
}

// TestCanonicalFormNestedSort confirms recursive key sorting works.
func TestCanonicalFormNestedSort(t *testing.T) {
	input := map[string]interface{}{
		"z": map[string]interface{}{"b": "two", "a": "one"},
		"a": float64(1),
	}
	got, err := canonicalize.Canonicalize(input)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	expected := `{"a":1,"z":{"a":"one","b":"two"}}`
	if string(got) != expected {
		t.Errorf("nested canonical form: got %q, want %q", string(got), expected)
	}
}

// TestEnvelopeFields checks that required v2.1 spec fields are all populated.
func TestEnvelopeFields(t *testing.T) {
	signer := newTestSigner(t)

	env, err := envelope.BuildEnvelope(
		map[string]interface{}{"name": "test"},
		map[string]interface{}{"content": []interface{}{}},
		"did:web:example.com",
		"2.0.0",
		signer,
	)
	if err != nil {
		t.Fatalf("BuildEnvelope: %v", err)
	}

	if env.Version == "" {
		t.Error("version is empty")
	}
	if env.ToolCallID == "" {
		t.Error("tool_call_id is empty")
	}
	if env.ToolID != "did:web:example.com" {
		t.Errorf("tool_id: got %q", env.ToolID)
	}
	if env.ToolVersion != "2.0.0" {
		t.Errorf("tool_version: got %q", env.ToolVersion)
	}
	if env.InvokedAt == "" {
		t.Error("invoked_at is empty")
	}
	if env.InvokedBy == "" {
		t.Error("invoked_by is empty")
	}
	if env.RequestDigest == "" {
		t.Error("request_digest is empty")
	}
	if env.ResultDigest == "" {
		t.Error("result_digest is empty")
	}
	if len(env.Sources) == 0 {
		t.Error("sources is empty")
	}
	if env.Signature == nil || env.Signature.Value == "" {
		t.Error("signature is missing")
	}
	if env.Signature.ProtectedHeader.Alg != "Ed25519" {
		t.Errorf("alg: got %q", env.Signature.ProtectedHeader.Alg)
	}
}
