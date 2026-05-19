// Mimir anchor — full AVS-mode deploy.
//
// Deploys, in order:
//   1. MockServiceManager      (test operator registry)
//   2. MockSlasher             (test slashing target)
//   3. MimirValidationRegistry (AVS mode, pointing at the two mocks above)
//
// Run from anchor/go/ so the embedded ABI files resolve relatively.
//
// Usage:
//   HOLESKY_PRIVATE_KEY=<hex> HOLESKY_RPC_URL=https://... go run ./cmd/deploy-avs
//
// (The HOLESKY_ env-var names are kept as-is for parity with cmd/deploy;
// they work for any EVM chain — the script auto-detects via eth_chainId.)
//
// Note on "AVS-mode" honesty: the mocks aren't EigenLayer. They implement
// Mimir's narrow IEigenLayer.sol interface, which differs from real
// EigenLayer v2 AllocationManager.slash's SlashingParams struct. This
// deploy proves "the AVS-mode lifecycle works on a real chain" — not
// "Mimir talks to real EigenLayer." Real-EigenLayer alignment is a v0.2
// follow-up: refactor IEigenLayer.sol + tests + re-deploy.
package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
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
)

const (
	deployGasLimit   = 5_000_000
	receiptTimeout   = 5 * time.Minute
	receiptPollEvery = 3 * time.Second
	defaultSlashWad  = "100000000000000000" // 1e17 = 10%
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
	bytecode, err := hex.DecodeString(s)
	if err != nil {
		log.Fatalf("decode %s bytecode: %v", name, err)
	}
	return parsed, bytecode
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

func sendDeploy(ec *ethclient.Client, priv *ecdsa.PrivateKey, from common.Address, chainID *big.Int, calldata []byte, label string) common.Address {
	ctx := context.Background()
	nonce, err := ec.PendingNonceAt(ctx, from)
	if err != nil {
		log.Fatalf("%s: nonce: %v", label, err)
	}
	gasTip, err := ec.SuggestGasTipCap(ctx)
	if err != nil {
		log.Fatalf("%s: SuggestGasTipCap: %v", label, err)
	}
	head, err := ec.HeaderByNumber(ctx, nil)
	if err != nil {
		log.Fatalf("%s: HeaderByNumber: %v", label, err)
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
		Data:      calldata,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), priv)
	if err != nil {
		log.Fatalf("%s: SignTx: %v", label, err)
	}
	if err := ec.SendTransaction(ctx, signed); err != nil {
		log.Fatalf("%s: SendTransaction: %v", label, err)
	}
	addr := crypto.CreateAddress(from, nonce)
	log.Printf("       %s: tx %s submitted, predicted %s", label, signed.Hash().Hex(), addr.Hex())
	rec, err := waitMined(ec, signed.Hash())
	if err != nil {
		log.Fatalf("%s: waitMined: %v", label, err)
	}
	if rec.Status != types.ReceiptStatusSuccessful {
		log.Fatalf("%s: deploy reverted at block %d (gas used %d)", label, rec.BlockNumber.Uint64(), rec.GasUsed)
	}
	code, _ := ec.CodeAt(ctx, addr, nil)
	if len(code) == 0 {
		log.Fatalf("%s: no code at deployed address %s", label, addr.Hex())
	}
	log.Printf("       %s: block %d  gas %d  OK (%d bytes)", label, rec.BlockNumber.Uint64(), rec.GasUsed, len(code))
	return addr
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
		return fmt.Sprintf("(no etherscan known for chain_id %s) %s", chainID.String(), addr.Hex())
	}
}

func main() {
	rpc := mustEnv("HOLESKY_RPC_URL")
	keyHex := strings.TrimPrefix(mustEnv("HOLESKY_PRIVATE_KEY"), "0x")

	slashWad := new(big.Int)
	if s := os.Getenv("SLASH_WAD"); s != "" {
		if _, ok := slashWad.SetString(s, 10); !ok {
			log.Fatalf("SLASH_WAD: must be base-10 integer")
		}
	} else {
		slashWad.SetString(defaultSlashWad, 10)
	}

	priv, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		log.Fatalf("parse private key: %v", err)
	}
	from := crypto.PubkeyToAddress(priv.PublicKey)

	ec, err := ethclient.Dial(rpc)
	if err != nil {
		log.Fatalf("dial %s: %v", rpc, err)
	}
	defer ec.Close()

	ctx := context.Background()
	chainID, err := ec.ChainID(ctx)
	if err != nil {
		log.Fatalf("ChainID: %v", err)
	}
	bal, err := ec.BalanceAt(ctx, from, nil)
	if err != nil {
		log.Fatalf("BalanceAt: %v", err)
	}
	if bal.Sign() == 0 {
		log.Fatalf("deployer balance zero — fund %s first", from.Hex())
	}

	log.Println("==== Mimir AVS-mode deploy ====")
	log.Printf("  rpc           : %s", rpc)
	log.Printf("  chain_id      : %s", chainID.String())
	log.Printf("  deployer      : %s", from.Hex())
	log.Printf("  balance       : %s wei (%s ETH)", bal.String(), big.NewFloat(0).Quo(new(big.Float).SetInt(bal), big.NewFloat(1e18)).Text('f', 6))
	log.Printf("  slash_wad     : %s (= %s%% of allocation per fraud proof)", slashWad.String(), big.NewFloat(0).Quo(new(big.Float).SetInt(slashWad), big.NewFloat(1e16)).Text('f', 1))

	// Read all three contract artifacts.
	_, mgrBin := readContract("MockServiceManager")
	_, slasherBin := readContract("MockSlasher")
	regABI, regBin := readContract("MimirValidationRegistry")

	// ---- 1. Deploy MockServiceManager (no constructor args) ----
	log.Println()
	log.Println("[1/3] Deploy MockServiceManager")
	mgrAddr := sendDeploy(ec, priv, from, chainID, mgrBin, "MockServiceManager")

	// ---- 2. Deploy MockSlasher (no constructor args) ----
	log.Println()
	log.Println("[2/3] Deploy MockSlasher")
	slasherAddr := sendDeploy(ec, priv, from, chainID, slasherBin, "MockSlasher")

	// ---- 3. Deploy MimirValidationRegistry in AVS mode ----
	log.Println()
	log.Println("[3/3] Deploy MimirValidationRegistry (AVS mode)")
	args, err := regABI.Pack("", mgrAddr, slasherAddr, slashWad)
	if err != nil {
		log.Fatalf("pack registry constructor: %v", err)
	}
	regCalldata := append(append([]byte{}, regBin...), args...)
	regAddr := sendDeploy(ec, priv, from, chainID, regCalldata, "MimirRegistry")

	// ---- Summary ----
	log.Println()
	log.Println("==== AVS DEPLOY OK ====")
	log.Printf("  MockServiceManager   : %s", mgrAddr.Hex())
	log.Printf("  MockSlasher          : %s", slasherAddr.Hex())
	log.Printf("  MimirRegistry (AVS)  : %s", regAddr.Hex())
	log.Println()
	log.Printf("  registry explorer    : %s", explorerURL(chainID, regAddr))
	log.Println()
	log.Println("  Next: register the deployer as an operator + run cmd/verify-avs:")
	log.Println()
	log.Printf("    CONTRACT_ADDRESS=%s \\", regAddr.Hex())
	log.Printf("    MOCK_SERVICE_MANAGER=%s \\", mgrAddr.Hex())
	log.Printf("    MOCK_SLASHER=%s \\", slasherAddr.Hex())
	log.Println("    go run ./cmd/verify-avs")
}
