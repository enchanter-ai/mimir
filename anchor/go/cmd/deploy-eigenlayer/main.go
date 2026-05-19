// Mimir anchor — deploy the full EigenLayer-adapter stack to a live chain.
//
// Deploys five contracts in dependency order:
//
//   1. MockAllocationManager     (target the adapter forwards to;
//                                 replace with real EigenLayer
//                                 AllocationManager address in production)
//   2. EigenLayerSlasherAdapter  (Mimir ISlasher → EigenLayer SlashingParams)
//   3. MockServiceManager        (operator registry; in production this is
//                                 the AVS's ServiceManagerBase contract)
//   4. MimirValidationRegistry   (AVS mode, slasher=adapter)
//
// Then registers the deployer as an operator on MockServiceManager and runs
// a full lifecycle: anchor → revoke → verifies MockAllocationManager.slash
// was called with the correct SlashingParams.
//
// Run from anchor/go/:
//   HOLESKY_RPC_URL=https://... HOLESKY_PRIVATE_KEY=<hex> \
//     go run ./cmd/deploy-eigenlayer
//
// To wire against REAL EigenLayer:
//   - Skip step 1, pass ALLOCATION_MANAGER=<real AllocationManager addr> env
//   - Provide OPERATOR_SET_ID (your AVS's id) + STRATEGIES (comma-separated)
//   - Run on a network where real EigenLayer lives (mainnet, Hoodi).
package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	anchor "github.com/enchanter-ai/mimir/anchor"
)

const (
	deployGasLimit   = 5_000_000
	receiptTimeout   = 5 * time.Minute
	receiptPollEvery = 3 * time.Second
)

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("required env var %s is not set", name)
	}
	return v
}

func readContract(name string) (abi.ABI, []byte) {
	abiBytes, err := os.ReadFile(filepath.Join("abi", name+".json"))
	if err != nil {
		log.Fatalf("read %s.json: %v (run from anchor/go/)", name, err)
	}
	parsed, err := abi.JSON(strings.NewReader(string(abiBytes)))
	if err != nil {
		log.Fatalf("parse %s ABI: %v", name, err)
	}
	binText, err := os.ReadFile(filepath.Join("abi", name+".bin"))
	if err != nil {
		log.Fatalf("read %s.bin: %v", name, err)
	}
	s := strings.TrimSpace(string(binText))
	s = strings.TrimPrefix(s, "0x")
	code, err := hex.DecodeString(s)
	if err != nil {
		log.Fatalf("decode %s: %v", name, err)
	}
	return parsed, code
}

func waitMined(ec *ethclient.Client, h common.Hash) *types.Receipt {
	deadline := time.Now().Add(receiptTimeout)
	for {
		r, err := ec.TransactionReceipt(context.Background(), h)
		if err == nil {
			return r
		}
		if !errors.Is(err, ethereum.NotFound) && !strings.Contains(err.Error(), "not found") {
			log.Fatalf("receipt: %v", err)
		}
		if time.Now().After(deadline) {
			log.Fatalf("receipt timeout for %s", h.Hex())
		}
		time.Sleep(receiptPollEvery)
	}
}

func sendDeploy(ec *ethclient.Client, priv *ecdsa.PrivateKey, from common.Address, chainID *big.Int, calldata []byte, label string) common.Address {
	ctx := context.Background()
	nonce, err := ec.PendingNonceAt(ctx, from)
	if err != nil {
		log.Fatalf("%s: nonce: %v", label, err)
	}
	tip, _ := ec.SuggestGasTipCap(ctx)
	head, _ := ec.HeaderByNumber(ctx, nil)
	baseFee := head.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(0)
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       deployGasLimit,
		Data:      calldata,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), priv)
	if err != nil {
		log.Fatalf("%s sign: %v", label, err)
	}
	if err := ec.SendTransaction(ctx, signed); err != nil {
		log.Fatalf("%s send: %v", label, err)
	}
	addr := crypto.CreateAddress(from, nonce)
	log.Printf("   %s tx %s submitted, predicted %s", label, signed.Hash().Hex(), addr.Hex())
	rec := waitMined(ec, signed.Hash())
	if rec.Status != types.ReceiptStatusSuccessful {
		log.Fatalf("%s deploy reverted at block %d (gas %d)", label, rec.BlockNumber.Uint64(), rec.GasUsed)
	}
	code, _ := ec.CodeAt(ctx, addr, nil)
	log.Printf("   %s block %d  gas %d  OK (%d bytes)", label, rec.BlockNumber.Uint64(), rec.GasUsed, len(code))
	return addr
}

func sendCall(ec *ethclient.Client, priv *ecdsa.PrivateKey, from, to common.Address, chainID *big.Int, data []byte, label string) *types.Receipt {
	ctx := context.Background()
	nonce, _ := ec.PendingNonceAt(ctx, from)
	tip, _ := ec.SuggestGasTipCap(ctx)
	head, _ := ec.HeaderByNumber(ctx, nil)
	baseFee := head.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(0)
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)
	gas, err := ec.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &to, Data: data})
	if err != nil || gas == 0 {
		gas = 500_000
	} else {
		gas = gas * 12 / 10
	}
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       gas,
		To:        &to,
		Data:      data,
	})
	signed, _ := types.SignTx(tx, types.LatestSignerForChainID(chainID), priv)
	if err := ec.SendTransaction(ctx, signed); err != nil {
		log.Fatalf("%s send: %v", label, err)
	}
	log.Printf("   %s tx %s", label, signed.Hash().Hex())
	rec := waitMined(ec, signed.Hash())
	if rec.Status != types.ReceiptStatusSuccessful {
		log.Fatalf("%s reverted at block %d (gas %d)", label, rec.BlockNumber.Uint64(), rec.GasUsed)
	}
	log.Printf("   %s block %d  gas %d  OK", label, rec.BlockNumber.Uint64(), rec.GasUsed)
	return rec
}

func explorerURL(chainID *big.Int, addr common.Address) string {
	switch chainID.Int64() {
	case 1:
		return "https://etherscan.io/address/" + addr.Hex()
	case 11155111:
		return "https://sepolia.etherscan.io/address/" + addr.Hex()
	case 17000:
		return "https://holesky.etherscan.io/address/" + addr.Hex()
	case 560048:
		return "https://hoodi.etherscan.io/address/" + addr.Hex()
	default:
		return addr.Hex()
	}
}

func main() {
	rpc := mustEnv("HOLESKY_RPC_URL")
	keyHex := strings.TrimPrefix(mustEnv("HOLESKY_PRIVATE_KEY"), "0x")
	priv, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		log.Fatalf("parse key: %v", err)
	}
	from := crypto.PubkeyToAddress(priv.PublicKey)

	ec, err := ethclient.Dial(rpc)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer ec.Close()
	ctx := context.Background()
	chainID, _ := ec.ChainID(ctx)
	bal, _ := ec.BalanceAt(ctx, from, nil)
	if bal.Sign() == 0 {
		log.Fatalf("deployer balance zero on chain %s — fund %s first", chainID.String(), from.Hex())
	}

	log.Println("==== Mimir EigenLayer-adapter stack deploy ====")
	log.Printf("  rpc       : %s", rpc)
	log.Printf("  chain_id  : %s", chainID.String())
	log.Printf("  deployer  : %s", from.Hex())
	log.Printf("  balance   : %s ETH", new(big.Float).Quo(new(big.Float).SetInt(bal), big.NewFloat(1e18)).Text('f', 6))

	// Resolve real AllocationManager if provided; otherwise deploy MockAllocationManager.
	var amAddr common.Address
	if s := os.Getenv("ALLOCATION_MANAGER"); s != "" {
		amAddr = common.HexToAddress(s)
		log.Printf("  using REAL AllocationManager at %s", amAddr.Hex())
	} else {
		log.Println("\n[1/4] Deploy MockAllocationManager (no real EigenLayer wired)")
		_, amBin := readContract("MockAllocationManager")
		amAddr = sendDeploy(ec, priv, from, chainID, amBin, "MockAllocationManager")
	}

	log.Println("\n[2/4] Deploy EigenLayerSlasherAdapter")
	adapterABI, adapterBin := readContract("EigenLayerSlasherAdapter")
	operatorSetId := uint32(1)
	strategies := []common.Address{common.HexToAddress("0x1111111111111111111111111111111111111111")}
	args, err := adapterABI.Pack("", amAddr, operatorSetId, strategies)
	if err != nil {
		log.Fatalf("pack adapter constructor: %v", err)
	}
	adapterAddr := sendDeploy(ec, priv, from, chainID, append(adapterBin, args...), "EigenLayerSlasherAdapter")

	log.Println("\n[3/4] Deploy MockServiceManager + AVS registry")
	_, mgrBin := readContract("MockServiceManager")
	mgrAddr := sendDeploy(ec, priv, from, chainID, mgrBin, "MockServiceManager")

	regABI, regBin := readContract("MimirValidationRegistry")
	slashWad := new(big.Int)
	slashWad.SetString("100000000000000000", 10)
	regArgs, _ := regABI.Pack("", mgrAddr, adapterAddr, slashWad)
	regAddr := sendDeploy(ec, priv, from, chainID, append(regBin, regArgs...), "MimirRegistry")

	log.Println("\n[4/4] Live lifecycle — register → anchor → revoke → confirm slash")
	mgrABI, _ := readContract("MockServiceManager")
	amABI, _ := readContract("MockAllocationManager")

	// Register operator
	data, _ := mgrABI.Pack("registerOperator", from)
	sendCall(ec, priv, from, mgrAddr, chainID, data, "registerOperator")

	// Anchor via anchor.Client
	cli, _ := anchor.NewWithClient(ec, keyHex, regAddr, chainID)
	var digest [32]byte
	copy(digest[:], []byte("mimir-eigenlayer-adapter-live"))
	expiry := uint64(time.Now().Add(24 * time.Hour).Unix())
	log.Printf("   digest: 0x%s, expiry %d", hex.EncodeToString(digest[:]), expiry)
	tx, err := cli.AnchorEnvelope(ctx, digest, expiry)
	if err != nil {
		log.Fatalf("anchor: %v", err)
	}
	rec := waitMined(ec, tx)
	log.Printf("   AnchorEnvelope block %d gas %d", rec.BlockNumber.Uint64(), rec.GasUsed)

	// Revoke — fires adapter.slash → AllocationManager.slash
	rTx, err := cli.RevokeAnchor(ctx, digest, []byte("eigenlayer-adapter-live-fraud-proof"))
	if err != nil {
		log.Fatalf("revoke: %v", err)
	}
	rRec := waitMined(ec, rTx)
	log.Printf("   RevokeAnchor block %d gas %d (fired adapter.slash)", rRec.BlockNumber.Uint64(), rRec.GasUsed)

	// Verify AllocationManager received the call with correct shape
	d, _ := amABI.Pack("totalSlashedRecorded", from)
	out, _ := ec.CallContract(ctx, ethereum.CallMsg{To: &amAddr, Data: d}, nil)
	unp, _ := amABI.Unpack("totalSlashedRecorded", out)
	total := unp[0].(*big.Int)

	d, _ = amABI.Pack("lastOperatorSetId")
	out, _ = ec.CallContract(ctx, ethereum.CallMsg{To: &amAddr, Data: d}, nil)
	osid, _ := amABI.Unpack("lastOperatorSetId", out)

	d, _ = amABI.Pack("lastStrategyCount")
	out, _ = ec.CallContract(ctx, ethereum.CallMsg{To: &amAddr, Data: d}, nil)
	cnt, _ := amABI.Unpack("lastStrategyCount", out)

	d, _ = amABI.Pack("lastDescription")
	out, _ = ec.CallContract(ctx, ethereum.CallMsg{To: &amAddr, Data: d}, nil)
	desc, _ := amABI.Unpack("lastDescription", out)

	log.Println()
	log.Println("==== EIGENLAYER ADAPTER STACK LIVE ====")
	log.Printf("  MockAllocationManager      : %s", explorerURL(chainID, amAddr))
	log.Printf("  EigenLayerSlasherAdapter   : %s", explorerURL(chainID, adapterAddr))
	log.Printf("  MockServiceManager         : %s", explorerURL(chainID, mgrAddr))
	log.Printf("  MimirValidationRegistry    : %s", explorerURL(chainID, regAddr))
	log.Println()
	log.Println("  Lifecycle assertions against the LIVE chain:")
	log.Printf("    AllocationManager.totalSlashedRecorded(operator) = %s (expect 100000000000000000 = 1e17 wad)", total.String())
	log.Printf("    AllocationManager.lastOperatorSetId              = %d (expect %d)", osid[0].(uint32), operatorSetId)
	log.Printf("    AllocationManager.lastStrategyCount              = %s (expect %d)", cnt[0].(*big.Int).String(), len(strategies))
	log.Printf("    AllocationManager.lastDescription                = %q", desc[0].(string))

	expectedTotal := slashWad
	if total.Cmp(expectedTotal) != 0 || osid[0].(uint32) != operatorSetId || cnt[0].(*big.Int).Int64() != int64(len(strategies)) {
		log.Fatalf("\n  *** ASSERTION FAILED — see numbers above ***")
	}
	if !strings.HasPrefix(desc[0].(string), "0x") {
		log.Fatalf("\n  description should be hex-encoded reasonHash but doesn't start with 0x")
	}
	log.Println("\n  *** ALL ASSERTIONS PASS — EigenLayer adapter pattern proven on a real chain. ***")
}
