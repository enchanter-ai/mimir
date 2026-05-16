// Test-only deploy helpers used by anchor_test.go and eigenlayer_test.go.
//
// The MimirValidationRegistry constructor takes (IServiceManager, ISlasher,
// uint256 slashWad). These must be ABI-encoded and appended to the creation
// bytecode before deploying. These helpers handle that, and provide the same
// for MockServiceManager and MockSlasher (no-arg constructors).
package anchor_test

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
)

// readContract loads <name>.json (ABI) + <name>.bin (creation bytecode hex)
// from the anchor/go/abi/ directory.
func readContract(t *testing.T, name string) (abi.ABI, []byte) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	abiPath := filepath.Join(wd, "abi", name+".json")
	binPath := filepath.Join(wd, "abi", name+".bin")

	abiBytes, err := os.ReadFile(abiPath)
	if err != nil {
		t.Fatalf("read %s.json: %v", name, err)
	}
	parsed, err := abi.JSON(strings.NewReader(string(abiBytes)))
	if err != nil {
		t.Fatalf("parse %s ABI: %v", name, err)
	}

	binText, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read %s.bin: %v", name, err)
	}
	s := strings.TrimSpace(string(binText))
	s = strings.TrimPrefix(s, "0x")
	bytecode, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decode %s bytecode: %v", name, err)
	}
	return parsed, bytecode
}

// deployRaw deploys ready-to-deploy creation calldata. Returns deployed addr.
func deployRaw(
	t *testing.T,
	ctx context.Context,
	backend *simulated.Backend,
	ec simulated.Client,
	deployer *ecdsa.PrivateKey,
	creationCalldata []byte,
) common.Address {
	t.Helper()
	chainID, err := ec.ChainID(ctx)
	if err != nil {
		t.Fatalf("chainID: %v", err)
	}
	from := crypto.PubkeyToAddress(deployer.PublicKey)
	nonce, err := ec.PendingNonceAt(ctx, from)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	tx := types.NewContractCreation(nonce, big.NewInt(0), 5_000_000, big.NewInt(1_000_000_000), creationCalldata)
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), deployer)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := ec.SendTransaction(ctx, signed); err != nil {
		t.Fatalf("send: %v", err)
	}
	backend.Commit()
	addr := crypto.CreateAddress(from, nonce)
	code, err := ec.CodeAt(ctx, addr, nil)
	if err != nil {
		t.Fatalf("CodeAt: %v", err)
	}
	if len(code) == 0 {
		t.Fatalf("deployment produced no code (constructor reverted)")
	}
	return addr
}

// deployRegistry deploys MimirValidationRegistry. For permissionless mode,
// pass common.Address{} for both manager + slasher, and big.NewInt(0) for slashWad.
func deployRegistry(
	t *testing.T,
	ctx context.Context,
	backend *simulated.Backend,
	ec simulated.Client,
	deployer *ecdsa.PrivateKey,
	serviceManager, slasher common.Address,
	slashWad *big.Int,
) common.Address {
	t.Helper()
	parsedABI, bytecode := readContract(t, "MimirValidationRegistry")
	args, err := parsedABI.Pack("", serviceManager, slasher, slashWad)
	if err != nil {
		t.Fatalf("pack registry constructor: %v", err)
	}
	return deployRaw(t, ctx, backend, ec, deployer, append(bytecode, args...))
}

// deployMockServiceManager deploys the test ServiceManager (no constructor args).
func deployMockServiceManager(
	t *testing.T,
	ctx context.Context,
	backend *simulated.Backend,
	ec simulated.Client,
	deployer *ecdsa.PrivateKey,
) common.Address {
	t.Helper()
	_, bytecode := readContract(t, "MockServiceManager")
	return deployRaw(t, ctx, backend, ec, deployer, bytecode)
}

// deployMockSlasher deploys the test Slasher (no constructor args).
func deployMockSlasher(
	t *testing.T,
	ctx context.Context,
	backend *simulated.Backend,
	ec simulated.Client,
	deployer *ecdsa.PrivateKey,
) common.Address {
	t.Helper()
	_, bytecode := readContract(t, "MockSlasher")
	return deployRaw(t, ctx, backend, ec, deployer, bytecode)
}
