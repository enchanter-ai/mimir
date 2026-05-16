package kms

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
)

// EphemeralSigner implements Signer using an in-process Ed25519 keypair
// generated at construction time.
//
// WARNING: The private key lives only in memory and is lost on process exit.
// This is intentionally the dev-mode default and MUST NOT be used in production.
// Use AWSSigner for production key custody.
type EphemeralSigner struct {
	pub   ed25519.PublicKey
	priv  ed25519.PrivateKey
	keyID string
}

// NewEphemeralSigner generates a fresh Ed25519 keypair and returns an
// EphemeralSigner. keyID is any opaque string the caller wants embedded in
// envelope protected headers; pass "" to get an auto-generated value.
func NewEphemeralSigner(keyID string) (*EphemeralSigner, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ephemeral signer: keygen: %w", err)
	}
	if keyID == "" {
		keyID = "ephemeral-" + randomHex8()
	}
	return &EphemeralSigner{pub: pub, priv: priv, keyID: keyID}, nil
}

// Sign signs message with the in-process private key using stdlib ed25519.Sign.
// The signature is always 64 bytes (raw Ed25519, no DER wrapping).
func (e *EphemeralSigner) Sign(_ context.Context, message []byte) ([]byte, error) {
	return ed25519.Sign(e.priv, message), nil
}

// PublicKey returns the 32-byte public key.
func (e *EphemeralSigner) PublicKey() ed25519.PublicKey { return e.pub }

// KeyID returns the key identifier set at construction.
func (e *EphemeralSigner) KeyID() string { return e.keyID }
