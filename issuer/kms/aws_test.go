package kms

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// TestAWSSignerWithFake exercises the full AWSSigner code path against a
// wire-faithful fake KMS API. The fake matches real KMS's DER-encoded public
// key, raw 64-byte signature output, and input validation rules — so passing
// this test means the production AWSSigner code is compatible with the real
// AWS surface (modulo credentials).
func TestAWSSignerWithFake(t *testing.T) {
	ctx := context.Background()
	const arn = "arn:aws:kms:us-east-1:123456789012:key/00000000-0000-0000-0000-000000000001"

	fake, err := NewAWSKMSFake(arn)
	if err != nil {
		t.Fatalf("NewAWSKMSFake: %v", err)
	}

	signer, err := NewAWSSignerWithAPI(ctx, fake, arn)
	if err != nil {
		t.Fatalf("NewAWSSignerWithAPI: %v", err)
	}

	// Construction should have cached the pub key via one GetPublicKey call.
	if fake.GetPublicKeyCalls != 1 {
		t.Errorf("GetPublicKey calls: got %d, want 1", fake.GetPublicKeyCalls)
	}

	// PublicKey on the signer must equal the fake's underlying key.
	if !signer.PublicKey().Equal(fake.PublicKey()) {
		t.Error("signer.PublicKey() != fake.PublicKey() — DER round-trip lost the key")
	}

	// KeyID round-trip.
	if signer.KeyID() != arn {
		t.Errorf("KeyID: got %q, want %q", signer.KeyID(), arn)
	}

	// Sign a sample message; verify against the fake's pub key directly.
	message := []byte("canonical-envelope-bytes-for-test")
	sig, err := signer.Sign(ctx, message)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != ed25519.SignatureSize {
		t.Errorf("signature length: got %d, want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(fake.PublicKey(), message, sig) {
		t.Error("signature failed external verification against fake pub key")
	}

	// PublicKey caching: subsequent Sign calls must NOT refetch the pub key.
	_, _ = signer.Sign(ctx, message)
	_, _ = signer.Sign(ctx, message)
	if fake.GetPublicKeyCalls != 1 {
		t.Errorf("GetPublicKey was called %d times — expected 1 (caching broken)", fake.GetPublicKeyCalls)
	}
}

// TestAWSSignerRejectsWrongAlgorithm proves the AWSSigner passes the correct
// SigningAlgorithm. We use a fake that rejects anything other than ED25519_SHA_512
// and assert that the wrapper does in fact pass that constant.
func TestAWSSignerSendsCorrectAlgorithm(t *testing.T) {
	ctx := context.Background()
	const arn = "arn:aws:kms:us-east-1:123456789012:key/test"

	fake, err := NewAWSKMSFake(arn)
	if err != nil {
		t.Fatalf("NewAWSKMSFake: %v", err)
	}
	signer, err := NewAWSSignerWithAPI(ctx, fake, arn)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	// The fake will reject any algorithm other than Ed25519Sha512 — so success
	// here is itself the assertion that the wrapper sent the right constant.
	if _, err := signer.Sign(ctx, []byte("x")); err != nil {
		t.Fatalf("Sign should succeed with correct algorithm: %v", err)
	}
}

// TestAWSSignerCatchesBadSigLength proves the wrapper rejects malformed KMS
// responses (defence in depth — if AWS ever returns a non-64-byte sig the
// wrapper must reject rather than silently corrupting downstream consumers).
func TestAWSSignerCatchesBadSigLength(t *testing.T) {
	ctx := context.Background()
	const arn = "arn:aws:kms:us-east-1:123456789012:key/test"

	// A custom fake that returns a malformed signature (63 bytes).
	bad := &shortSigFake{keyARN: arn}
	if _, err := NewAWSSignerWithAPI(ctx, bad, arn); err != nil {
		t.Fatalf("setup with shortSigFake: %v", err)
	}

	signer, err := NewAWSSignerWithAPI(ctx, bad, arn)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err = signer.Sign(ctx, []byte("x"))
	if err == nil {
		t.Fatal("expected Sign to reject 63-byte signature; got nil")
	}
	if !strings.Contains(err.Error(), "unexpected signature length") {
		t.Errorf("error doesn't mention sig length: %v", err)
	}
}

// shortSigFake is a tiny fake that returns a 63-byte (deliberately wrong) sig,
// to prove our wrapper catches malformed KMS responses.
type shortSigFake struct {
	keyARN string
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
}

func (s *shortSigFake) GetPublicKey(ctx context.Context, in *awskms.GetPublicKeyInput, _ ...func(*awskms.Options)) (*awskms.GetPublicKeyOutput, error) {
	if s.priv == nil {
		var err error
		s.pub, s.priv, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
	}
	der, err := x509.MarshalPKIXPublicKey(s.pub)
	if err != nil {
		return nil, err
	}
	return &awskms.GetPublicKeyOutput{
		KeyId:             aws.String(s.keyARN),
		PublicKey:         der,
		KeySpec:           types.KeySpecEccNistEdwards25519,
		KeyUsage:          types.KeyUsageTypeSignVerify,
		SigningAlgorithms: []types.SigningAlgorithmSpec{types.SigningAlgorithmSpecEd25519Sha512},
	}, nil
}

func (s *shortSigFake) Sign(ctx context.Context, in *awskms.SignInput, _ ...func(*awskms.Options)) (*awskms.SignOutput, error) {
	return &awskms.SignOutput{
		KeyId:            aws.String(s.keyARN),
		Signature:        []byte("deliberately-short-not-64-bytes"), // 31 bytes
		SigningAlgorithm: types.SigningAlgorithmSpecEd25519Sha512,
	}, nil
}
