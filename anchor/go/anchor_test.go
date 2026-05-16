// Anchor integration tests using go-ethereum's in-process simulated backend.
//
// These tests run WITHOUT any external tooling — no Foundry, no Anvil, no
// running node. The contract is compiled from .sol to bytecode via
// `node compile.js` in the parent directory (see compile.js), the bytecode is
// embedded into anchor.go, and deployment + RPC happens against a pure-Go EVM.
//
// To regenerate bytecode after editing the .sol:
//   cd anchor && node compile.js
//
// Run tests:
//   cd anchor/go && go test -v ./...
package anchor_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"

	anchor "github.com/enchanter-ai/mimir/anchor"
)

// genesisBalance is the funded balance for the test account at chain genesis (1000 ETH in wei).
var genesisBalance, _ = new(big.Int).SetString("1000000000000000000000", 10)

// testSetup spins up an in-process simulated backend with one funded account,
// deploys MimirValidationRegistry from the embedded bytecode, and returns a
// configured anchor.Client.
//
// Cleanup of the backend is registered via t.Cleanup.
func testSetup(t *testing.T) (*anchor.Client, *simulated.Backend, common.Address, *ecdsa.PrivateKey) {
	t.Helper()

	// 1. Generate a key.
	privKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	from := crypto.PubkeyToAddress(privKey.PublicKey)

	// 2. Spin up the simulated backend with that account funded.
	alloc := types.GenesisAlloc{from: {Balance: genesisBalance}}
	backend := simulated.NewBackend(alloc, simulated.WithBlockGasLimit(30_000_000))
	t.Cleanup(func() { _ = backend.Close() })

	ec := backend.Client()
	ctx := context.Background()

	// 3. Deploy the contract.
	chainID, err := ec.ChainID(ctx)
	if err != nil {
		t.Fatalf("chainID: %v", err)
	}
	_ = from // (used below via privKey → from inside deployRegistry)

	// Deploy in permissionless mode: zero addresses, default slashWad.
	contractAddr := deployRegistry(t, ctx, backend, ec, privKey, common.Address{}, common.Address{}, big.NewInt(0))
	t.Logf("contract deployed at %s", contractAddr.Hex())

	// 4. Build the anchor.Client using NewWithClient (adapter for simulated client).
	privHex := hex.EncodeToString(crypto.FromECDSA(privKey))
	c, err := anchor.NewWithClient(ec, privHex, contractAddr, chainID)
	if err != nil {
		t.Fatalf("anchor.NewWithClient: %v", err)
	}

	return c, backend, contractAddr, privKey
}

func randomDigest(t *testing.T) [32]byte {
	t.Helper()
	var d [32]byte
	if _, err := rand.Read(d[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return d
}

// commitAndWait commits a block and waits up to 5s for the tx receipt.
func commitAndWait(t *testing.T, backend *simulated.Backend, c *anchor.Client, txHash common.Hash) *types.Receipt {
	t.Helper()
	backend.Commit()
	r, err := c.WaitMined(context.Background(), txHash, 5*time.Second)
	if err != nil {
		t.Fatalf("WaitMined: %v", err)
	}
	if r.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("tx %s reverted (status=%d)", txHash.Hex(), r.Status)
	}
	return r
}

// ------------------------------------------------------------------
// Test 1: register + verify round-trip
// ------------------------------------------------------------------

func TestAnchorRegisterVerifyRoundtrip(t *testing.T) {
	c, backend, _, _ := testSetup(t)
	ctx := context.Background()

	digest := randomDigest(t)
	expiry := uint64(time.Now().Add(24 * time.Hour).Unix())

	txHash, err := c.AnchorEnvelope(ctx, digest, expiry)
	if err != nil {
		t.Fatalf("AnchorEnvelope: %v", err)
	}
	commitAndWait(t, backend, c, txHash)

	res, err := c.VerifyAnchor(ctx, digest)
	if err != nil {
		t.Fatalf("VerifyAnchor: %v", err)
	}
	if res.Issuer == (common.Address{}) {
		t.Fatal("issuer is zero address — registration didn't take")
	}
	if res.Expiry != expiry {
		t.Errorf("expiry: got %d, want %d", res.Expiry, expiry)
	}
	if res.Revoked {
		t.Error("revoked=true on fresh entry")
	}

	valid, err := c.IsValid(ctx, digest)
	if err != nil {
		t.Fatalf("IsValid: %v", err)
	}
	if !valid {
		t.Error("IsValid returned false on fresh non-expired entry")
	}
}

// ------------------------------------------------------------------
// Test 2: duplicate registration reverts
// ------------------------------------------------------------------

func TestAnchorDuplicateReverts(t *testing.T) {
	c, backend, _, _ := testSetup(t)
	ctx := context.Background()

	digest := randomDigest(t)
	txHash, err := c.AnchorEnvelope(ctx, digest, 0)
	if err != nil {
		t.Fatalf("first AnchorEnvelope: %v", err)
	}
	commitAndWait(t, backend, c, txHash)

	// Second registration of same digest must revert. With our sendTx path,
	// the gas-estimate step performs an eth_call which will surface the revert
	// as an error BEFORE the tx is even sent.
	_, err = c.AnchorEnvelope(ctx, digest, 0)
	if err == nil {
		t.Fatal("expected duplicate registration to revert; got nil error")
	}
	t.Logf("got expected revert error: %v", err)
}

// ------------------------------------------------------------------
// Test 3: revoke flips revoked flag
// ------------------------------------------------------------------

func TestAnchorRevoke(t *testing.T) {
	c, backend, _, _ := testSetup(t)
	ctx := context.Background()

	digest := randomDigest(t)
	expiry := uint64(time.Now().Add(24 * time.Hour).Unix())

	txHash, err := c.AnchorEnvelope(ctx, digest, expiry)
	if err != nil {
		t.Fatalf("AnchorEnvelope: %v", err)
	}
	commitAndWait(t, backend, c, txHash)

	// Submit a revocation proof — any blob is accepted in this slice.
	proof := []byte("synthetic-fraud-proof-blob-for-test")
	revokeTx, err := c.RevokeAnchor(ctx, digest, proof)
	if err != nil {
		t.Fatalf("RevokeAnchor: %v", err)
	}
	commitAndWait(t, backend, c, revokeTx)

	res, err := c.VerifyAnchor(ctx, digest)
	if err != nil {
		t.Fatalf("VerifyAnchor: %v", err)
	}
	if !res.Revoked {
		t.Error("revoked flag did not flip to true")
	}

	valid, err := c.IsValid(ctx, digest)
	if err != nil {
		t.Fatalf("IsValid: %v", err)
	}
	if valid {
		t.Error("IsValid returned true after revocation")
	}
}

// ------------------------------------------------------------------
// Test 4: expiry causes isValid to return false
// ------------------------------------------------------------------

func TestAnchorExpiry(t *testing.T) {
	c, backend, _, _ := testSetup(t)
	ctx := context.Background()

	digest := randomDigest(t)
	// expiry 60s ago — already expired
	expiry := uint64(time.Now().Add(-60 * time.Second).Unix())

	txHash, err := c.AnchorEnvelope(ctx, digest, expiry)
	if err != nil {
		t.Fatalf("AnchorEnvelope: %v", err)
	}
	commitAndWait(t, backend, c, txHash)

	valid, err := c.IsValid(ctx, digest)
	if err != nil {
		t.Fatalf("IsValid: %v", err)
	}
	if valid {
		t.Error("IsValid returned true on expired entry")
	}

	// VerifyAnchor still returns the entry (it doesn't filter by expiry — that's IsValid's job).
	res, err := c.VerifyAnchor(ctx, digest)
	if err != nil {
		t.Fatalf("VerifyAnchor: %v", err)
	}
	if res.Issuer == (common.Address{}) {
		t.Error("VerifyAnchor lost the entry on expiry — should still return it, just IsValid filters")
	}
}

// ------------------------------------------------------------------
// Test 5: unknown digest verifies as zero-address
// ------------------------------------------------------------------

func TestAnchorUnknownDigest(t *testing.T) {
	c, _, _, _ := testSetup(t)
	ctx := context.Background()

	digest := randomDigest(t)
	res, err := c.VerifyAnchor(ctx, digest)
	if err != nil {
		t.Fatalf("VerifyAnchor: %v", err)
	}
	if res.Issuer != (common.Address{}) {
		t.Errorf("unknown digest issuer should be zero address, got %s", res.Issuer.Hex())
	}
	if res.Revoked {
		t.Error("unknown digest revoked should be false")
	}

	valid, err := c.IsValid(ctx, digest)
	if err != nil {
		t.Fatalf("IsValid: %v", err)
	}
	if valid {
		t.Error("IsValid returned true on unknown digest")
	}
}

// ------------------------------------------------------------------
// Test 6: re-revoke reverts
// ------------------------------------------------------------------

func TestAnchorReRevokeReverts(t *testing.T) {
	c, backend, _, _ := testSetup(t)
	ctx := context.Background()

	digest := randomDigest(t)
	txHash, err := c.AnchorEnvelope(ctx, digest, 0)
	if err != nil {
		t.Fatalf("AnchorEnvelope: %v", err)
	}
	commitAndWait(t, backend, c, txHash)

	revokeTx, err := c.RevokeAnchor(ctx, digest, []byte("first-proof"))
	if err != nil {
		t.Fatalf("first RevokeAnchor: %v", err)
	}
	commitAndWait(t, backend, c, revokeTx)

	// Second revoke should revert (AlreadyRevoked error in contract).
	_, err = c.RevokeAnchor(ctx, digest, []byte("second-proof"))
	if err == nil {
		t.Fatal("expected second revocation to revert")
	}
	t.Logf("got expected revert: %v", err)
}

// ------------------------------------------------------------------
// Test 7: full envelope-digest lifecycle (the realistic AVS flow)
// ------------------------------------------------------------------

func TestAnchorEnvelopeLifecycle(t *testing.T) {
	c, backend, _, _ := testSetup(t)
	ctx := context.Background()

	// Simulate 5 different envelopes being anchored over time.
	digests := make([][32]byte, 5)
	for i := range digests {
		digests[i] = randomDigest(t)
		expiry := uint64(time.Now().Add(time.Duration(i+1) * time.Hour).Unix())
		tx, err := c.AnchorEnvelope(ctx, digests[i], expiry)
		if err != nil {
			t.Fatalf("anchor envelope %d: %v", i, err)
		}
		commitAndWait(t, backend, c, tx)
	}

	// Verify all 5 read back correctly.
	for i, d := range digests {
		valid, err := c.IsValid(ctx, d)
		if err != nil {
			t.Fatalf("IsValid digest %d: %v", i, err)
		}
		if !valid {
			t.Errorf("digest %d not valid after anchoring", i)
		}
	}

	// Revoke #2 via a fraud-proof blob; remaining 4 stay valid.
	rTx, err := c.RevokeAnchor(ctx, digests[2], []byte("replay-mismatch-proof"))
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	commitAndWait(t, backend, c, rTx)

	for i, d := range digests {
		v, err := c.IsValid(ctx, d)
		if err != nil {
			t.Fatalf("IsValid post-revoke digest %d: %v", i, err)
		}
		if i == 2 && v {
			t.Errorf("digest %d should be invalid post-revoke", i)
		}
		if i != 2 && !v {
			t.Errorf("digest %d unexpectedly invalid (not the one we revoked)", i)
		}
	}
}
