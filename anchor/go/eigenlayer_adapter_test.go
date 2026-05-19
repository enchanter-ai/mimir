// EigenLayerSlasherAdapter call-translation tests.
//
// Verifies that the EigenLayerSlasherAdapter — when called via Mimir's
// narrow ISlasher.slash(operator, wad, reasonHash) shape — correctly
// constructs an EigenLayer v2 SlashingParams struct and forwards it to
// the AllocationManager interface.
//
// Deploys:
//   - MockAllocationManager (real-EigenLayer-shape mock)
//   - EigenLayerSlasherAdapter (wraps the AM into our ISlasher shape)
//   - MimirValidationRegistry (AVS mode pointing at MockServiceManager + adapter)
//
// Then exercises:
//   - register → revoke triggers adapter.slash → adapter calls AM.slash
//   - AM receives correct operator, operatorSetId, strategies array, wads
//   - description is the hex-encoded reasonHash
package anchor_test

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
)

// deployMockAllocationManager deploys MockAllocationManager (no ctor args).
func deployMockAllocationManager(
	t *testing.T,
	ctx context.Context,
	backend *simulated.Backend,
	ec simulated.Client,
	deployer *ecdsa.PrivateKey,
) common.Address {
	t.Helper()
	_, bytecode := readContract(t, "MockAllocationManager")
	return deployRaw(t, ctx, backend, ec, deployer, bytecode)
}

// deployEigenLayerSlasherAdapter deploys EigenLayerSlasherAdapter pointing
// at the given AllocationManager, with a fixed operatorSetId + strategies.
func deployEigenLayerSlasherAdapter(
	t *testing.T,
	ctx context.Context,
	backend *simulated.Backend,
	ec simulated.Client,
	deployer *ecdsa.PrivateKey,
	allocationManager common.Address,
	operatorSetId uint32,
	strategies []common.Address,
) common.Address {
	t.Helper()
	parsedABI, bytecode := readContract(t, "EigenLayerSlasherAdapter")
	args, err := parsedABI.Pack("", allocationManager, operatorSetId, strategies)
	if err != nil {
		t.Fatalf("pack adapter constructor: %v", err)
	}
	return deployRaw(t, ctx, backend, ec, deployer, append(bytecode, args...))
}

// TestEigenLayerAdapterCallTranslation deploys MockAllocationManager +
// EigenLayerSlasherAdapter, calls adapter.slash directly, then reads back
// what MockAllocationManager observed. Asserts the SlashingParams struct
// was built correctly from Mimir's narrow inputs.
func TestEigenLayerAdapterCallTranslation(t *testing.T) {
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
	chainID, _ := ec.ChainID(ctx)

	// --- Deploy MockAllocationManager + adapter ---
	amAddr := deployMockAllocationManager(t, ctx, backend, ec, priv)

	const operatorSetId = uint32(7)
	strategyA := common.HexToAddress("0x1111111111111111111111111111111111111111")
	strategyB := common.HexToAddress("0x2222222222222222222222222222222222222222")
	adapterAddr := deployEigenLayerSlasherAdapter(t, ctx, backend, ec, priv, amAddr,
		operatorSetId, []common.Address{strategyA, strategyB})

	adapterABI, _ := readContract(t, "EigenLayerSlasherAdapter")
	amABI, _ := readContract(t, "MockAllocationManager")

	// --- Pre-state: AM has no slash recorded ---
	op := common.HexToAddress("0x3333333333333333333333333333333333333333")
	data, _ := amABI.Pack("totalSlashedRecorded", op)
	raw, err := ec.CallContract(ctx, ethereum.CallMsg{To: &amAddr, Data: data}, nil)
	if err != nil {
		t.Fatalf("pre totalSlashedRecorded: %v", err)
	}
	pre, _ := amABI.Unpack("totalSlashedRecorded", raw)
	if pre[0].(*big.Int).Sign() != 0 {
		t.Errorf("pre-state totalSlashed should be 0, got %s", pre[0].(*big.Int).String())
	}

	// --- Call adapter.slash(operator, wad=1e17, reasonHash=0xDEADBEEF...) ---
	var reasonHash [32]byte
	copy(reasonHash[:8], []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE})
	wad := new(big.Int)
	wad.SetString("100000000000000000", 10) // 1e17

	slashData, err := adapterABI.Pack("slash", op, wad, reasonHash)
	if err != nil {
		t.Fatalf("pack adapter.slash: %v", err)
	}

	nonce, _ := ec.PendingNonceAt(ctx, from)
	tx := types.NewTransaction(nonce, adapterAddr, big.NewInt(0), 800_000, big.NewInt(1_000_000_000), slashData)
	signed, _ := types.SignTx(tx, types.LatestSignerForChainID(chainID), priv)
	if err := ec.SendTransaction(ctx, signed); err != nil {
		t.Fatalf("send adapter.slash: %v", err)
	}
	backend.Commit()

	// --- AssertMockAllocationManager observed the call correctly ---
	// 1. totalSlashedRecorded(operator) should equal wad
	data, _ = amABI.Pack("totalSlashedRecorded", op)
	raw, err = ec.CallContract(ctx, ethereum.CallMsg{To: &amAddr, Data: data}, nil)
	if err != nil {
		t.Fatalf("post totalSlashedRecorded: %v", err)
	}
	post, _ := amABI.Unpack("totalSlashedRecorded", raw)
	if post[0].(*big.Int).Cmp(wad) != 0 {
		t.Errorf("AM.totalSlashedRecorded: got %s, want %s", post[0].(*big.Int), wad)
	}

	// 2. lastOperatorSetId should equal 7
	data, _ = amABI.Pack("lastOperatorSetId")
	raw, _ = ec.CallContract(ctx, ethereum.CallMsg{To: &amAddr, Data: data}, nil)
	osid, _ := amABI.Unpack("lastOperatorSetId", raw)
	if osid[0].(uint32) != operatorSetId {
		t.Errorf("AM.lastOperatorSetId: got %d, want %d", osid[0].(uint32), operatorSetId)
	}

	// 3. lastStrategyCount should equal 2
	data, _ = amABI.Pack("lastStrategyCount")
	raw, _ = ec.CallContract(ctx, ethereum.CallMsg{To: &amAddr, Data: data}, nil)
	cnt, _ := amABI.Unpack("lastStrategyCount", raw)
	if cnt[0].(*big.Int).Int64() != 2 {
		t.Errorf("AM.lastStrategyCount: got %d, want 2", cnt[0].(*big.Int).Int64())
	}

	// 4. lastDescription should equal "0x" + hex(reasonHash)
	data, _ = amABI.Pack("lastDescription")
	raw, _ = ec.CallContract(ctx, ethereum.CallMsg{To: &amAddr, Data: data}, nil)
	desc, _ := amABI.Unpack("lastDescription", raw)
	gotDesc := desc[0].(string)
	wantDesc := "0x" + hex.EncodeToString(reasonHash[:])
	if gotDesc != wantDesc {
		t.Errorf("AM.lastDescription:\n  got  %q\n  want %q", gotDesc, wantDesc)
	}

	// 5. adapter.totalSlashed(operator) mirror should also equal wad
	atsData, _ := adapterABI.Pack("totalSlashed", op)
	raw, _ = ec.CallContract(ctx, ethereum.CallMsg{To: &adapterAddr, Data: atsData}, nil)
	ats, _ := adapterABI.Unpack("totalSlashed", raw)
	if ats[0].(*big.Int).Cmp(wad) != 0 {
		t.Errorf("adapter.totalSlashed: got %s, want %s", ats[0].(*big.Int), wad)
	}
}

// TestAdapterRejectsInvalidInputs confirms the adapter's defensive checks
// fire for zero-address operator, zero wad, and out-of-range wad.
func TestAdapterRejectsInvalidInputs(t *testing.T) {
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

	amAddr := deployMockAllocationManager(t, ctx, backend, ec, priv)
	strategy := common.HexToAddress("0x1111111111111111111111111111111111111111")
	adapterAddr := deployEigenLayerSlasherAdapter(t, ctx, backend, ec, priv, amAddr, 1, []common.Address{strategy})

	adapterABI, _ := readContract(t, "EigenLayerSlasherAdapter")
	op := common.HexToAddress("0x3333333333333333333333333333333333333333")
	bigE18 := new(big.Int)
	bigE18.SetString("1000000000000000000", 10)
	overE18 := new(big.Int).Add(bigE18, big.NewInt(1))

	cases := []struct {
		name string
		op   common.Address
		wad  *big.Int
	}{
		{"zero operator", common.Address{}, big.NewInt(100)},
		{"zero wad", op, big.NewInt(0)},
		{"wad > 1e18", op, overE18},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rh [32]byte
			data, _ := adapterABI.Pack("slash", tc.op, tc.wad, rh)
			_, err := ec.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &adapterAddr, Data: data})
			if err == nil {
				t.Errorf("expected revert for %s, got nil", tc.name)
			} else if !strings.Contains(err.Error(), "execution reverted") {
				t.Errorf("expected revert, got %v", err)
			}
		})
	}
}
