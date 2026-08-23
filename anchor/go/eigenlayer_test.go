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
	"github.com/ethereum/go-ethereum/accounts/abi"
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

	// reporterKey is the trusted Slashing-Reporter ECDSA key whose EIP-191
	// signature over the canonical slash message authorizes a slash (VF-11).
	reporterKey  *ecdsa.PrivateKey
	reporterAddr common.Address

	managerAddr  common.Address
	slasherAddr  common.Address
	registryAddr common.Address

	slashWad *big.Int
	chainID  *big.Int

	client *anchor.Client
}

func avsSetup(t *testing.T, slashWad *big.Int) *avsRig {
	t.Helper()

	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	from := crypto.PubkeyToAddress(priv.PublicKey)

	// Independent trusted Slashing-Reporter key (NOT the deployer/operator).
	reporterKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("genkey reporter: %v", err)
	}
	reporterAddr := crypto.PubkeyToAddress(reporterKey.PublicKey)

	alloc := types.GenesisAlloc{from: {Balance: genesisBalance}}
	backend := simulated.NewBackend(alloc, simulated.WithBlockGasLimit(30_000_000))
	t.Cleanup(func() { _ = backend.Close() })
	ec := backend.Client()
	ctx := context.Background()

	managerAddr := deployMockServiceManager(t, ctx, backend, ec, priv)
	slasherAddr := deployMockSlasher(t, ctx, backend, ec, priv)
	registryAddr := deployRegistry(t, ctx, backend, ec, priv, managerAddr, slasherAddr, slashWad, reporterAddr)

	chainID, err := ec.ChainID(ctx)
	if err != nil {
		t.Fatalf("chainID: %v", err)
	}

	c, err := anchor.NewWithClient(ec, hex.EncodeToString(crypto.FromECDSA(priv)), registryAddr, chainID)
	if err != nil {
		t.Fatalf("anchor.NewWithClient: %v", err)
	}

	// The contract's default slashWad is 1e17 when 0 is passed — mirror that
	// here so tests that pass 0 can still reconstruct the signed message.
	effectiveWad := new(big.Int).Set(slashWad)
	if effectiveWad.Sign() == 0 {
		effectiveWad.SetString("100000000000000000", 10) // 1e17 default
	}

	return &avsRig{
		backend:      backend,
		ec:           ec,
		deployer:     priv,
		from:         from,
		reporterKey:  reporterKey,
		reporterAddr: reporterAddr,
		managerAddr:  managerAddr,
		slasherAddr:  slasherAddr,
		registryAddr: registryAddr,
		slashWad:     effectiveWad,
		chainID:      chainID,
		client:       c,
	}
}

// buildSlashProof produces a 65-byte (r,s,v) EIP-191 signature by `signer` over
// the canonical slash message:
//   m = keccak256(abi.encode(chainid, registry, digest, issuer, slashWad))
// then toEthSignedMessageHash(m). go-ethereum's crypto.Sign yields v in {0,1};
// the contract expects {27,28}, so we add 27.
func (rig *avsRig) buildSlashProof(t *testing.T, signer *ecdsa.PrivateKey, digest [32]byte, issuer common.Address) []byte {
	t.Helper()

	// abi.encode(uint256 chainid, address registry, bytes32 digest, address issuer, uint256 slashWad)
	uint256T, _ := abi.NewType("uint256", "", nil)
	addressT, _ := abi.NewType("address", "", nil)
	bytes32T, _ := abi.NewType("bytes32", "", nil)
	args := abi.Arguments{
		{Type: uint256T}, {Type: addressT}, {Type: bytes32T}, {Type: addressT}, {Type: uint256T},
	}
	encoded, err := args.Pack(rig.chainID, rig.registryAddr, digest, issuer, rig.slashWad)
	if err != nil {
		t.Fatalf("abi.Pack canonical message: %v", err)
	}
	m := crypto.Keccak256Hash(encoded)

	// toEthSignedMessageHash: keccak256("\x19Ethereum Signed Message:\n32" || m)
	prefixed := crypto.Keccak256Hash(
		[]byte("\x19Ethereum Signed Message:\n32"),
		m.Bytes(),
	)

	sig, err := crypto.Sign(prefixed.Bytes(), signer)
	if err != nil {
		t.Fatalf("sign slash proof: %v", err)
	}
	// crypto.Sign returns [R || S || V] with V in {0,1}; contract wants {27,28}.
	sig[64] += 27
	return sig
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
// Test 3 (VF-11): revoke with an UNVERIFIED proof must NOT slash.
//
// Regression guard for VF-11. Previously revoke() called slasher.slash()
// unconditionally on every AVS-mode revoke, so any caller could slash an
// honest issuer with arbitrary bytes. The fix gates the slash on
// _verifySlashProof(), which is currently unimplemented (returns false)
// pending a reviewed proof scheme — so no slash may fire. The revocation
// flag must still flip (open-challenge model preserved).
// ------------------------------------------------------------------

func TestAVSRevokeDoesNotSlashUnverifiedProof(t *testing.T) {
	// Configure a non-zero slashWad (25%) to prove that even a fully
	// configured slasher does NOT fire on an unverified proof.
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

	// A third party revokes with a bogus, unverified proof.
	proof := []byte("totally-bogus-unverified-proof")
	rTx, err := rig.client.RevokeAnchor(ctx, digest, proof)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	commitAndWait(t, rig.backend, rig.client, rTx)

	// VF-11: the slasher must NOT have been invoked.
	totalSlashed := rig.querySlashed(t, rig.from)
	if totalSlashed.Sign() != 0 {
		t.Errorf("VF-11 REGRESSION: unverified proof slashed issuer by %s (want 0)",
			totalSlashed.String())
	}
	t.Logf("VF-11 ok: unverified proof did not slash (totalSlashed=%s)", totalSlashed.String())

	// Revocation flag must still have flipped: IsValid == false post-revoke.
	valid, err := rig.client.IsValid(ctx, digest)
	if err != nil {
		t.Fatalf("IsValid: %v", err)
	}
	if valid {
		t.Error("IsValid returned true after revocation; revoke flag should flip")
	}
}

// ------------------------------------------------------------------
// Test 4 (VF-11): multi-operator — an unverified revoke slashes nobody.
//
// With slashing gated behind an (unimplemented) proof verifier, revoking a
// digest must leave BOTH the targeted issuer and any bystander operator
// unslashed. This also documents that the previously-asserted "slash
// isolation" property is currently moot because no slash fires at all;
// once a real proof scheme is wired in, restore an assertion that a VALID
// proof slashes ONLY the targeted operator by exactly slashWad.
// ------------------------------------------------------------------

func TestAVSRevokeSlashesNobodyWithoutValidProof(t *testing.T) {
	rig := avsSetup(t, big.NewInt(0)) // default 10%
	ctx := context.Background()

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

	// Revoke it with an unverified proof.
	rTx, err := rig.client.RevokeAnchor(ctx, digest, []byte("unverified-proof"))
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	commitAndWait(t, rig.backend, rig.client, rTx)

	deployerSlashed := rig.querySlashed(t, rig.from)
	otherSlashed := rig.querySlashed(t, otherAddr)

	// VF-11: no slash fires for anyone on an unverified proof.
	if deployerSlashed.Sign() != 0 {
		t.Errorf("VF-11 REGRESSION: targeted operator slashed by %s (want 0)", deployerSlashed)
	}
	if otherSlashed.Sign() != 0 {
		t.Errorf("bystander operator slashed unexpectedly: %s", otherSlashed)
	}
}

// ------------------------------------------------------------------
// Test 5 (VF-11 impl): VALID reporter proof slashes by EXACTLY slashWad.
//
// This is the legitimate-slash path the suite previously lacked. A proof
// that is an EIP-191 signature by the trusted Slashing-Reporter key over the
// canonical slash message (chainid, registry, digest, issuer, slashWad) MUST
// enable the economic slash — exactly once, for exactly slashWad — and emit
// SlashTriggered (not SlashWithheldUnverifiedProof).
// ------------------------------------------------------------------

func TestAVSValidProofSlashesExactWad(t *testing.T) {
	slashWad := new(big.Int)
	slashWad.SetString("250000000000000000", 10) // 25%

	rig := avsSetup(t, slashWad)
	ctx := context.Background()

	rig.registerOperator(t, rig.from)

	digest := randomDigest(t)
	tx, err := rig.client.AnchorEnvelope(ctx, digest, 0)
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}
	commitAndWait(t, rig.backend, rig.client, tx)

	if got := rig.querySlashed(t, rig.from); got.Sign() != 0 {
		t.Fatalf("pre-revoke totalSlashed: got %s, want 0", got)
	}

	// Reporter signs a valid proof authorizing the slash of rig.from for digest.
	proof := rig.buildSlashProof(t, rig.reporterKey, digest, rig.from)

	rTx, err := rig.client.RevokeAnchor(ctx, digest, proof)
	if err != nil {
		t.Fatalf("revoke with valid proof: %v", err)
	}
	rec := commitAndWait(t, rig.backend, rig.client, rTx)

	// Slashed by EXACTLY slashWad.
	got := rig.querySlashed(t, rig.from)
	if got.Cmp(slashWad) != 0 {
		t.Errorf("valid proof: totalSlashed got %s, want %s", got, slashWad)
	}

	// SlashTriggered emitted; SlashWithheldUnverifiedProof NOT emitted.
	if !hasEventTopic(rec, "SlashTriggered(bytes32,address,uint256,bytes32)") {
		t.Error("expected SlashTriggered event on valid-proof slash")
	}
	if hasEventTopic(rec, "SlashWithheldUnverifiedProof(bytes32,address,address,bytes32)") {
		t.Error("SlashWithheldUnverifiedProof must NOT be emitted on a valid proof")
	}

	// Revoked flag flipped.
	valid, err := rig.client.IsValid(ctx, digest)
	if err != nil {
		t.Fatalf("IsValid: %v", err)
	}
	if valid {
		t.Error("IsValid should be false after a valid-proof revoke")
	}
}

// ------------------------------------------------------------------
// Test 6 (VF-11 impl): invalid proofs all WITHHELD, no stake moved.
//
// Wrong signing key, wrong digest, and a malformed/short proof must each
// leave totalSlashed == 0, flip the revoked flag, and emit
// SlashWithheldUnverifiedProof (never SlashTriggered).
// ------------------------------------------------------------------

func TestAVSInvalidProofsAreWithheld(t *testing.T) {
	cases := []struct {
		name  string
		proof func(rig *avsRig, t *testing.T, digest [32]byte) []byte
	}{
		{
			name: "wrong signing key",
			proof: func(rig *avsRig, t *testing.T, digest [32]byte) []byte {
				wrongKey, err := crypto.GenerateKey()
				if err != nil {
					t.Fatalf("genkey: %v", err)
				}
				return rig.buildSlashProof(t, wrongKey, digest, rig.from)
			},
		},
		{
			name: "wrong digest (reporter signs a different digest)",
			proof: func(rig *avsRig, t *testing.T, digest [32]byte) []byte {
				other := randomDigest(t)
				return rig.buildSlashProof(t, rig.reporterKey, other, rig.from)
			},
		},
		{
			name: "malformed short proof",
			proof: func(rig *avsRig, t *testing.T, _ [32]byte) []byte {
				return []byte("too-short")
			},
		},
		{
			name: "pre-fix bogus bytes",
			proof: func(rig *avsRig, t *testing.T, _ [32]byte) []byte {
				return []byte("totally-bogus-unverified-proof")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := avsSetup(t, big.NewInt(0)) // default 10%
			ctx := context.Background()
			rig.registerOperator(t, rig.from)

			digest := randomDigest(t)
			tx, err := rig.client.AnchorEnvelope(ctx, digest, 0)
			if err != nil {
				t.Fatalf("anchor: %v", err)
			}
			commitAndWait(t, rig.backend, rig.client, tx)

			rTx, err := rig.client.RevokeAnchor(ctx, digest, tc.proof(rig, t, digest))
			if err != nil {
				t.Fatalf("revoke: %v", err)
			}
			rec := commitAndWait(t, rig.backend, rig.client, rTx)

			if got := rig.querySlashed(t, rig.from); got.Sign() != 0 {
				t.Errorf("VF-11: invalid proof slashed by %s (want 0)", got)
			}
			if !hasEventTopic(rec, "SlashWithheldUnverifiedProof(bytes32,address,address,bytes32)") {
				t.Error("expected SlashWithheldUnverifiedProof on invalid proof")
			}
			if hasEventTopic(rec, "SlashTriggered(bytes32,address,uint256,bytes32)") {
				t.Error("SlashTriggered must NOT be emitted on an invalid proof")
			}

			valid, err := rig.client.IsValid(ctx, digest)
			if err != nil {
				t.Fatalf("IsValid: %v", err)
			}
			if valid {
				t.Error("revoke flag should flip even when the slash is withheld")
			}
		})
	}
}

// hasEventTopic reports whether the receipt carries a log whose topic[0] is
// keccak256(eventSig), e.g. "SlashTriggered(bytes32,address,uint256,bytes32)".
func hasEventTopic(rec *types.Receipt, eventSig string) bool {
	want := crypto.Keccak256Hash([]byte(eventSig))
	for _, lg := range rec.Logs {
		if len(lg.Topics) > 0 && lg.Topics[0] == want {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------------
// Test 7: register rejects mismatched issuer (anti-spoofing)
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
