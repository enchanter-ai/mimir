// Package types defines the wire-format structs for the MCP Provenance Envelope v2.1.
// Field names match § 6 of the spec exactly.
package types

// ProtectedHeader carries the algorithm and key identifier used to sign the envelope.
type ProtectedHeader struct {
	Alg   string `json:"alg"`
	KeyID string `json:"key_id"`
}

// Signature holds the protected header and the base64url-encoded (no padding) signature value.
// During canonical-form computation, Value MUST be omitted (set to nil / empty string).
type Signature struct {
	ProtectedHeader ProtectedHeader `json:"protected_header"`
	Value           string          `json:"value,omitempty"`
}

// Source represents a single evidence source attached to the envelope.
// MVP stubs this with a single placeholder entry; the real scoring engine replaces it.
type Source struct {
	URI        string  `json:"uri"`
	Confidence float64 `json:"confidence"`
	Label      string  `json:"label"`
}

// Envelope is the top-level MCP Provenance Envelope (v2.1 spec § 6).
// JSON field names match the spec verbatim.
type Envelope struct {
	Version       string     `json:"version"`
	ToolCallID    string     `json:"tool_call_id"`
	ToolID        string     `json:"tool_id"`
	ToolVersion   string     `json:"tool_version"`
	InvokedAt     string     `json:"invoked_at"`
	InvokedBy     string     `json:"invoked_by"`
	RequestDigest string     `json:"request_digest"`
	ResultDigest  string     `json:"result_digest"`
	Sources       []Source   `json:"sources"`
	Signature     *Signature `json:"signature"`
}

// AttestRequest is the JSON body accepted by POST /v1/attest.
//
// ClientIdentityProof is OPTIONAL (spec § 6.11). When supplied, it MUST be a
// DPoP JWT (RFC 9449) bound to the current HTTP method + URI. If verification
// succeeds, the envelope's invoked_by becomes did:jwk:<thumbprint>; otherwise
// the request is rejected with 400. Clients can alternatively pass the proof
// in the `DPoP` HTTP header — see main.go's clientID() helper.
type AttestRequest struct {
	Request             interface{} `json:"request"`
	Result              interface{} `json:"result"`
	ToolID              string      `json:"tool_id"`
	ToolVersion         string      `json:"tool_version"`
	ClientIdentityProof string      `json:"client_identity_proof,omitempty"`
}

// AttestResponse is the JSON body returned by POST /v1/attest.
type AttestResponse struct {
	Envelope        *Envelope `json:"envelope"`
	ValidationLevel string    `json:"validation_level"`
}

// JWKPublicKey is a minimal JWK representation for an OKP Ed25519 key.
// Optional fields (alg, status) are present to support the JWK-Set publishing
// model used by GET /v1/keys for verifying envelopes signed under rotated keys.
type JWKPublicKey struct {
	Kty    string `json:"kty"`
	Crv    string `json:"crv"`
	X      string `json:"x"`
	Kid    string `json:"kid"`
	Use    string `json:"use"`
	Alg    string `json:"alg,omitempty"`
	Status string `json:"status,omitempty"` // "active" | "retired" | "revoked"
}

// JWKSet is the RFC 7517 § 5 JWK Set representation served at GET /v1/keys.
// Verifiers look up the envelope's signature.protected_header.key_id against
// this set to locate the right public key for verifying historical envelopes.
type JWKSet struct {
	Keys []JWKPublicKey `json:"keys"`
}
