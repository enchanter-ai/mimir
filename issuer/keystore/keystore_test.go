package keystore

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/enchanter-ai/mimir/issuer/types"
)

func mintJWK(t *testing.T, kid, status string) types.JWKPublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	return types.JWKPublicKey{
		Kty:    "OKP",
		Crv:    "Ed25519",
		X:      string(pub[:1]) + "stub", // not used by Lookup; only Kid matters
		Kid:    kid,
		Use:    "sig",
		Alg:    "EdDSA",
		Status: status,
	}
}

func TestNewWithNoHistoricalFile(t *testing.T) {
	t.Setenv("ISSUER_HISTORICAL_KEYS_FILE", filepath.Join(t.TempDir(), "does-not-exist.json"))
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	s, err := New("kid-active", pub)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.HistoricalCount() != 0 {
		t.Errorf("expected 0 historical, got %d", s.HistoricalCount())
	}
	set := s.JWKSet()
	if len(set.Keys) != 1 {
		t.Errorf("JWKSet should have 1 key when no history, got %d", len(set.Keys))
	}
	if set.Keys[0].Status != "active" || set.Keys[0].Kid != "kid-active" {
		t.Errorf("active key wrong: %+v", set.Keys[0])
	}
}

func TestNewLoadsHistorical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "historical-keys.json")
	historical := []types.JWKPublicKey{
		mintJWK(t, "kid-old-1", "retired"),
		mintJWK(t, "kid-old-2", "revoked"),
		mintJWK(t, "kid-old-3", ""), // status omitted -> defaults to retired
	}
	bytes, _ := json.Marshal(historical)
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		t.Fatalf("write historical file: %v", err)
	}

	t.Setenv("ISSUER_HISTORICAL_KEYS_FILE", path)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	s, err := New("kid-new", pub)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.HistoricalCount() != 3 {
		t.Fatalf("expected 3 historical, got %d", s.HistoricalCount())
	}

	set := s.JWKSet()
	if len(set.Keys) != 4 {
		t.Errorf("JWKSet should be active + 3 historical = 4, got %d", len(set.Keys))
	}
	if set.Keys[0].Kid != "kid-new" || set.Keys[0].Status != "active" {
		t.Errorf("first key in set must be active, got %+v", set.Keys[0])
	}

	// kid-old-3's blank status must have defaulted to "retired".
	for _, k := range set.Keys[1:] {
		if k.Kid == "kid-old-3" && k.Status != "retired" {
			t.Errorf("kid-old-3 status: expected 'retired', got %q", k.Status)
		}
	}
}

func TestLookupActive(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("ISSUER_HISTORICAL_KEYS_FILE", filepath.Join(t.TempDir(), "none.json"))
	s, _ := New("kid-active", pub)

	jwk, valid := s.Lookup("kid-active")
	if !valid {
		t.Error("active key should be valid")
	}
	if jwk.Kid != "kid-active" {
		t.Errorf("Kid mismatch: %q", jwk.Kid)
	}

	_, valid = s.Lookup("kid-unknown")
	if valid {
		t.Error("unknown kid should not be valid")
	}
}

func TestLookupRevokedReturnsInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.json")
	historical := []types.JWKPublicKey{
		mintJWK(t, "kid-retired", "retired"),
		mintJWK(t, "kid-revoked", "revoked"),
	}
	bytes, _ := json.Marshal(historical)
	_ = os.WriteFile(path, bytes, 0o600)
	t.Setenv("ISSUER_HISTORICAL_KEYS_FILE", path)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	s, _ := New("kid-active", pub)

	if _, valid := s.Lookup("kid-retired"); !valid {
		t.Error("retired key: Lookup should report valid (envelopes from before retirement remain verifiable)")
	}
	if _, valid := s.Lookup("kid-revoked"); valid {
		t.Error("revoked key: Lookup must report INVALID (envelopes signed under revoked keys are tainted)")
	}
}

func TestRejectsKidCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.json")
	// Historical entry with the SAME kid as the active key — must error.
	historical := []types.JWKPublicKey{mintJWK(t, "kid-active", "retired")}
	bytes, _ := json.Marshal(historical)
	_ = os.WriteFile(path, bytes, 0o600)
	t.Setenv("ISSUER_HISTORICAL_KEYS_FILE", path)

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := New("kid-active", pub); err == nil {
		t.Fatal("expected error when historical kid collides with active kid")
	}
}

func TestRejectsBadStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.json")
	historical := []types.JWKPublicKey{mintJWK(t, "kid-bad", "active")} // active not allowed in history
	bytes, _ := json.Marshal(historical)
	_ = os.WriteFile(path, bytes, 0o600)
	t.Setenv("ISSUER_HISTORICAL_KEYS_FILE", path)

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := New("kid-active", pub); err == nil {
		t.Fatal("expected error when historical status is not 'retired' or 'revoked'")
	}
}
