// Mimir anchor — mint a fresh Ethereum keypair.
//
// Prints a freshly-generated secp256k1 private key + derived public address,
// in a format ready to paste into a `.env` file for use with
// `anchor/cmd/deploy` and `anchor/cmd/verify`.
//
// SECURITY: the private key prints to stdout in cleartext. Do not run this
// where the terminal is shared, screen-recorded, or piped to a logging
// service. The output is the wallet — anyone who sees it controls it.
//
// Usage:
//
//   cd anchor/go && go run ./cmd/genkey
//
// Sample output:
//
//   address     : 0xAbC123...
//   private key : aabbcc... (64 hex chars, no 0x prefix)
//
//   Next: fund the address with ~0.01 testnet ETH from a faucet,
//         then add the private-key line to mimir/.env:
//             HOLESKY_PRIVATE_KEY=<the-64-hex-chars-printed-above>
package main

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/crypto"
)

func main() {
	priv, err := crypto.GenerateKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "genkey failed: %v\n", err)
		os.Exit(1)
	}

	privHex := hex.EncodeToString(crypto.FromECDSA(priv))
	addr := crypto.PubkeyToAddress(priv.PublicKey)

	fmt.Println("==== Fresh wallet ====")
	fmt.Printf("  address     : %s\n", addr.Hex())
	fmt.Printf("  private key : %s\n", privHex)
	fmt.Println()
	fmt.Println("Next:")
	fmt.Println("  1. Send ~0.01 testnet ETH to the address above from a faucet:")
	fmt.Println("       https://sepolia-faucet.pk910.de/         (Sepolia, PoW)")
	fmt.Println("       https://www.alchemy.com/faucets/ethereum-sepolia  (Sepolia)")
	fmt.Println("       https://holesky-faucet.pk910.de/         (Holesky, if still up)")
	fmt.Println("  2. Add to mimir/.env (it is .gitignore'd; never committed):")
	fmt.Println("       HOLESKY_RPC_URL=https://ethereum-sepolia.publicnode.com")
	fmt.Printf("       HOLESKY_PRIVATE_KEY=%s\n", privHex)
	fmt.Println("  3. Confirm balance is > 0 before deploy:")
	fmt.Println("       cd anchor/go && go run ./cmd/deploy   # script prints balance at start")
}
