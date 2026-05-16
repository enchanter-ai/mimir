package kms_test

import (
	"context"
	"crypto/ed25519"
	"os"
	"testing"

	"github.com/enchanter-ai/mimir/issuer/kms"
)

var testMessage = []byte("mimir-kms-signer-test-canonical-bytes-2026-05-13")

// TestEphemeralSignVerify ensures that EphemeralSigner produces signatures
// that verify correctly with its own public key.
func TestEphemeralSignVerify(t *testing.T) {
	signer, err := kms.NewEphemeralSigner("")
	if err != nil {
		t.Fatalf("NewEphemeralSigner: %v", err)
	}

	if len(signer.PublicKey()) != ed25519.PublicKeySize {
		t.Fatalf("public key length: got %d, want %d", len(signer.PublicKey()), ed25519.PublicKeySize)
	}
	if signer.KeyID() == "" {
		t.Fatal("KeyID must not be empty")
	}

	sig, err := signer.Sign(context.Background(), testMessage)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != ed25519.SignatureSize {
		t.Fatalf("signature length: got %d, want %d", len(sig), ed25519.SignatureSize)
	}

	if !ed25519.Verify(signer.PublicKey(), testMessage, sig) {
		t.Error("EphemeralSigner: signature verification FAILED")
	}
}

// TestEphemeralKeyIDPrefix verifies that passing a custom keyID is respected.
func TestEphemeralKeyIDPrefix(t *testing.T) {
	signer, err := kms.NewEphemeralSigner("my-custom-id")
	if err != nil {
		t.Fatalf("NewEphemeralSigner: %v", err)
	}
	if signer.KeyID() != "my-custom-id" {
		t.Errorf("KeyID: got %q, want %q", signer.KeyID(), "my-custom-id")
	}
}

// TestMockKMSSignVerify ensures that MockKMS produces valid, verifiable signatures.
func TestMockKMSSignVerify(t *testing.T) {
	m := kms.NewMockKMS()

	if len(m.PublicKey()) != ed25519.PublicKeySize {
		t.Fatalf("mock public key length: got %d, want %d", len(m.PublicKey()), ed25519.PublicKeySize)
	}
	if m.KeyID() != "mock-kms-key" {
		t.Errorf("MockKMS KeyID: got %q, want %q", m.KeyID(), "mock-kms-key")
	}

	sig, err := m.Sign(context.Background(), testMessage)
	if err != nil {
		t.Fatalf("MockKMS Sign: %v", err)
	}
	if len(sig) != ed25519.SignatureSize {
		t.Fatalf("mock signature length: got %d, want %d", len(sig), ed25519.SignatureSize)
	}

	if !ed25519.Verify(m.PublicKey(), testMessage, sig) {
		t.Error("MockKMS: signature verification FAILED")
	}
}

// TestMockKMSIsDeterministic ensures the same message always produces the
// same signature (fixed seed → deterministic RFC 8032 signatures).
func TestMockKMSIsDeterministic(t *testing.T) {
	m := kms.NewMockKMS()

	sig1, _ := m.Sign(context.Background(), testMessage)
	sig2, _ := m.Sign(context.Background(), testMessage)

	if string(sig1) != string(sig2) {
		t.Error("MockKMS signatures are not deterministic across calls")
	}

	// Two independent instances must also produce identical results.
	m2 := kms.NewMockKMS()
	sig3, _ := m2.Sign(context.Background(), testMessage)
	if string(sig1) != string(sig3) {
		t.Error("MockKMS signatures are not deterministic across instances")
	}
}

// TestMockKMSPublicKeyStable confirms that PublicKey() returns the same value
// on repeated calls (important if callers cache the return value).
func TestMockKMSPublicKeyStable(t *testing.T) {
	m := kms.NewMockKMS()
	if string(m.PublicKey()) != string(m.PublicKey()) {
		t.Error("PublicKey() is not stable across calls")
	}
}

// TestAWSKMSDryRun calls AWSSigner against a real KMS key only when the
// AWS_KMS_TEST_KEY_ARN and AWS_KMS_TEST_REGION environment variables are set.
// In CI without AWS credentials, this test is skipped automatically.
//
// To run manually:
//
//	AWS_KMS_TEST_KEY_ARN=arn:aws:kms:us-east-1:123456789012:key/... \
//	AWS_KMS_TEST_REGION=us-east-1 \
//	go test ./kms/ -run TestAWSKMSDryRun -v
func TestAWSKMSDryRun(t *testing.T) {
	keyARN := os.Getenv("AWS_KMS_TEST_KEY_ARN")
	region := os.Getenv("AWS_KMS_TEST_REGION")
	if keyARN == "" || region == "" {
		t.Skip("skipping AWS KMS live test: AWS_KMS_TEST_KEY_ARN and AWS_KMS_TEST_REGION not set")
	}

	ctx := context.Background()

	signer, err := kms.NewAWSSigner(ctx, keyARN, region)
	if err != nil {
		t.Fatalf("NewAWSSigner: %v", err)
	}

	if len(signer.PublicKey()) != ed25519.PublicKeySize {
		t.Fatalf("AWS public key length: got %d, want %d", len(signer.PublicKey()), ed25519.PublicKeySize)
	}
	if signer.KeyID() == "" {
		t.Fatal("AWS KeyID must not be empty")
	}

	sig, err := signer.Sign(ctx, testMessage)
	if err != nil {
		t.Fatalf("AWS KMS Sign: %v", err)
	}
	if len(sig) != ed25519.SignatureSize {
		t.Fatalf("AWS signature length: got %d, want %d", len(sig), ed25519.SignatureSize)
	}

	// Verify locally using stdlib — this confirms the signature is raw Ed25519,
	// not DER-wrapped, and is directly usable in our envelope format.
	if !ed25519.Verify(signer.PublicKey(), testMessage, sig) {
		t.Error("AWS KMS: signature verification FAILED against locally cached public key")
	}
}
