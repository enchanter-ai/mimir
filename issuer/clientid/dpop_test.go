package clientid

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"
)

// ─── Test helpers ─────────────────────────────────────────────────────────

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// makeEdDSAProof returns a fully-formed DPoP proof + the thumbprint we expect Verify to compute.
func makeEdDSAProof(t *testing.T, htm, htu string, iat int64, jti string) (string, ed25519.PublicKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	hdr := map[string]any{
		"typ": "dpop+jwt",
		"alg": "EdDSA",
		"jwk": map[string]any{
			"kty": "OKP",
			"crv": "Ed25519",
			"x":   b64u(pub),
		},
	}
	payload := map[string]any{
		"htm": htm,
		"htu": htu,
		"iat": iat,
		"jti": jti,
	}
	hdrJSON, _ := json.Marshal(hdr)
	payloadJSON, _ := json.Marshal(payload)
	signingInput := b64u(hdrJSON) + "." + b64u(payloadJSON)
	sig := ed25519.Sign(priv, []byte(signingInput))
	proof := signingInput + "." + b64u(sig)

	// Compute expected thumbprint for cross-check.
	canonical := []byte(fmt.Sprintf(`{"crv":"Ed25519","kty":"OKP","x":%q}`, b64u(pub)))
	h := sha256.Sum256(canonical)
	return proof, pub, b64u(h[:])
}

func makeES256Proof(t *testing.T, htm, htu string, iat int64, jti string) (string, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	xb := priv.PublicKey.X.Bytes()
	yb := priv.PublicKey.Y.Bytes()
	// pad to 32 bytes (P-256 coords) — required for JWS ES256 raw encoding.
	xb = leftPad(xb, 32)
	yb = leftPad(yb, 32)

	hdr := map[string]any{
		"typ": "dpop+jwt",
		"alg": "ES256",
		"jwk": map[string]any{
			"kty": "EC",
			"crv": "P-256",
			"x":   b64u(xb),
			"y":   b64u(yb),
		},
	}
	payload := map[string]any{"htm": htm, "htu": htu, "iat": iat, "jti": jti}
	hdrJSON, _ := json.Marshal(hdr)
	payloadJSON, _ := json.Marshal(payload)
	signingInput := b64u(hdrJSON) + "." + b64u(payloadJSON)
	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, priv, hash[:])
	if err != nil {
		t.Fatalf("ecdsa.Sign: %v", err)
	}
	sig := append(leftPad(r.Bytes(), 32), leftPad(s.Bytes(), 32)...)
	proof := signingInput + "." + b64u(sig)

	canonical := []byte(fmt.Sprintf(`{"crv":"P-256","kty":"EC","x":%q,"y":%q}`, b64u(xb), b64u(yb)))
	h := sha256.Sum256(canonical)
	return proof, b64u(h[:])
}

func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		return b
	}
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}

// ─── Tests ───────────────────────────────────────────────────────────────

func TestEdDSAHappyPath(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	proof, _, expected := makeEdDSAProof(t, "POST", "https://issuer.example/v1/attest", now.Unix(), "jti-abc-123")

	r, err := Verify(VerifyParams{
		Proof:      proof,
		HTTPMethod: "POST",
		HTTPURI:    "https://issuer.example/v1/attest",
		Now:        now,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if r.Alg != "EdDSA" {
		t.Errorf("alg: got %q, want EdDSA", r.Alg)
	}
	if r.Thumbprint != expected {
		t.Errorf("thumbprint mismatch:\n  got:  %s\n  want: %s", r.Thumbprint, expected)
	}
	if r.DID != "did:jwk:"+expected {
		t.Errorf("DID format wrong: %q", r.DID)
	}
	if r.JTI != "jti-abc-123" {
		t.Errorf("jti round-trip: %q", r.JTI)
	}
}

func TestES256HappyPath(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	proof, expected := makeES256Proof(t, "POST", "https://issuer.example/v1/attest-mcp", now.Unix(), "jti-xyz")

	r, err := Verify(VerifyParams{
		Proof:      proof,
		HTTPMethod: "POST",
		HTTPURI:    "https://issuer.example/v1/attest-mcp",
		Now:        now,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if r.Alg != "ES256" {
		t.Errorf("alg: got %q, want ES256", r.Alg)
	}
	if r.Thumbprint != expected {
		t.Errorf("thumbprint mismatch:\n  got:  %s\n  want: %s", r.Thumbprint, expected)
	}
}

func TestThumbprintIsStable(t *testing.T) {
	// Same key MUST produce the same thumbprint across two independent
	// Verify calls — otherwise we cannot use it as a stable identity.
	now := time.Now()
	jwk := map[string]any{
		"kty": "OKP",
		"crv": "Ed25519",
		"x":   b64u(make([]byte, 32)), // fixed bytes don't matter for stability
	}
	hdr1 := map[string]any{"typ": "dpop+jwt", "alg": "EdDSA", "jwk": jwk}
	// Re-marshal jwk into different key orders — JSON unmarshal must normalise.
	jwk2 := map[string]any{
		"x":   b64u(make([]byte, 32)),
		"kty": "OKP",
		"crv": "Ed25519",
	}
	hdr2 := map[string]any{"alg": "EdDSA", "jwk": jwk2, "typ": "dpop+jwt"}

	thumb1, err := thumbprint(mustJSON(t, hdr1["jwk"]))
	if err != nil {
		t.Fatalf("thumbprint 1: %v", err)
	}
	thumb2, err := thumbprint(mustJSON(t, hdr2["jwk"]))
	if err != nil {
		t.Fatalf("thumbprint 2: %v", err)
	}
	if thumb1 != thumb2 {
		t.Errorf("thumbprint depends on map order; got %q vs %q", thumb1, thumb2)
	}
	_ = now
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

// ─── Rejection-path tests ────────────────────────────────────────────────

func TestRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"only.two",
		"a.b.c.d",
		"!!!.!!!.!!!", // not base64
	}
	for _, c := range cases {
		_, err := Verify(VerifyParams{Proof: c, HTTPMethod: "POST", HTTPURI: "/x", Now: time.Now()})
		if err == nil {
			t.Errorf("expected error for malformed proof %q", c)
		} else if !errors.Is(err, ErrMalformed) {
			t.Errorf("proof %q: expected ErrMalformed, got %v", c, err)
		}
	}
}

func TestRejectsWrongTyp(t *testing.T) {
	now := time.Now()
	proof, _, _ := makeEdDSAProof(t, "POST", "/x", now.Unix(), "j")
	// Tamper with header: replace dpop+jwt with plain JWT
	parts := strings.Split(proof, ".")
	hdrJSON, _ := base64.RawURLEncoding.DecodeString(parts[0])
	tampered := strings.Replace(string(hdrJSON), `"typ":"dpop+jwt"`, `"typ":"JWT"`, 1)
	parts[0] = b64u([]byte(tampered))
	tamperedProof := strings.Join(parts, ".")

	_, err := Verify(VerifyParams{Proof: tamperedProof, HTTPMethod: "POST", HTTPURI: "/x", Now: now})
	if !errors.Is(err, ErrWrongTyp) {
		t.Errorf("expected ErrWrongTyp, got %v", err)
	}
}

func TestRejectsUnsupportedAlg(t *testing.T) {
	now := time.Now()
	hdr := map[string]any{
		"typ": "dpop+jwt",
		"alg": "RS256",
		"jwk": map[string]any{"kty": "RSA", "n": "...", "e": "AQAB"},
	}
	payload := map[string]any{"htm": "POST", "htu": "/x", "iat": now.Unix(), "jti": "j"}
	hdrJSON, _ := json.Marshal(hdr)
	payloadJSON, _ := json.Marshal(payload)
	proof := b64u(hdrJSON) + "." + b64u(payloadJSON) + "." + b64u([]byte("fake-sig"))

	_, err := Verify(VerifyParams{Proof: proof, HTTPMethod: "POST", HTTPURI: "/x", Now: now})
	if !errors.Is(err, ErrUnsupportedAlg) {
		t.Errorf("expected ErrUnsupportedAlg, got %v", err)
	}
}

func TestRejectsMethodMismatch(t *testing.T) {
	now := time.Now()
	proof, _, _ := makeEdDSAProof(t, "POST", "/x", now.Unix(), "j")
	_, err := Verify(VerifyParams{Proof: proof, HTTPMethod: "GET", HTTPURI: "/x", Now: now})
	if !errors.Is(err, ErrMethodMismatch) {
		t.Errorf("expected ErrMethodMismatch, got %v", err)
	}
}

func TestRejectsURIMismatch(t *testing.T) {
	now := time.Now()
	proof, _, _ := makeEdDSAProof(t, "POST", "https://issuer/v1/attest", now.Unix(), "j")
	_, err := Verify(VerifyParams{Proof: proof, HTTPMethod: "POST", HTTPURI: "https://attacker/v1/attest", Now: now})
	if !errors.Is(err, ErrURIMismatch) {
		t.Errorf("expected ErrURIMismatch, got %v", err)
	}
}

func TestRejectsStaleIAT(t *testing.T) {
	serverNow := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	// proof was generated 10 minutes ago
	proof, _, _ := makeEdDSAProof(t, "POST", "/x", serverNow.Add(-10*time.Minute).Unix(), "j")
	_, err := Verify(VerifyParams{
		Proof: proof, HTTPMethod: "POST", HTTPURI: "/x",
		Now: serverNow, MaxClockSkew: 60 * time.Second,
	})
	if !errors.Is(err, ErrIATOutOfWindow) {
		t.Errorf("expected ErrIATOutOfWindow, got %v", err)
	}
}

func TestRejectsFutureIAT(t *testing.T) {
	serverNow := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	proof, _, _ := makeEdDSAProof(t, "POST", "/x", serverNow.Add(10*time.Minute).Unix(), "j")
	_, err := Verify(VerifyParams{
		Proof: proof, HTTPMethod: "POST", HTTPURI: "/x",
		Now: serverNow, MaxClockSkew: 60 * time.Second,
	})
	if !errors.Is(err, ErrIATOutOfWindow) {
		t.Errorf("expected ErrIATOutOfWindow, got %v", err)
	}
}

func TestRejectsMissingJTI(t *testing.T) {
	now := time.Now()
	// Build a proof with no jti.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	hdr := map[string]any{
		"typ": "dpop+jwt", "alg": "EdDSA",
		"jwk": map[string]any{"kty": "OKP", "crv": "Ed25519", "x": b64u(pub)},
	}
	payload := map[string]any{"htm": "POST", "htu": "/x", "iat": now.Unix()}
	hdrJSON, _ := json.Marshal(hdr)
	payloadJSON, _ := json.Marshal(payload)
	signingInput := b64u(hdrJSON) + "." + b64u(payloadJSON)
	sig := ed25519.Sign(priv, []byte(signingInput))
	proof := signingInput + "." + b64u(sig)

	_, err := Verify(VerifyParams{Proof: proof, HTTPMethod: "POST", HTTPURI: "/x", Now: now})
	if !errors.Is(err, ErrMissingJTI) {
		t.Errorf("expected ErrMissingJTI, got %v", err)
	}
}

func TestRejectsTamperedSignature(t *testing.T) {
	now := time.Now()
	proof, _, _ := makeEdDSAProof(t, "POST", "/x", now.Unix(), "j")
	// Flip one byte in the signature segment.
	parts := strings.Split(proof, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sig[0] ^= 0x01
	parts[2] = b64u(sig)
	tampered := strings.Join(parts, ".")

	_, err := Verify(VerifyParams{Proof: tampered, HTTPMethod: "POST", HTTPURI: "/x", Now: now})
	if !errors.Is(err, ErrInvalidSig) {
		t.Errorf("expected ErrInvalidSig, got %v", err)
	}
}

func TestRejectsTamperedPayload(t *testing.T) {
	now := time.Now()
	proof, _, _ := makeEdDSAProof(t, "POST", "/x", now.Unix(), "j")
	// Modify the payload but keep the original signature — must fail.
	parts := strings.Split(proof, ".")
	payloadJSON, _ := base64.RawURLEncoding.DecodeString(parts[1])
	tampered := strings.Replace(string(payloadJSON), `"htm":"POST"`, `"htm":"PUT"`, 1)
	parts[1] = b64u([]byte(tampered))
	tamperedProof := strings.Join(parts, ".")

	_, err := Verify(VerifyParams{Proof: tamperedProof, HTTPMethod: "POST", HTTPURI: "/x", Now: now})
	// Could be method mismatch OR signature mismatch depending on order; both
	// indicate the proof is rejected. The point: it MUST be rejected.
	if err == nil {
		t.Error("expected tampered payload to be rejected")
	}
}

// Tampering with the embedded JWK should fail signature verification (the
// signing input doesn't match the wrong-key checker even if the header parses).
func TestRejectsSwappedJWK(t *testing.T) {
	now := time.Now()
	proof, _, _ := makeEdDSAProof(t, "POST", "/x", now.Unix(), "j")
	parts := strings.Split(proof, ".")
	// Replace the JWK with a different randomly-generated public key.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	newHdr := map[string]any{
		"typ": "dpop+jwt", "alg": "EdDSA",
		"jwk": map[string]any{"kty": "OKP", "crv": "Ed25519", "x": b64u(otherPub)},
	}
	hb, _ := json.Marshal(newHdr)
	parts[0] = b64u(hb)
	swapped := strings.Join(parts, ".")

	_, err := Verify(VerifyParams{Proof: swapped, HTTPMethod: "POST", HTTPURI: "/x", Now: now})
	if !errors.Is(err, ErrInvalidSig) {
		t.Errorf("expected ErrInvalidSig (signing input no longer matches swapped key), got %v", err)
	}
}

func TestES256OnPointNotOnCurveIsRejected(t *testing.T) {
	// Construct a header with an EC point that is not on P-256.
	now := time.Now()
	bad := big.NewInt(7) // any garbage
	xb := leftPad(bad.Bytes(), 32)
	yb := leftPad(bad.Bytes(), 32)
	hdr := map[string]any{
		"typ": "dpop+jwt", "alg": "ES256",
		"jwk": map[string]any{"kty": "EC", "crv": "P-256", "x": b64u(xb), "y": b64u(yb)},
	}
	payload := map[string]any{"htm": "POST", "htu": "/x", "iat": now.Unix(), "jti": "j"}
	hdrJSON, _ := json.Marshal(hdr)
	payloadJSON, _ := json.Marshal(payload)
	proof := b64u(hdrJSON) + "." + b64u(payloadJSON) + "." + b64u(make([]byte, 64))

	_, err := Verify(VerifyParams{Proof: proof, HTTPMethod: "POST", HTTPURI: "/x", Now: now})
	if !errors.Is(err, ErrInvalidJWK) {
		t.Errorf("expected ErrInvalidJWK for off-curve point, got %v", err)
	}
}
