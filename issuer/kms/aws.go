package kms

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// KMSAPI is the narrow slice of the AWS KMS SDK that AWSSigner depends on.
// Both *awskms.Client (production) and any test fake satisfy this.
type KMSAPI interface {
	Sign(ctx context.Context, in *awskms.SignInput, opts ...func(*awskms.Options)) (*awskms.SignOutput, error)
	GetPublicKey(ctx context.Context, in *awskms.GetPublicKeyInput, opts ...func(*awskms.Options)) (*awskms.GetPublicKeyOutput, error)
}

// AWSSigner implements Signer using an AWS KMS asymmetric Ed25519 key.
//
// Key requirements:
//   - KeyUsage:    SIGN_VERIFY
//   - KeySpec:     ECC_NIST_EDWARDS25519
//   - IAM:         caller has kms:Sign + kms:GetPublicKey on the key ARN
//
// Signing surface:
//   - SigningAlgorithm: ED25519_SHA_512 (the only Ed25519 algorithm KMS exposes;
//     this is standard EdDSA — internal SHA-512 is part of Ed25519 itself).
//   - MessageType:      RAW (KMS handles hashing).
//   - Returned signature is raw 64 bytes (RFC 8032 format) — no DER reshaping
//     required, directly compatible with the rest of the verification path.
//
// Public-key fetch:
//   - GetPublicKey returns DER-encoded SubjectPublicKeyInfo (RFC 5280).
//   - We parse via x509.ParsePKIXPublicKey, assert ed25519.PublicKey, cache.
//   - KMS does not rotate the public key under a given ARN, so the cache
//     is safe for the process lifetime.
type AWSSigner struct {
	client    KMSAPI
	kmsKeyID  string
	cachedPub ed25519.PublicKey
	once      sync.Once
	initErr   error
}

// NewAWSSigner creates an AWSSigner using AWS SDK default credential resolution.
// Calls GetPublicKey immediately to fail fast on misconfiguration.
func NewAWSSigner(ctx context.Context, kmsKeyID, region string) (*AWSSigner, error) {
	if kmsKeyID == "" {
		return nil, fmt.Errorf("aws signer: kmsKeyID must not be empty")
	}
	if region == "" {
		return nil, fmt.Errorf("aws signer: region must not be empty")
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("aws signer: load AWS config: %w", err)
	}

	return NewAWSSignerWithAPI(ctx, awskms.NewFromConfig(cfg), kmsKeyID)
}

// NewAWSSignerWithAPI lets callers inject a KMSAPI implementation (for tests
// against a deep mock, or for callers using a custom credential chain).
func NewAWSSignerWithAPI(ctx context.Context, api KMSAPI, kmsKeyID string) (*AWSSigner, error) {
	if api == nil {
		return nil, fmt.Errorf("aws signer: api must not be nil")
	}
	if kmsKeyID == "" {
		return nil, fmt.Errorf("aws signer: kmsKeyID must not be empty")
	}
	s := &AWSSigner{client: api, kmsKeyID: kmsKeyID}
	if err := s.loadPublicKey(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// loadPublicKey calls KMS GetPublicKey, parses the DER SubjectPublicKeyInfo,
// asserts ed25519.PublicKey, and caches the 32-byte raw key.
func (a *AWSSigner) loadPublicKey(ctx context.Context) error {
	out, err := a.client.GetPublicKey(ctx, &awskms.GetPublicKeyInput{
		KeyId: aws.String(a.kmsKeyID),
	})
	if err != nil {
		return fmt.Errorf("aws signer: GetPublicKey: %w", err)
	}

	raw, err := x509.ParsePKIXPublicKey(out.PublicKey)
	if err != nil {
		return fmt.Errorf("aws signer: parse DER public key: %w", err)
	}

	pub, ok := raw.(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("aws signer: expected ed25519.PublicKey from KMS, got %T", raw)
	}

	a.cachedPub = pub
	return nil
}

// Sign calls KMS Sign with algorithm ED25519_SHA_512 and MessageType RAW.
// Returns the raw 64-byte Ed25519 signature.
func (a *AWSSigner) Sign(ctx context.Context, message []byte) ([]byte, error) {
	out, err := a.client.Sign(ctx, &awskms.SignInput{
		KeyId:            aws.String(a.kmsKeyID),
		Message:          message,
		MessageType:      types.MessageTypeRaw,
		SigningAlgorithm: types.SigningAlgorithmSpecEd25519Sha512,
	})
	if err != nil {
		return nil, fmt.Errorf("aws signer: KMS Sign: %w", err)
	}

	if len(out.Signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("aws signer: unexpected signature length %d (want %d)",
			len(out.Signature), ed25519.SignatureSize)
	}
	return out.Signature, nil
}

// PublicKey returns the cached 32-byte Ed25519 public key.
func (a *AWSSigner) PublicKey() ed25519.PublicKey { return a.cachedPub }

// KeyID returns the KMS key ARN (or alias) provided at construction.
func (a *AWSSigner) KeyID() string { return a.kmsKeyID }
