// Package envelope builds and signs MCP Provenance Envelopes per spec v2.1 § 6.
package envelope

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/enchanter-ai/mimir/issuer/canonicalize"
	"github.com/enchanter-ai/mimir/issuer/kms"
	"github.com/enchanter-ai/mimir/issuer/types"
	"github.com/google/uuid"
)

const (
	envelopeVersion = "mcp-provenance/2026-05-13-ed25519"
	invokedByStub   = "did:enchanter:unverified" // v2.1 § 6.7 clause 3 placeholder
)

// BuildEnvelope constructs, signs, and returns a complete Provenance Envelope.
//
// Parameters:
//   - request:     the raw tools/call request parameters (any JSON-serialisable value)
//   - result:      the raw tools/call result (any JSON-serialisable value)
//   - toolID:      DID identifying the tool (e.g. "did:web:example.com")
//   - toolVersion: semver string for the tool
//   - signer:      kms.Signer implementation (ephemeral, mock, or AWS KMS)
//
// The keyID embedded in the envelope's protected header is taken from signer.KeyID().
func BuildEnvelope(
	request, result interface{},
	toolID, toolVersion string,
	signer kms.Signer,
) (*types.Envelope, error) {
	return buildEnvelopeCtx(context.Background(), request, result, toolID, toolVersion, signer)
}

// BuildEnvelopeCtx is like BuildEnvelope but accepts a context, propagated to
// the Signer so AWS KMS calls can respect deadlines and cancellation.
func BuildEnvelopeCtx(
	ctx context.Context,
	request, result interface{},
	toolID, toolVersion string,
	signer kms.Signer,
) (*types.Envelope, error) {
	return buildEnvelopeCtx(ctx, request, result, toolID, toolVersion, signer)
}

func buildEnvelopeCtx(
	ctx context.Context,
	request, result interface{},
	toolID, toolVersion string,
	signer kms.Signer,
) (*types.Envelope, error) {

	// --- Step 1: compute request digest ---
	reqDigest, err := computeDigest(request)
	if err != nil {
		return nil, fmt.Errorf("build_envelope: request digest: %w", err)
	}

	// --- Step 2: compute result digest ---
	// Per spec, digest is over the result's "content" array if present,
	// otherwise over the full result object.
	resultTarget, err := extractResultContent(result)
	if err != nil {
		return nil, fmt.Errorf("build_envelope: result extraction: %w", err)
	}
	resDigest, err := computeDigest(resultTarget)
	if err != nil {
		return nil, fmt.Errorf("build_envelope: result digest: %w", err)
	}

	// --- Step 3: assemble envelope (signature.value absent for signing) ---
	env := &types.Envelope{
		Version:       envelopeVersion,
		ToolCallID:    uuid.NewString(), // MVP stub; real call-ID comes from the MCP client
		ToolID:        toolID,
		ToolVersion:   toolVersion,
		InvokedAt:     time.Now().UTC().Format(time.RFC3339),
		InvokedBy:     invokedByStub,
		RequestDigest: reqDigest,
		ResultDigest:  resDigest,
		Sources:       stubSources(),
		Signature: &types.Signature{
			ProtectedHeader: types.ProtectedHeader{
				Alg:   "Ed25519",
				KeyID: signer.KeyID(),
			},
			// Value intentionally absent here — set after signing
		},
	}

	// --- Step 4: canonical bytes for signing (signature.value must be absent) ---
	canonical, err := canonicalize.Canonicalize(env)
	if err != nil {
		return nil, fmt.Errorf("build_envelope: canonicalize for signing: %w", err)
	}

	// --- Step 5: sign via the Signer interface ---
	sig, err := signer.Sign(ctx, canonical)
	if err != nil {
		return nil, fmt.Errorf("build_envelope: sign: %w", err)
	}

	// --- Step 6: attach signature (base64url, no padding) ---
	env.Signature.Value = base64.RawURLEncoding.EncodeToString(sig)

	return env, nil
}

// computeDigest returns "sha-256:<hex>" over the RFC 8785 canonical form of v.
func computeDigest(v interface{}) (string, error) {
	canonical, err := canonicalize.Canonicalize(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return fmt.Sprintf("sha-256:%x", sum), nil
}

// extractResultContent returns result.content if the field exists,
// otherwise returns result unchanged.
func extractResultContent(result interface{}) (interface{}, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		// Not an object — digest the whole thing.
		return result, nil
	}
	if content, ok := m["content"]; ok {
		return content, nil
	}
	return result, nil
}

// stubSources returns a single placeholder source. The real scoring engine
// replaces this with evidence-backed entries.
func stubSources() []types.Source {
	return []types.Source{
		{
			URI:        "stub:scoring-engine-not-yet-integrated",
			Confidence: 0.0,
			Label:      "MVP stub — replace with real scoring engine output",
		},
	}
}
