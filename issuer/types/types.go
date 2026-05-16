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
type AttestRequest struct {
	Request     interface{} `json:"request"`
	Result      interface{} `json:"result"`
	ToolID      string      `json:"tool_id"`
	ToolVersion string      `json:"tool_version"`
}

// AttestResponse is the JSON body returned by POST /v1/attest.
type AttestResponse struct {
	Envelope        *Envelope `json:"envelope"`
	ValidationLevel string    `json:"validation_level"`
}

// JWKPublicKey is a minimal JWK representation for an OKP Ed25519 key.
type JWKPublicKey struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Kid string `json:"kid"`
	Use string `json:"use"`
}
