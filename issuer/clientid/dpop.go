// Package clientid implements the ClientIdentityProof extension from spec § 6.11.
//
// The extension lets a tool-call consumer (the MCP client / agent / end-user)
// bind their identity to the envelope by signing a DPoP proof (RFC 9449) over
// the attest request. The issuer verifies the proof, computes the proof key's
// JWK thumbprint (RFC 7638), and writes `did:jwk:<thumbprint>` into the
// envelope's `invoked_by` field instead of the stub `did:enchanter:unverified`.
//
// Verification surface
// --------------------
// A DPoP proof is a 3-segment JWT (RFC 7519). We accept the subset that maps
// cleanly to Mimir's needs:
//
//   - Header `typ` MUST be `dpop+jwt`.
//   - Header `alg` MUST be one of `EdDSA` (OKP/Ed25519) or `ES256` (EC/P-256).
//     These two cover real-world DPoP clients (the WebAuthn / passkey crowd
//     uses ES256; the JWKS-everywhere crowd uses EdDSA).
//   - Header `jwk` is the public key the proof is signed with. We use ONLY
//     this embedded key; we do not look up an external trust anchor (DPoP
//     scopes trust to "proof of possession", not "trusted issuer").
//   - Payload `htm` MUST equal the HTTP method of the current request.
//   - Payload `htu` MUST match the request URL (path + query, no fragment).
//   - Payload `iat` MUST be within ±maxAgeSeconds of the server clock.
//   - Payload `jti` MUST be present (for downstream nonce tracking; we do
//     not currently cache jti — operators should do that at the gateway if
//     they want strict single-use semantics).
//
// What we DO NOT check (deliberately, with rationale):
//   - `ath` (access-token-hash). Mimir is not an OAuth resource server; there
//     is no bearer token to bind to. Setting `ath` is harmless but ignored.
//   - Cross-request nonce uniqueness. Bounded by the iat window check and by
//     gateway-layer nonce tracking, NOT here.
//
// Output of a successful Verify(): the RFC 7638 thumbprint of the proof's
// JWK, which is what callers persist as the stable client identity.
package clientid

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Error categories — operators / verifiers should be able to distinguish these.
var (
	ErrMalformed       = errors.New("clientid: DPoP proof malformed")
	ErrUnsupportedAlg  = errors.New("clientid: unsupported DPoP alg")
	ErrWrongTyp        = errors.New("clientid: DPoP header typ must be 'dpop+jwt'")
	ErrMethodMismatch  = errors.New("clientid: DPoP htm does not match request method")
	ErrURIMismatch     = errors.New("clientid: DPoP htu does not match request URI")
	ErrIATOutOfWindow  = errors.New("clientid: DPoP iat outside acceptable window")
	ErrMissingJTI      = errors.New("clientid: DPoP payload missing jti")
	ErrInvalidSig      = errors.New("clientid: DPoP signature did not verify")
	ErrInvalidJWK      = errors.New("clientid: embedded JWK invalid")
)

// VerifyParams bundles the inputs to Verify so callers can't accidentally swap.
type VerifyParams struct {
	Proof         string        // raw `DPoP` header / body field value
	HTTPMethod    string        // e.g. "POST"
	HTTPURI       string        // exact URL the proof must commit to
	MaxClockSkew  time.Duration // how far iat can drift from `Now` in either direction
	Now           time.Time     // injectable for tests; defaults to time.Now()
}

// Result is what a successful Verify returns. Thumbprint is the RFC 7638
// JWK thumbprint of the proof's embedded public key — this is the stable
// client identity persisted into envelope.invoked_by.
type Result struct {
	Thumbprint string  // base64url-no-pad of SHA-256 over the canonical JWK
	DID        string  // "did:jwk:<Thumbprint>"
	Alg        string  // the JWT alg actually used ("EdDSA" or "ES256")
	JTI        string  // payload jti, for callers that want to enforce single-use
	IssuedAt   int64   // payload iat (unix seconds)
}

// Verify validates a DPoP proof per the rules in this package's doc. Returns
// the proof's thumbprint + the alg/jti/iat on success.
func Verify(p VerifyParams) (*Result, error) {
	if p.Proof == "" {
		return nil, fmt.Errorf("%w: empty proof", ErrMalformed)
	}
	if p.Now.IsZero() {
		p.Now = time.Now()
	}
	if p.MaxClockSkew <= 0 {
		p.MaxClockSkew = 60 * time.Second
	}

	// --- 1. Split into 3 segments. ---
	parts := strings.Split(p.Proof, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected 3 segments, got %d", ErrMalformed, len(parts))
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header b64: %v", ErrMalformed, err)
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload b64: %v", ErrMalformed, err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: signature b64: %v", ErrMalformed, err)
	}

	// --- 2. Parse header. ---
	var hdr struct {
		Typ string          `json:"typ"`
		Alg string          `json:"alg"`
		JWK json.RawMessage `json:"jwk"`
	}
	if err := json.Unmarshal(headerJSON, &hdr); err != nil {
		return nil, fmt.Errorf("%w: header parse: %v", ErrMalformed, err)
	}
	if hdr.Typ != "dpop+jwt" {
		return nil, fmt.Errorf("%w: typ=%q", ErrWrongTyp, hdr.Typ)
	}
	switch hdr.Alg {
	case "EdDSA", "ES256":
		// supported
	default:
		return nil, fmt.Errorf("%w: alg=%q", ErrUnsupportedAlg, hdr.Alg)
	}

	// --- 3. Parse payload. ---
	var payload struct {
		HTM string `json:"htm"`
		HTU string `json:"htu"`
		IAT int64  `json:"iat"`
		JTI string `json:"jti"`
	}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, fmt.Errorf("%w: payload parse: %v", ErrMalformed, err)
	}
	if !strings.EqualFold(payload.HTM, p.HTTPMethod) {
		return nil, fmt.Errorf("%w: proof htm=%q, request method=%q", ErrMethodMismatch, payload.HTM, p.HTTPMethod)
	}
	if payload.HTU != p.HTTPURI {
		return nil, fmt.Errorf("%w: proof htu=%q, request uri=%q", ErrURIMismatch, payload.HTU, p.HTTPURI)
	}
	now := p.Now.Unix()
	skew := int64(p.MaxClockSkew / time.Second)
	if payload.IAT < now-skew || payload.IAT > now+skew {
		return nil, fmt.Errorf("%w: iat=%d server_now=%d skew=±%ds", ErrIATOutOfWindow, payload.IAT, now, skew)
	}
	if payload.JTI == "" {
		return nil, ErrMissingJTI
	}

	// --- 4. Verify signature against the embedded JWK. ---
	signingInput := []byte(parts[0] + "." + parts[1])
	if err := verifySig(hdr.JWK, hdr.Alg, signingInput, sig); err != nil {
		return nil, err
	}

	// --- 5. Compute thumbprint. ---
	thumb, err := thumbprint(hdr.JWK)
	if err != nil {
		return nil, fmt.Errorf("%w: thumbprint: %v", ErrInvalidJWK, err)
	}

	return &Result{
		Thumbprint: thumb,
		DID:        "did:jwk:" + thumb,
		Alg:        hdr.Alg,
		JTI:        payload.JTI,
		IssuedAt:   payload.IAT,
	}, nil
}

// verifySig dispatches to the alg-specific verifier.
func verifySig(jwkRaw json.RawMessage, alg string, signingInput, sig []byte) error {
	switch alg {
	case "EdDSA":
		return verifyEdDSA(jwkRaw, signingInput, sig)
	case "ES256":
		return verifyES256(jwkRaw, signingInput, sig)
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlg, alg)
	}
}

func verifyEdDSA(jwkRaw json.RawMessage, signingInput, sig []byte) error {
	var jwk struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		X   string `json:"x"`
	}
	if err := json.Unmarshal(jwkRaw, &jwk); err != nil {
		return fmt.Errorf("%w: jwk parse: %v", ErrInvalidJWK, err)
	}
	if jwk.Kty != "OKP" || jwk.Crv != "Ed25519" {
		return fmt.Errorf("%w: EdDSA requires kty=OKP crv=Ed25519, got kty=%q crv=%q",
			ErrInvalidJWK, jwk.Kty, jwk.Crv)
	}
	pub, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: Ed25519 x decode: %v (len=%d)", ErrInvalidJWK, err, len(pub))
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: Ed25519 signature length %d (want %d)",
			ErrInvalidSig, len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), signingInput, sig) {
		return ErrInvalidSig
	}
	return nil
}

func verifyES256(jwkRaw json.RawMessage, signingInput, sig []byte) error {
	var jwk struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}
	if err := json.Unmarshal(jwkRaw, &jwk); err != nil {
		return fmt.Errorf("%w: jwk parse: %v", ErrInvalidJWK, err)
	}
	if jwk.Kty != "EC" || jwk.Crv != "P-256" {
		return fmt.Errorf("%w: ES256 requires kty=EC crv=P-256, got kty=%q crv=%q",
			ErrInvalidJWK, jwk.Kty, jwk.Crv)
	}
	xb, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		return fmt.Errorf("%w: EC x decode: %v", ErrInvalidJWK, err)
	}
	yb, err := base64.RawURLEncoding.DecodeString(jwk.Y)
	if err != nil {
		return fmt.Errorf("%w: EC y decode: %v", ErrInvalidJWK, err)
	}
	if len(xb) != 32 || len(yb) != 32 {
		return fmt.Errorf("%w: P-256 coords must be 32 bytes (got x=%d y=%d)",
			ErrInvalidJWK, len(xb), len(yb))
	}
	pub := &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(xb),
		Y:     new(big.Int).SetBytes(yb),
	}
	if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return fmt.Errorf("%w: EC point not on P-256", ErrInvalidJWK)
	}
	// JWS ES256 signature is raw R || S, 64 bytes total. NOT DER-encoded.
	if len(sig) != 64 {
		return fmt.Errorf("%w: ES256 signature length %d (want 64)", ErrInvalidSig, len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	hash := sha256.Sum256(signingInput)
	if !ecdsa.Verify(pub, hash[:], r, s) {
		return ErrInvalidSig
	}
	return nil
}

// thumbprint computes the RFC 7638 JWK thumbprint over the embedded key.
// Canonical form is JSON with the REQUIRED members of the key type in
// lexicographic order, no whitespace.
func thumbprint(jwkRaw json.RawMessage) (string, error) {
	var probe struct {
		Kty string `json:"kty"`
	}
	if err := json.Unmarshal(jwkRaw, &probe); err != nil {
		return "", fmt.Errorf("kty probe: %w", err)
	}
	var canonical []byte
	switch probe.Kty {
	case "OKP":
		var k struct {
			Crv string `json:"crv"`
			X   string `json:"x"`
		}
		if err := json.Unmarshal(jwkRaw, &k); err != nil {
			return "", err
		}
		// Per RFC 8037 § 2 for OKP, required members are crv, kty, x.
		canonical = []byte(fmt.Sprintf(`{"crv":%q,"kty":"OKP","x":%q}`, k.Crv, k.X))
	case "EC":
		var k struct {
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		}
		if err := json.Unmarshal(jwkRaw, &k); err != nil {
			return "", err
		}
		// Per RFC 7638 § 3.2 for EC, required members are crv, kty, x, y.
		canonical = []byte(fmt.Sprintf(`{"crv":%q,"kty":"EC","x":%q,"y":%q}`, k.Crv, k.X, k.Y))
	default:
		return "", fmt.Errorf("unsupported kty %q for thumbprint", probe.Kty)
	}
	h := sha256.Sum256(canonical)
	return base64.RawURLEncoding.EncodeToString(h[:]), nil
}
