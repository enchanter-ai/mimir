// Mimir anchor — Holesky / testnet deploy.
//
// Reads HOLESKY_PRIVATE_KEY + HOLESKY_RPC_URL (and optionally SERVICE_MANAGER /
// SLASHER / SLASH_WAD) from the environment, deploys MimirValidationRegistry,
// and prints the deployed address plus an Etherscan link.
//
// Two modes are chosen automatically from env:
//
//   1. PERMISSIONLESS — leave SERVICE_MANAGER + SLASHER unset.
//      register/revoke are open to anyone; no on-chain slashing.
//
//   2. AVS — set SERVICE_MANAGER + SLASHER (and SLASH_WAD if non-default).
//      register is operator-gated; revoke triggers Slasher.slash().
//
// Usage:
//
//   cd anchor && go run ./cmd/deploy
//
// Required env:
//   HOLESKY_PRIVATE_KEY=<hex>
//   HOLESKY_RPC_URL=https://...
//
// Optional env:
//   SERVICE_MANAGER=0x...
//   SLASHER=0x...
//   SLASH_WAD=100000000000000000   (1e17 = 10%)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
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
	defaultSlashWad  = "100000000000000000" // 1e17 = 10%
	deployGasLimit   = 5_000_000
	receiptPollEvery = 3 * time.Second
	receiptTimeout   = 5 * time.Minute
)

func main() {
	rpc := mustEnv("HOLESKY_RPC_URL")
	keyHex := strings.TrimPrefix(mustEnv("HOLESKY_PRIVATE_KEY"), "0x")

	var serviceManager, slasher common.Address
	if s := os.Getenv("SERVICE_MANAGER"); s != "" {
		serviceManager = common.HexToAddress(s)
	}
	if s := os.Getenv("SLASHER"); s != "" {
		slasher = common.HexToAddress(s)
	}

	slashWad := new(big.Int)
	if s := os.Getenv("SLASH_WAD"); s != "" {
		if _, ok := slashWad.SetString(s, 10); !ok {
			log.Fatalf("SLASH_WAD must be a base-10 integer; got %q", s)
		}
	} else {
		slashWad.SetString(defaultSlashWad, 10)
	}

	mgrZero := serviceManager == (common.Address{})
	slZero := slasher == (common.Address{})
	if mgrZero != slZero {
		log.Fatalf("SERVICE_MANAGER and SLASHER must both be set or both unset; got mgr=%s slasher=%s",
			serviceManager.Hex(), slasher.Hex())
	}
	mode := "PERMISSIONLESS"
	if !mgrZero {
		mode = "AVS"
	}

	priv, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		log.Fatalf("parse HOLESKY_PRIVATE_KEY: %v", err)
	}
	from := crypto.PubkeyToAddress(priv.PublicKey)

	ec, err := ethclient.Dial(rpc)
	if err != nil {
		log.Fatalf("dial %s: %v", rpc, err)
	}
	defer ec.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	chainID, err := ec.ChainID(ctx)
	cancel()
	if err != nil {
		log.Fatalf("ChainID: %v", err)
	}

	bal, err := ec.BalanceAt(context.Background(), from, nil)
	if err != nil {
		log.Fatalf("BalanceAt: %v", err)
	}

	log.Println("==== Mimir anchor deploy ====")
	log.Printf("  rpc           : %s", rpc)
	log.Printf("  chain_id      : %s", chainID.String())
	log.Printf("  deployer      : %s", from.Hex())
	log.Printf("  balance       : %s ETH", weiToEth(bal))
	log.Printf("  mode          : %s", mode)
	log.Printf("  service_mgr   : %s", serviceManager.Hex())
	log.Printf("  slasher       : %s", slasher.Hex())
	log.Printf("  slash_wad     : %s (%.1f%%)", slashWad.String(), wadPercent(slashWad))

	if bal.Sign() == 0 {
		log.Fatalf("deployer balance is zero — fund %s with testnet ETH before deploy", from.Hex())
	}

	parsedABI, err := loadABI()
	if err != nil {
		log.Fatalf("loadABI: %v", err)
	}
	args, err := parsedABI.Pack("", serviceManager, slasher, slashWad)
	if err != nil {
		log.Fatalf("pack constructor args: %v", err)
	}
	creationCalldata := append(anchor.DeployBytecodeBytes(), args...)

	nonce, err := ec.PendingNonceAt(context.Background(), from)
	if err != nil {
		log.Fatalf("PendingNonceAt: %v", err)
	}

	gasTip, err := ec.SuggestGasTipCap(context.Background())
	if err != nil {
		log.Fatalf("SuggestGasTipCap: %v", err)
	}
	head, err := ec.HeaderByNumber(context.Background(), nil)
	if err != nil {
		log.Fatalf("HeaderByNumber: %v", err)
	}
	baseFee := head.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(0)
	}
	gasFeeCap := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), gasTip)

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: gasTip,
		GasFeeCap: gasFeeCap,
		Gas:       deployGasLimit,
		Data:      creationCalldata,
	})

	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), priv)
	if err != nil {
		log.Fatalf("SignTx: %v", err)
	}

	if err := ec.SendTransaction(context.Background(), signed); err != nil {
		log.Fatalf("SendTransaction: %v", err)
	}

	deployedAddr := crypto.CreateAddress(from, nonce)
	log.Printf("  tx submitted  : %s", signed.Hash().Hex())
	log.Printf("  predicted addr: %s", deployedAddr.Hex())
	log.Printf("  waiting for receipt (timeout %s)...", receiptTimeout)

	rec, err := waitMined(ec, signed.Hash())
	if err != nil {
		log.Fatalf("waitMined: %v", err)
	}
	if rec.Status != types.ReceiptStatusSuccessful {
		log.Fatalf("deployment reverted (block %d, gas_used %d)", rec.BlockNumber.Uint64(), rec.GasUsed)
	}

	code, err := ec.CodeAt(context.Background(), deployedAddr, nil)
	if err != nil {
		log.Fatalf("CodeAt: %v", err)
	}
	if len(code) == 0 {
		log.Fatalf("deployment succeeded but no code at %s", deployedAddr.Hex())
	}

	log.Println("==== DEPLOY OK ====")
	log.Printf("  contract      : %s", deployedAddr.Hex())
	log.Printf("  block         : %d", rec.BlockNumber.Uint64())
	log.Printf("  gas used      : %d", rec.GasUsed)
	log.Printf("  effective gas : %s wei", rec.EffectiveGasPrice.String())
	log.Printf("  bytecode      : %d bytes", len(code))
	log.Printf("  etherscan     : %s", etherscanURL(chainID, deployedAddr))
	log.Println()
	log.Println("Next:")
	log.Printf("  CONTRACT_ADDRESS=%s go run ./cmd/verify", deployedAddr.Hex())
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("required env var %s is not set", name)
	}
	return v
}

func loadABI() (abi.ABI, error) {
	// Run as `go run ./cmd/deploy` from anchor/go/.
	// The ABI JSON lives at anchor/go/abi/MimirValidationRegistry.json.
	const path = "abi/MimirValidationRegistry.json"
	b, err := os.ReadFile(path)
	if err != nil {
		return abi.ABI{}, fmt.Errorf("read %s (run from anchor/go/): %w", path, err)
	}
	return abi.JSON(strings.NewReader(string(b)))
}

func waitMined(ec *ethclient.Client, h common.Hash) (*types.Receipt, error) {
	deadline := time.Now().Add(receiptTimeout)
	for {
		r, err := ec.TransactionReceipt(context.Background(), h)
		if err == nil {
			return r, nil
		}
		if !errors.Is(err, ethereum.NotFound) && !strings.Contains(err.Error(), "not found") {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("receipt timeout after %s", receiptTimeout)
		}
		time.Sleep(receiptPollEvery)
	}
}

func etherscanURL(chainID *big.Int, addr common.Address) string {
	switch chainID.Int64() {
	case 1:
		return "https://etherscan.io/address/" + addr.Hex()
	case 17000:
		return "https://holesky.etherscan.io/address/" + addr.Hex()
	case 11155111:
		return "https://sepolia.etherscan.io/address/" + addr.Hex()
	default:
		return fmt.Sprintf("(no etherscan known for chain_id %s) %s", chainID.String(), addr.Hex())
	}
}

func weiToEth(wei *big.Int) string {
	f := new(big.Float).Quo(new(big.Float).SetInt(wei), big.NewFloat(1e18))
	return f.Text('f', 6)
}

func wadPercent(wad *big.Int) float64 {
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(wad), big.NewFloat(1e18)).Float64()
	return f * 100.0
}
