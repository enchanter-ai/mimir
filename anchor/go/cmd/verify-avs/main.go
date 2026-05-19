// Mimir anchor — AVS-mode post-deploy lifecycle verifier.
//
// Drives the full AVS-mode flow against a live deployment:
//   1. Register the caller as an operator on MockServiceManager.
//   2. AnchorEnvelope (now operator-gated).
//   3. VerifyAnchor reads back (issuer, expiry, revoked).
//   4. IsValid → true.
//   5. RevokeAnchor with a fraud-proof blob.
//      In AVS mode this also calls MockSlasher.slash() with the configured wad.
//   6. Read MockSlasher.totalSlashed(operator); assert == slashWad.
//   7. VerifyAnchor again → revoked: true; IsValid → false.
//
// Usage (run from anchor/go/):
//
//   CONTRACT_ADDRESS=0x... \
//   MOCK_SERVICE_MANAGER=0x... \
//   MOCK_SLASHER=0x... \
//   HOLESKY_RPC_URL=https://... HOLESKY_PRIVATE_KEY=<hex> \
//     go run ./cmd/verify-avs
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
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

	anchor "github.com/enchanter-ai/mimir/anchor"
)

const receiptTimeout = 5 * time.Minute

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("required env var %s is not set", name)
	}
	return v
}

func readABI(name string) abi.ABI {
	b, err := os.ReadFile(filepath.Join("abi", name+".json"))
	if err != nil {
		log.Fatalf("read %s.json: %v (run from anchor/go/)", name, err)
	}
	parsed, err := abi.JSON(strings.NewReader(string(b)))
	if err != nil {
		log.Fatalf("parse %s ABI: %v", name, err)
	}
	return parsed
}

func sendTx(ec *ethclient.Client, priv *ecdsa.PrivateKey, from, to common.Address, chainID *big.Int, data []byte, label string) *types.Receipt {
	ctx := context.Background()
	nonce, err := ec.PendingNonceAt(ctx, from)
	if err != nil {
		log.Fatalf("%s: nonce: %v", label, err)
	}
	tip, err := ec.SuggestGasTipCap(ctx)
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
	feeCap := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)

	gas, err := ec.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &to, Data: data})
	if err != nil || gas == 0 {
		log.Printf("  %s: gas estimate failed (%v) — using 500k fallback", label, err)
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
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), priv)
	if err != nil {
		log.Fatalf("%s: SignTx: %v", label, err)
	}
	if err := ec.SendTransaction(ctx, signed); err != nil {
		log.Fatalf("%s: SendTransaction: %v", label, err)
	}
	log.Printf("  %s: tx %s", label, signed.Hash().Hex())

	deadline := time.Now().Add(receiptTimeout)
	for {
		rec, err := ec.TransactionReceipt(ctx, signed.Hash())
		if err == nil {
			if rec.Status != types.ReceiptStatusSuccessful {
				log.Fatalf("  %s: tx REVERTED at block %d (gas used %d)", label, rec.BlockNumber.Uint64(), rec.GasUsed)
			}
			log.Printf("  %s: block %d  gas %d  OK", label, rec.BlockNumber.Uint64(), rec.GasUsed)
			return rec
		}
		if !errors.Is(err, ethereum.NotFound) && !strings.Contains(err.Error(), "not found") {
			log.Fatalf("  %s: receipt: %v", label, err)
		}
		if time.Now().After(deadline) {
			log.Fatalf("  %s: receipt timeout", label)
		}
		time.Sleep(3 * time.Second)
	}
}

func main() {
	rpc := mustEnv("HOLESKY_RPC_URL")
	keyHex := strings.TrimPrefix(mustEnv("HOLESKY_PRIVATE_KEY"), "0x")
	regAddr := common.HexToAddress(mustEnv("CONTRACT_ADDRESS"))
	mgrAddr := common.HexToAddress(mustEnv("MOCK_SERVICE_MANAGER"))
	slasherAddr := common.HexToAddress(mustEnv("MOCK_SLASHER"))

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
	chainID, err := ec.ChainID(ctx)
	if err != nil {
		log.Fatalf("ChainID: %v", err)
	}

	cli, err := anchor.NewWithClient(ec, keyHex, regAddr, chainID)
	if err != nil {
		log.Fatalf("anchor client: %v", err)
	}

	mgrABI := readABI("MockServiceManager")
	slasherABI := readABI("MockSlasher")

	log.Println("==== Mimir AVS-mode lifecycle verify ====")
	log.Printf("  rpc            : %s", rpc)
	log.Printf("  chain_id       : %s", chainID.String())
	log.Printf("  caller         : %s", from.Hex())
	log.Printf("  registry       : %s", regAddr.Hex())
	log.Printf("  service mgr    : %s", mgrAddr.Hex())
	log.Printf("  slasher        : %s", slasherAddr.Hex())

	// Random digest + expiry.
	var digest [32]byte
	if _, err := rand.Read(digest[:]); err != nil {
		log.Fatalf("rand: %v", err)
	}
	expiry := uint64(time.Now().Add(24 * time.Hour).Unix())
	log.Printf("  digest         : 0x%s", hex.EncodeToString(digest[:]))
	log.Printf("  expiry         : %d (24h)", expiry)

	// ---- 1. Register operator ----
	log.Println()
	log.Println("[1/7] MockServiceManager.registerOperator(caller)")
	data, _ := mgrABI.Pack("registerOperator", from)
	sendTx(ec, priv, from, mgrAddr, chainID, data, "registerOperator")

	// ---- 2. AnchorEnvelope (operator-gated) ----
	log.Println()
	log.Println("[2/7] AnchorEnvelope (operator-gated)")
	tx, err := cli.AnchorEnvelope(ctx, digest, expiry)
	if err != nil {
		log.Fatalf("AnchorEnvelope: %v", err)
	}
	rec, err := cli.WaitMined(ctx, tx, receiptTimeout)
	if err != nil {
		log.Fatalf("AnchorEnvelope WaitMined: %v", err)
	}
	if rec.Status != 1 {
		log.Fatalf("AnchorEnvelope reverted at block %d (gas %d) — operator gating might be wrong",
			rec.BlockNumber.Uint64(), rec.GasUsed)
	}
	log.Printf("  AnchorEnvelope: block %d  gas %d  OK", rec.BlockNumber.Uint64(), rec.GasUsed)

	// ---- 3. VerifyAnchor reads back ----
	log.Println()
	log.Println("[3/7] VerifyAnchor (read-back)")
	res, err := cli.VerifyAnchor(ctx, digest)
	if err != nil {
		log.Fatalf("VerifyAnchor: %v", err)
	}
	log.Printf("  issuer  : %s  (expect %s)", res.Issuer.Hex(), from.Hex())
	log.Printf("  expiry  : %d  (expect %d)", res.Expiry, expiry)
	log.Printf("  revoked : %v", res.Revoked)
	if res.Issuer != from || res.Expiry != expiry || res.Revoked {
		log.Fatalf("read-back mismatch")
	}

	// ---- 4. IsValid → true ----
	log.Println()
	log.Println("[4/7] IsValid")
	valid, err := cli.IsValid(ctx, digest)
	if err != nil {
		log.Fatalf("IsValid: %v", err)
	}
	log.Printf("  IsValid : %v", valid)
	if !valid {
		log.Fatalf("expected IsValid=true on fresh entry")
	}

	// ---- 5. RevokeAnchor (also triggers slash hook) ----
	log.Println()
	log.Println("[5/7] RevokeAnchor (also fires Slasher.slash)")
	tx2, err := cli.RevokeAnchor(ctx, digest, []byte("avs-mode-live-fraud-proof-blob"))
	if err != nil {
		log.Fatalf("RevokeAnchor: %v", err)
	}
	rec2, err := cli.WaitMined(ctx, tx2, receiptTimeout)
	if err != nil {
		log.Fatalf("RevokeAnchor WaitMined: %v", err)
	}
	if rec2.Status != 1 {
		log.Fatalf("RevokeAnchor reverted at block %d (gas %d)", rec2.BlockNumber.Uint64(), rec2.GasUsed)
	}
	log.Printf("  RevokeAnchor: block %d  gas %d  OK", rec2.BlockNumber.Uint64(), rec2.GasUsed)

	// ---- 6. Read MockSlasher.totalSlashed(from) ----
	log.Println()
	log.Println("[6/7] MockSlasher.totalSlashed(caller)")
	queryData, _ := slasherABI.Pack("totalSlashed", from)
	out, err := ec.CallContract(ctx, ethereum.CallMsg{To: &slasherAddr, Data: queryData}, nil)
	if err != nil {
		log.Fatalf("CallContract totalSlashed: %v", err)
	}
	unpacked, err := slasherABI.Unpack("totalSlashed", out)
	if err != nil {
		log.Fatalf("unpack totalSlashed: %v", err)
	}
	total := unpacked[0].(*big.Int)
	expectedWad := new(big.Int)
	expectedWad.SetString("100000000000000000", 10) // 1e17
	log.Printf("  totalSlashed   : %s (expect %s = 10%% wad)", total.String(), expectedWad.String())
	if total.Cmp(expectedWad) != 0 {
		log.Fatalf("totalSlashed mismatch: got %s, want %s", total.String(), expectedWad.String())
	}

	// ---- 7. Re-verify ----
	log.Println()
	log.Println("[7/7] Re-verify after revoke")
	res2, err := cli.VerifyAnchor(ctx, digest)
	if err != nil {
		log.Fatalf("VerifyAnchor 2: %v", err)
	}
	valid2, err := cli.IsValid(ctx, digest)
	if err != nil {
		log.Fatalf("IsValid 2: %v", err)
	}
	log.Printf("  revoked        : %v (expect true)", res2.Revoked)
	log.Printf("  IsValid        : %v (expect false)", valid2)
	if !res2.Revoked || valid2 {
		log.Fatalf("post-revoke state wrong")
	}

	log.Println()
	log.Println("==== AVS-MODE LIFECYCLE PASS ====")
	log.Printf("  operator %s slashed by %s (10%% wad)", from.Hex(), total.String())
	switch chainID.Int64() {
	case 11155111:
		log.Printf("  https://sepolia.etherscan.io/address/%s", regAddr.Hex())
	}
	_ = fmt.Sprintf("") // silence unused if not all branches use fmt
}
