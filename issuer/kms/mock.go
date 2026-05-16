package kms

import (
	"context"
	"crypto/ed25519"
)

// hardcodedSeed is a fixed 32-byte seed used by MockKMS so that tests are
// deterministic across runs. Never use this seed outside of tests.
var hardcodedSeed = [32]byte{
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
	0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20,
}

// MockKMS implements Signer without any real KMS dependency.
// It generates a deterministic Ed25519 keypair from a hard-coded seed,
// making it safe and stable for unit tests.
//
// Behaviour is identical to AWSSigner for sign/verify purposes:
//   - Sign returns raw 64-byte Ed25519 signatures.
//   - PublicKey returns a stable 32-byte key.
//   - KeyID returns the constant "mock-kms-key".
type MockKMS struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

// NewMockKMS constructs a MockKMS from the package-level hard-coded seed.
func NewMockKMS() *MockKMS {
	priv := ed25519.NewKeyFromSeed(hardcodedSeed[:])
	return &MockKMS{
		pub:  priv.Public().(ed25519.PublicKey),
		priv: priv,
	}
}

// Sign signs message using the deterministic private key.
// Returns raw 64-byte Ed25519 signature; never returns an error.
func (m *MockKMS) Sign(_ context.Context, message []byte) ([]byte, error) {
	return ed25519.Sign(m.priv, message), nil
}

// PublicKey returns the deterministic 32-byte public key.
func (m *MockKMS) PublicKey() ed25519.PublicKey { return m.pub }

// KeyID returns the fixed identifier "mock-kms-key".
func (m *MockKMS) KeyID() string { return "mock-kms-key" }
