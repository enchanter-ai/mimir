// Package kms defines the Signer interface and its implementations for
// Ed25519 key custody: ephemeral (dev), mock (test), and AWS KMS (production).
package kms

import (
	"context"
	"crypto/ed25519"
)

// Signer abstracts Ed25519 signing regardless of key custody backend.
//
// Implementations:
//   - EphemeralSigner — in-process key, dev/local use only.
//   - MockKMS        — fixed seed, deterministic; safe for unit tests.
//   - AWSSigner      — AWS KMS-backed; production key custody.
type Signer interface {
	// Sign returns a raw 64-byte Ed25519 signature over message.
	// The caller must not pre-hash the message; Sign handles any
	// backend-specific hashing requirements internally.
	Sign(ctx context.Context, message []byte) ([]byte, error)

	// PublicKey returns the 32-byte Ed25519 public key corresponding
	// to the active signing key. The value is stable for the lifetime
	// of the Signer.
	PublicKey() ed25519.PublicKey

	// KeyID returns an opaque identifier for the active key (e.g. a
	// UUID for ephemeral keys, or a KMS key ARN for AWS keys).
	KeyID() string
}
