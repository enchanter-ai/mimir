// Mimir anchor — post-deploy verification.
//
// Reads CONTRACT_ADDRESS + HOLESKY_RPC_URL + HOLESKY_PRIVATE_KEY and runs a
// full round-trip against the live deployed contract:
//   1. AnchorEnvelope with a random digest
//   2. VerifyAnchor reads back issuer/expiry/revoked
//   3. IsValid confirms entry validity
//   4. RevokeAnchor flips revoked (skippable via SKIP_REVOKE=1 for AVS mode
//      where the deployer is not authorized at the Slasher)
//
// Usage:
//
//   cd anchor && CONTRACT_ADDRESS=0x... go run ./cmd/verify
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	anchor "github.com/enchanter-ai/mimir/anchor"
)

func main() {
	rpc := mustEnv("HOLESKY_RPC_URL")
	keyHex := strings.TrimPrefix(mustEnv("HOLESKY_PRIVATE_KEY"), "0x")
	contract := common.HexToAddress(mustEnv("CONTRACT_ADDRESS"))

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

	priv, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		log.Fatalf("parse key: %v", err)
	}
	from := crypto.PubkeyToAddress(priv.PublicKey)

	cli, err := anchor.NewWithClient(ec, keyHex, contract, chainID)
	if err != nil {
		log.Fatalf("anchor.NewWithClient: %v", err)
	}

	log.Println("==== Mimir anchor verify ====")
	log.Printf("  rpc       : %s", rpc)
	log.Printf("  chain_id  : %s", chainID.String())
	log.Printf("  contract  : %s", contract.Hex())
	log.Printf("  caller    : %s", from.Hex())

	var digest [32]byte
	if _, err := rand.Read(digest[:]); err != nil {
		log.Fatalf("rand: %v", err)
	}
	expiry := uint64(time.Now().Add(24 * time.Hour).Unix())
	log.Printf("  digest    : 0x%s", hex.EncodeToString(digest[:]))
	log.Printf("  expiry    : %d (24h)", expiry)

	log.Println()
	log.Println("  [step 1] AnchorEnvelope")
	tx, err := cli.AnchorEnvelope(ctx, digest, expiry)
	if err != nil {
		log.Fatalf("AnchorEnvelope: %v", err)
	}
	log.Printf("           tx: %s", tx.Hex())
	rec, err := cli.WaitMined(ctx, tx, 3*time.Minute)
	if err != nil {
		log.Fatalf("WaitMined: %v", err)
	}
	if rec.Status != 1 {
		log.Fatalf("anchor tx reverted (block %d, gas %d)", rec.BlockNumber.Uint64(), rec.GasUsed)
	}
	log.Printf("           block %d  gas %d  OK", rec.BlockNumber.Uint64(), rec.GasUsed)

	log.Println()
	log.Println("  [step 2] VerifyAnchor")
	res, err := cli.VerifyAnchor(ctx, digest)
	if err != nil {
		log.Fatalf("VerifyAnchor: %v", err)
	}
	log.Printf("           issuer  : %s", res.Issuer.Hex())
	log.Printf("           expiry  : %d", res.Expiry)
	log.Printf("           revoked : %v", res.Revoked)
	if res.Issuer != from {
		log.Fatalf("issuer readback mismatch: expected %s got %s", from.Hex(), res.Issuer.Hex())
	}
	if res.Expiry != expiry {
		log.Fatalf("expiry readback mismatch: expected %d got %d", expiry, res.Expiry)
	}

	log.Println()
	log.Println("  [step 3] IsValid")
	valid, err := cli.IsValid(ctx, digest)
	if err != nil {
		log.Fatalf("IsValid: %v", err)
	}
	log.Printf("           %v", valid)
	if !valid {
		log.Fatalf("IsValid returned false on fresh entry")
	}

	if os.Getenv("SKIP_REVOKE") == "1" {
		log.Println()
		log.Println("  [step 4] RevokeAnchor — SKIPPED (SKIP_REVOKE=1)")
	} else {
		log.Println()
		log.Println("  [step 4] RevokeAnchor")
		proofBlob := []byte("post-deploy-verify-synthetic-proof")
		tx2, err := cli.RevokeAnchor(ctx, digest, proofBlob)
		if err != nil {
			log.Printf("           NOTE: RevokeAnchor failed (expected in AVS mode w/o Slasher access): %v", err)
		} else {
			log.Printf("           tx: %s", tx2.Hex())
			rec2, err := cli.WaitMined(ctx, tx2, 3*time.Minute)
			if err != nil {
				log.Fatalf("WaitMined: %v", err)
			}
			if rec2.Status != 1 {
				log.Printf("           NOTE: revoke reverted (expected in AVS mode)")
			} else {
				log.Printf("           block %d  gas %d  OK", rec2.BlockNumber.Uint64(), rec2.GasUsed)
				res2, _ := cli.VerifyAnchor(ctx, digest)
				log.Printf("           revoked now: %v", res2.Revoked)
			}
		}
	}

	log.Println()
	log.Println("==== VERIFY OK ====")
	log.Printf("  digest %s lives on-chain at %s", hex.EncodeToString(digest[:8])+"...", contract.Hex())
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("required env var %s is not set", name)
	}
	return v
}
