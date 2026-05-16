package kms

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// AWSKMSFake is a wire-faithful in-process emulator of the AWS KMS Ed25519
// signing surface. It is used to validate the AWSSigner code path WITHOUT
// requiring real AWS credentials.
//
// Faithfulness contract — this fake matches the real AWS KMS API in:
//   1. GetPublicKey returns DER-encoded SubjectPublicKeyInfo (RFC 5280),
//      identical encoding to real KMS (verified against AWS docs § 9.5).
//   2. Sign requires SigningAlgorithm == ED25519_SHA_512 and MessageType == RAW;
//      rejects any other combination with a descriptive error.
//   3. Sign returns a raw 64-byte Ed25519 signature (no DER wrapping) — real
//      KMS behaviour for ED25519_SHA_512.
//   4. KeyId mismatch returns an error mirroring NotFoundException.
//   5. The Message argument MUST be ≤ 4096 bytes (KMS RAW message limit).
//
// What this fake does NOT model: throttling, request signing, regional routing,
// audit logging, key-state lifecycle (Enabled / Disabled / PendingDeletion),
// grant-based access control. These are out of scope for the signing-correctness
// test the fake exists to support.
type AWSKMSFake struct {
	keyARN string
	priv   ed25519.PrivateKey
	pub    ed25519.PublicKey

	// invocation counters — useful for tests asserting that the wrapper
	// caches GetPublicKey and doesn't refetch on every Sign.
	GetPublicKeyCalls int
	SignCalls         int
}

// NewAWSKMSFake mints a fresh Ed25519 keypair and wraps it as a KMSAPI.
func NewAWSKMSFake(keyARN string) (*AWSKMSFake, error) {
	if keyARN == "" {
		return nil, fmt.Errorf("aws fake: keyARN must not be empty")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("aws fake: keygen: %w", err)
	}
	return &AWSKMSFake{keyARN: keyARN, priv: priv, pub: pub}, nil
}

// GetPublicKey emulates KMS's GetPublicKey API.
//
// The real API returns out.PublicKey as a DER-encoded SubjectPublicKeyInfo
// (RFC 5280 / PKIX). We replicate that exact wire encoding using the Go
// stdlib's x509.MarshalPKIXPublicKey, which produces byte-for-byte
// SubjectPublicKeyInfo identical to what KMS returns.
func (f *AWSKMSFake) GetPublicKey(
	ctx context.Context,
	in *awskms.GetPublicKeyInput,
	opts ...func(*awskms.Options),
) (*awskms.GetPublicKeyOutput, error) {
	f.GetPublicKeyCalls++
	if in == nil || in.KeyId == nil {
		return nil, fmt.Errorf("aws fake: GetPublicKey: KeyId required")
	}
	if *in.KeyId != f.keyARN {
		return nil, fmt.Errorf("aws fake: NotFoundException: %s", *in.KeyId)
	}

	der, err := x509.MarshalPKIXPublicKey(f.pub)
	if err != nil {
		return nil, fmt.Errorf("aws fake: marshal PKIX: %w", err)
	}

	return &awskms.GetPublicKeyOutput{
		KeyId:             aws.String(f.keyARN),
		PublicKey:         der,
		KeySpec:           types.KeySpecEccNistEdwards25519,
		KeyUsage:          types.KeyUsageTypeSignVerify,
		SigningAlgorithms: []types.SigningAlgorithmSpec{types.SigningAlgorithmSpecEd25519Sha512},
	}, nil
}

// Sign emulates KMS's Sign API for Ed25519.
//
// Validations match real KMS:
//   - KeyId must match the fake's ARN (NotFoundException-like otherwise).
//   - SigningAlgorithm must be ED25519_SHA_512 (real KMS rejects others on
//     an Ed25519 key with InvalidSigningAlgorithmException).
//   - MessageType must be RAW (KMS handles the hashing internally for Ed25519).
//   - Message length must be ≤ 4096 bytes (real KMS RAW limit).
//
// Returns raw 64-byte Ed25519 signature — same wire shape as real KMS.
func (f *AWSKMSFake) Sign(
	ctx context.Context,
	in *awskms.SignInput,
	opts ...func(*awskms.Options),
) (*awskms.SignOutput, error) {
	f.SignCalls++
	if in == nil || in.KeyId == nil {
		return nil, fmt.Errorf("aws fake: Sign: KeyId required")
	}
	if *in.KeyId != f.keyARN {
		return nil, fmt.Errorf("aws fake: NotFoundException: %s", *in.KeyId)
	}
	if in.SigningAlgorithm != types.SigningAlgorithmSpecEd25519Sha512 {
		return nil, fmt.Errorf(
			"aws fake: InvalidSigningAlgorithmException: got %s, want ED25519_SHA_512",
			in.SigningAlgorithm)
	}
	if in.MessageType != types.MessageTypeRaw {
		return nil, fmt.Errorf(
			"aws fake: ValidationException: MessageType must be RAW for ED25519_SHA_512, got %s",
			in.MessageType)
	}
	if len(in.Message) == 0 {
		return nil, fmt.Errorf("aws fake: ValidationException: Message must be non-empty")
	}
	if len(in.Message) > 4096 {
		return nil, fmt.Errorf(
			"aws fake: ValidationException: Message exceeds 4096 bytes (got %d)",
			len(in.Message))
	}

	sig := ed25519.Sign(f.priv, in.Message)

	return &awskms.SignOutput{
		KeyId:            aws.String(f.keyARN),
		Signature:        sig,
		SigningAlgorithm: types.SigningAlgorithmSpecEd25519Sha512,
	}, nil
}

// PublicKey exposes the underlying public key for round-trip assertions in tests.
func (f *AWSKMSFake) PublicKey() ed25519.PublicKey { return f.pub }
