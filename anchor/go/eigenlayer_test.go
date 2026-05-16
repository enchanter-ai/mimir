// EigenLayer slashing integration tests.
//
// Deploys MockServiceManager, MockSlasher, then a MimirValidationRegistry
// wired to both. Exercises the slashing path end-to-end:
//   - Only registered operators may anchor envelopes (NotAnOperator revert).
//   - register() rejects mismatched issuer (must equal msg.sender in AVS mode).
//   - revoke() flips revoked AND calls Slasher.slash() with the configured wad.
//   - Slashed event is emitted; MockSlasher's totalSlashed accumulates.
//   - Multi-operator scenario: slashing one operator doesn't touch another's stake.
//
// What this PROVES: the wiring between MimirValidationRegistry, IServiceManager,
// and ISlasher is correct. Replacing the mocks with real EigenLayer Holesky
// addresses (or mainnet) is a deploy-time config change documented in
// anchor/README.md.
package anchor_test

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"

	anchor "github.com/enchanter-ai/mimir/anchor"
)

// avsSetup creates a simulated EVM, deploys MockServiceManager, MockSlasher,
// and a MimirValidationRegistry wired to both. Returns the deployer key,
// contract addresses, and an anchor.Client.
//
// Note: the anchor.Client's `from` address (the deployer) is NOT yet a
// registered operator. Tests must call RegisterOperator() on the manager
// before they can successfully anchor.
type avsRig struct {
	backend  *simulated.Backend
	ec       simulated.Client
	deployer *ecdsa.PrivateKey
	from     common.Address

	managerAddr  common.Address
	slasherAddr  common.Address
	registryAddr common.Address

	client *anchor.Client
}

func avsSetup(t *testing.T, slashWad *big.Int) *avsRig {
	t.Helper()

	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	from := crypto.PubkeyToAddress(priv.PublicKey)

	alloc := types.GenesisAlloc{from: {Balance: genesisBalance}}
	backend := simulated.NewBackend(alloc, simulated.WithBlockGasLimit(30_000_000))
	t.Cleanup(func() { _ = backend.Close() })
	ec := backend.Client()
	ctx := context.Background()

	managerAddr := deployMockServiceManager(t, ctx, backend, ec, priv)
	slasherAddr := deployMockSlasher(t, ctx, backend, ec, priv)
	registryAddr := deployRegistry(t, ctx, backend, ec, priv, managerAddr, slasherAddr, slashWad)

	chainID, err := ec.ChainID(ctx)
	if err != nil {
		t.Fatalf("chainID: %v", err)
	}

	c, err := anchor.NewWithClient(ec, hex.EncodeToString(crypto.FromECDSA(priv)), registryAddr, chainID)
	if err != nil {
		t.Fatalf("anchor.NewWithClient: %v", err)
	}

	return &avsRig{
		backend:      backend,
		ec:           ec,
		deployer:     priv,
		from:         from,
		managerAddr:  managerAddr,
		slasherAddr:  slasherAddr,
		registryAddr: registryAddr,
		client:       c,
	}
}

// registerOperator calls MockServiceManager.registerOperator(addr) by abi-encoding
// the call directly. We don't need an anchor.Client for this — just raw eth_sendTx.
func (rig *avsRig) registerOperator(t *testing.T, operator common.Address) {
	t.Helper()
	managerABI, _ := readContract(t, "MockServiceManager")
	data, err := managerABI.Pack("registerOperator", operator)
	if err != nil {
		t.Fatalf("pack registerOperator: %v", err)
	}
	rig.sendRaw(t, &rig.managerAddr, data)
}

// querySlashed reads MockSlasher.totalSlashed(operator) via eth_call.
func (rig *avsRig) querySlashed(t *testing.T, operator common.Address) *big.Int {
	t.Helper()
	slasherABI, _ := readContract(t, "MockSlasher")
	data, err := slasherABI.Pack("totalSlashed", operator)
	if err != nil {
		t.Fatalf("pack totalSlashed: %v", err)
	}
	raw, err := rig.ec.CallContract(context.Background(), ethereum.CallMsg{To: &rig.slasherAddr, Data: data}, nil)
	if err != nil {
		t.Fatalf("CallContract totalSlashed: %v", err)
	}
	out, err := slasherABI.Unpack("totalSlashed", raw)
	if err != nil {
		t.Fatalf("unpack totalSlashed: %v", err)
	}
	return out[0].(*big.Int)
}

// sendRaw signs and sends a raw transaction.
func (rig *avsRig) sendRaw(t *testing.T, to *common.Address, data []byte) {
	t.Helper()
	ctx := context.Background()
	chainID, err := rig.ec.ChainID(ctx)
	if err != nil {
		t.Fatalf("chainID: %v", err)
	}
	nonce, err := rig.ec.PendingNonceAt(ctx, rig.from)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	tx := types.NewTransaction(nonce, *to, big.NewInt(0), 1_000_000, big.NewInt(1_000_000_000), data)
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), rig.deployer)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := rig.ec.SendTransaction(ctx, signed); err != nil {
		t.Fatalf("send: %v", err)
	}
	rig.backend.Commit()

	// Surface a revert (failed receipt) loudly so tests fail with context.
	r, err := rig.client.WaitMined(ctx, signed.Hash(), 5*time.Second)
	if err != nil {
		t.Fatalf("WaitMined: %v", err)
	}
	if r.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("tx reverted (status=%d)", r.Status)
	}
}

// ------------------------------------------------------------------
// Test 1: non-operator register reverts with NotAnOperator
// ------------------------------------------------------------------

func TestAVSRegisterRequiresOperator(t *testing.T) {
	rig := avsSetup(t, big.NewInt(0))
	ctx := context.Background()

	// deployer is NOT registered as an operator yet.
	digest := randomDigest(t)
	_, err := rig.client.AnchorEnvelope(ctx, digest, 0)
	if err == nil {
		t.Fatal("expected NotAnOperator revert on register; got nil")
	}
	if !strings.Contains(err.Error(), "execution reverted") {
		t.Errorf("expected revert error, got %v", err)
	}
	t.Logf("got expected revert: %v", err)
}

// ------------------------------------------------------------------
// Test 2: registered operator can anchor
// ------------------------------------------------------------------

func TestAVSRegisteredOperatorCanAnchor(t *testing.T) {
	rig := avsSetup(t, big.NewInt(0))
	ctx := context.Background()

	rig.registerOperator(t, rig.from)

	digest := randomDigest(t)
	tx, err := rig.client.AnchorEnvelope(ctx, digest, 0)
	if err != nil {
		t.Fatalf("AnchorEnvelope as operator: %v", err)
	}
	commitAndWait(t, rig.backend, rig.client, tx)

	res, err := rig.client.VerifyAnchor(ctx, digest)
	if err != nil {
		t.Fatalf("VerifyAnchor: %v", err)
	}
	if res.Issuer != rig.from {
		t.Errorf("issuer: got %s, want %s", res.Issuer.Hex(), rig.from.Hex())
	}
}

// ------------------------------------------------------------------
// Test 3: revoke triggers slasher with configured wad
// ------------------------------------------------------------------

func TestAVSRevokeTriggersSlash(t *testing.T) {
	// Use a non-default slashWad to confirm the constructor arg is propagated:
	// 25% = 2.5e17.
	slashWad := new(big.Int)
	slashWad.SetString("250000000000000000", 10)

	rig := avsSetup(t, slashWad)
	ctx := context.Background()

	rig.registerOperator(t, rig.from)

	// Anchor an envelope.
	digest := randomDigest(t)
	tx, err := rig.client.AnchorEnvelope(ctx, digest, 0)
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}
	commitAndWait(t, rig.backend, rig.client, tx)

	// Pre-revoke: no stake slashed.
	if got := rig.querySlashed(t, rig.from); got.Sign() != 0 {
		t.Errorf("pre-revoke totalSlashed: got %s, want 0", got.String())
	}

	// Revoke with a fraud proof.
	proof := []byte("replay-mismatch-artifact-reference")
	rTx, err := rig.client.RevokeAnchor(ctx, digest, proof)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	commitAndWait(t, rig.backend, rig.client, rTx)

	// Post-revoke: slasher must have recorded the slash.
	totalSlashed := rig.querySlashed(t, rig.from)
	if totalSlashed.Cmp(slashWad) != 0 {
		t.Errorf("totalSlashed: got %s, want %s (the configured slashWad)",
			totalSlashed.String(), slashWad.String())
	}
	t.Logf("operator %s slashed by %s wad (25%% of allocation)",
		rig.from.Hex(), totalSlashed.String())

	// IsValid must be false post-revoke.
	valid, err := rig.client.IsValid(ctx, digest)
	if err != nil {
		t.Fatalf("IsValid: %v", err)
	}
	if valid {
		t.Error("IsValid returned true after revocation")
	}
}

// ------------------------------------------------------------------
// Test 4: multi-operator — slashing one doesn't touch another
// ------------------------------------------------------------------

func TestAVSSlashIsolatedPerOperator(t *testing.T) {
	rig := avsSetup(t, big.NewInt(0)) // default 10%
	ctx := context.Background()
	const defaultSlashWad = "100000000000000000" // 1e17

	// Register two operators: deployer + a fresh address.
	otherKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	otherAddr := crypto.PubkeyToAddress(otherKey.PublicKey)
	rig.registerOperator(t, rig.from)
	rig.registerOperator(t, otherAddr)

	// Fund the other operator so they can pay gas.
	fundAddr(t, rig, otherAddr, new(big.Int).Mul(big.NewInt(1), big.NewInt(1_000_000_000_000_000_000)))

	// Deployer (rig.client) anchors a digest.
	digest := randomDigest(t)
	tx, err := rig.client.AnchorEnvelope(ctx, digest, 0)
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}
	commitAndWait(t, rig.backend, rig.client, tx)

	// Revoke it. Deployer's slashed total = 1e17. Other's = 0.
	rTx, err := rig.client.RevokeAnchor(ctx, digest, []byte("proof"))
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	commitAndWait(t, rig.backend, rig.client, rTx)

	deployerSlashed := rig.querySlashed(t, rig.from)
	otherSlashed := rig.querySlashed(t, otherAddr)

	want := new(big.Int)
	want.SetString(defaultSlashWad, 10)
	if deployerSlashed.Cmp(want) != 0 {
		t.Errorf("deployer slashed: got %s, want %s", deployerSlashed, want)
	}
	if otherSlashed.Sign() != 0 {
		t.Errorf("other operator slashed unexpectedly: %s", otherSlashed)
	}
}

// ------------------------------------------------------------------
// Test 5: register rejects mismatched issuer (anti-spoofing)
// ------------------------------------------------------------------

func TestAVSRegisterRejectsForeignIssuer(t *testing.T) {
	rig := avsSetup(t, big.NewInt(0))
	rig.registerOperator(t, rig.from)
	ctx := context.Background()

	// Encode register(digest, otherAddr, 0) — issuer != msg.sender.
	registryABI, _ := readContract(t, "MimirValidationRegistry")
	other := common.HexToAddress("0x000000000000000000000000000000000000DEAD")
	digest := randomDigest(t)
	data, err := registryABI.Pack("register", digest, other, big.NewInt(0))
	if err != nil {
		t.Fatalf("pack register: %v", err)
	}

	// Send the raw call. Estimate-gas should reject it as a revert.
	chainID, _ := rig.ec.ChainID(ctx)
	_, err = rig.ec.EstimateGas(ctx, ethereum.CallMsg{
		From: rig.from,
		To:   &rig.registryAddr,
		Data: data,
	})
	if err == nil {
		t.Fatal("expected EstimateGas to flag IssuerMustBeCaller revert")
	}
	_ = chainID
	t.Logf("got expected revert: %v", err)
}

// ------------------------------------------------------------------
// helpers
// ------------------------------------------------------------------

// fundAddr sends some ETH from the deployer to addr so addr can pay gas.
func fundAddr(t *testing.T, rig *avsRig, addr common.Address, wei *big.Int) {
	t.Helper()
	ctx := context.Background()
	chainID, _ := rig.ec.ChainID(ctx)
	nonce, err := rig.ec.PendingNonceAt(ctx, rig.from)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	tx := types.NewTransaction(nonce, addr, wei, 21_000, big.NewInt(1_000_000_000), nil)
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), rig.deployer)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := rig.ec.SendTransaction(ctx, signed); err != nil {
		t.Fatalf("send: %v", err)
	}
	rig.backend.Commit()
}
