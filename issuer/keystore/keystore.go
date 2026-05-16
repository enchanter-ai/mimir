// Package keystore manages the JWK Set published at GET /v1/keys.
//
// One issuer process always has exactly one *active* signing key (the one
// returned by kms.Signer). Past keys remain in the JWK set as `status:retired`
// so verifiers can validate envelopes signed before rotation.
//
// Historical-key source of truth:
//
//	The file pointed to by ISSUER_HISTORICAL_KEYS_FILE (default
//	./historical-keys.json) is a JSON array of JWKPublicKey entries the
//	operator manually appends to before rotating the active key.
//
// Rotation procedure (operator):
//   1. Generate a new key in KMS (or for dev, restart with a new ephemeral seed).
//   2. Before pointing traffic at the new key, append the OLD key as a
//      JWKPublicKey with `status:"retired"` to historical-keys.json.
//   3. Switch KMS_KEY_ARN (or restart with new ephemeral seed).
//   4. Verifiers fetching /v1/keys see both keys; old envelopes still verify.
//
// Revoked keys (status:"revoked") are kept in the set so verifiers can
// programmatically reject envelopes signed under them — turning a key
// compromise from "silently invalid" into "loudly rejected with a reason".
package keystore

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/enchanter-ai/mimir/issuer/types"
)

const (
	defaultHistoricalKeysFile = "historical-keys.json"
)

// Store is the in-process keystore. Safe for concurrent use.
type Store struct {
	mu sync.RWMutex

	// active is the currently-signing key. Always status:"active".
	active types.JWKPublicKey

	// historical is the keys retired or revoked under prior rotations.
	historical []types.JWKPublicKey
}

// New constructs a Store seeded from an active Signer's public key + key ID.
// It then loads historical keys from ISSUER_HISTORICAL_KEYS_FILE if present.
func New(activeKeyID string, activePub ed25519.PublicKey) (*Store, error) {
	s := &Store{
		active: types.JWKPublicKey{
			Kty:    "OKP",
			Crv:    "Ed25519",
			X:      base64.RawURLEncoding.EncodeToString(activePub),
			Kid:    activeKeyID,
			Use:    "sig",
			Alg:    "EdDSA",
			Status: "active",
		},
	}
	if err := s.loadHistorical(); err != nil {
		return nil, fmt.Errorf("keystore: load historical: %w", err)
	}
	return s, nil
}

// loadHistorical reads the historical-keys file. Missing file is not an error;
// new deploys start with no rotation history.
func (s *Store) loadHistorical() error {
	path := os.Getenv("ISSUER_HISTORICAL_KEYS_FILE")
	if path == "" {
		path = defaultHistoricalKeysFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var keys []types.JWKPublicKey
	if err := json.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	// Validate each entry. Skip anything that can't be sanely accepted.
	for i, k := range keys {
		if k.Kid == "" {
			return fmt.Errorf("historical key %d: kid required", i)
		}
		if k.Kid == s.active.Kid {
			return fmt.Errorf("historical key %d: kid %q collides with the active key", i, k.Kid)
		}
		if k.Status == "" {
			k.Status = "retired"
			keys[i] = k
		}
		if k.Status != "retired" && k.Status != "revoked" {
			return fmt.Errorf("historical key %d (%s): status must be 'retired' or 'revoked', got %q",
				i, k.Kid, k.Status)
		}
	}
	s.historical = keys
	return nil
}

// JWKSet returns the published JWK Set: [active, ...historical].
// Verifiers should match envelope.signature.protected_header.key_id against
// the `kid` of an entry in this list.
func (s *Store) JWKSet() types.JWKSet {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]types.JWKPublicKey, 0, 1+len(s.historical))
	out = append(out, s.active)
	out = append(out, s.historical...)
	return types.JWKSet{Keys: out}
}

// Active returns the currently-signing key as a JWK.
func (s *Store) Active() types.JWKPublicKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// Lookup finds a key by kid. Returns the JWK + whether the key is currently
// valid for signature verification. A revoked key returns (jwk, false) so
// callers can produce a "key was revoked" error rather than silently failing.
func (s *Store) Lookup(kid string) (types.JWKPublicKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.active.Kid == kid {
		return s.active, true
	}
	for _, k := range s.historical {
		if k.Kid == kid {
			return k, k.Status != "revoked"
		}
	}
	return types.JWKPublicKey{}, false
}

// HistoricalCount returns the number of non-active keys known to the store.
// Mostly for telemetry/tests.
func (s *Store) HistoricalCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.historical)
}
