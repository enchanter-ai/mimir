// Package anchor provides a Go client for the MimirValidationRegistry
// ERC-8004 on-chain contract.
//
// It interacts with the contract via raw ABI encoding using go-ethereum's
// abi package — no generated go-bindings required, so the module builds
// without a local abigen installation.
//
// Usage:
//
//	c, err := anchor.New("http://127.0.0.1:8545", privKeyHex, contractAddr)
//	txHash, err := c.AnchorEnvelope(digest, expiry)
//	issuer, expiry, revoked, err := c.VerifyAnchor(digest)
package anchor

import (
	"context"
	"crypto/ecdsa"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

//go:embed abi/MimirValidationRegistry.json
var rawABI []byte

//go:embed abi/MimirValidationRegistry.bin
var rawBytecode []byte

// DeployBytecodeBytes returns the contract creation bytecode (decoded from hex).
// Compiled from contracts/MimirValidationRegistry.sol with solc 0.8.20.
// Regenerate with: cd anchor && node compile.js
func DeployBytecodeBytes() []byte {
	s := strings.TrimSpace(string(rawBytecode))
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("anchor: invalid embedded bytecode: " + err.Error())
	}
	return b
}

// RPCClient is the minimal method set Anchor uses from an Ethereum RPC client.
// Both *ethclient.Client and ethclient/simulated.Client satisfy this, so the
// same Client can target a real node OR an in-process simulated backend.
type RPCClient interface {
	ChainID(ctx context.Context) (*big.Int, error)
	PendingNonceAt(ctx context.Context, addr common.Address) (uint64, error)
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	EstimateGas(ctx context.Context, call ethereum.CallMsg) (uint64, error)
	CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
	CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error)
}

// Client is the on-chain anchor client.
type Client struct {
	ec       RPCClient
	abi      abi.ABI
	contract common.Address
	privKey  *ecdsa.PrivateKey
	chainID  *big.Int
	from     common.Address
}

// New constructs a Client.
//
//   - rpcURL      — HTTP/WS RPC endpoint (e.g., "http://127.0.0.1:8545")
//   - privKeyHex  — hex-encoded private key without "0x" prefix
//   - contractAddr — deployed MimirValidationRegistry address
func New(rpcURL, privKeyHex string, contractAddr common.Address) (*Client, error) {
	ec, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", rpcURL, err)
	}

	privKey, err := crypto.HexToECDSA(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	chainID, err := ec.ChainID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("get chain ID: %w", err)
	}

	parsedABI, err := parseABI()
	if err != nil {
		return nil, err
	}

	pub := privKey.Public().(*ecdsa.PublicKey)
	from := crypto.PubkeyToAddress(*pub)

	return &Client{
		ec:       ec,
		abi:      parsedABI,
		contract: contractAddr,
		privKey:  privKey,
		chainID:  chainID,
		from:     from,
	}, nil
}

// NewWithClient constructs a Client with a pre-dialled RPC client (useful in
// tests where the caller manages the connection lifecycle, or when targeting
// a simulated.Backend's in-process client).
func NewWithClient(
	ec RPCClient,
	privKeyHex string,
	contractAddr common.Address,
	chainID *big.Int,
) (*Client, error) {
	privKey, err := crypto.HexToECDSA(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	parsedABI, err := parseABI()
	if err != nil {
		return nil, err
	}

	pub := privKey.Public().(*ecdsa.PublicKey)
	from := crypto.PubkeyToAddress(*pub)

	return &Client{
		ec:       ec,
		abi:      parsedABI,
		contract: contractAddr,
		privKey:  privKey,
		chainID:  chainID,
		from:     from,
	}, nil
}

// AnchorEnvelope registers an envelope digest on-chain.
//
//   - envelopeDigest — keccak256 of the canonical RFC-8785 envelope bytes
//   - expiry         — unix timestamp; use 0 for no expiry
//
// Returns the transaction hash on success.
func (c *Client) AnchorEnvelope(ctx context.Context, envelopeDigest [32]byte, expiry uint64) (common.Hash, error) {
	issuer := c.from // the sending key is also the issuer

	data, err := c.abi.Pack("register", envelopeDigest, issuer, new(big.Int).SetUint64(expiry))
	if err != nil {
		return common.Hash{}, fmt.Errorf("pack register: %w", err)
	}

	return c.sendTx(ctx, data)
}

// RevokeAnchor submits a fraud proof that marks a digest as revoked.
//
// In the current local implementation the proof blob is stored as calldata
// only; in the EigenLayer AVS slice it will be forwarded to the slashing
// reporter.
func (c *Client) RevokeAnchor(ctx context.Context, envelopeDigest [32]byte, proof []byte) (common.Hash, error) {
	data, err := c.abi.Pack("revoke", envelopeDigest, proof)
	if err != nil {
		return common.Hash{}, fmt.Errorf("pack revoke: %w", err)
	}

	return c.sendTx(ctx, data)
}

// VerifyResult is the structured return value of VerifyAnchor.
type VerifyResult struct {
	Issuer  common.Address
	Expiry  uint64
	Revoked bool
}

// VerifyAnchor reads the on-chain entry for the given digest.
// Returns an error if the RPC call fails; returns zero-values (issuer
// == address(0)) when the digest has never been registered.
func (c *Client) VerifyAnchor(ctx context.Context, envelopeDigest [32]byte) (VerifyResult, error) {
	data, err := c.abi.Pack("verify", envelopeDigest)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("pack verify: %w", err)
	}

	msg := ethereum.CallMsg{To: &c.contract, Data: data}
	raw, err := c.ec.CallContract(ctx, msg, nil)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("call verify: %w", err)
	}

	out, err := c.abi.Unpack("verify", raw)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("unpack verify: %w", err)
	}
	if len(out) != 3 {
		return VerifyResult{}, fmt.Errorf("unexpected output length %d", len(out))
	}

	issuer := out[0].(common.Address)
	expiryBig := out[1].(*big.Int)
	revoked := out[2].(bool)

	return VerifyResult{
		Issuer:  issuer,
		Expiry:  expiryBig.Uint64(),
		Revoked: revoked,
	}, nil
}

// IsValid calls the contract's isValid() view function.
func (c *Client) IsValid(ctx context.Context, envelopeDigest [32]byte) (bool, error) {
	data, err := c.abi.Pack("isValid", envelopeDigest)
	if err != nil {
		return false, fmt.Errorf("pack isValid: %w", err)
	}

	msg := ethereum.CallMsg{To: &c.contract, Data: data}
	raw, err := c.ec.CallContract(ctx, msg, nil)
	if err != nil {
		return false, fmt.Errorf("call isValid: %w", err)
	}

	out, err := c.abi.Unpack("isValid", raw)
	if err != nil {
		return false, fmt.Errorf("unpack isValid: %w", err)
	}

	return out[0].(bool), nil
}

// WaitMined waits for a transaction to be mined, up to timeout.
func (c *Client) WaitMined(ctx context.Context, txHash common.Hash, timeout time.Duration) (*types.Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		receipt, err := c.ec.TransactionReceipt(ctx, txHash)
		if err == nil {
			return receipt, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for tx %s: %w", txHash, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Close releases the underlying RPC connection (only meaningful when backed
// by a real *ethclient.Client; no-op for the simulated client).
func (c *Client) Close() {
	if closer, ok := c.ec.(interface{ Close() }); ok {
		closer.Close()
	}
}

// -----------------------------------------------------------------------
// Deploy helper — used in tests to deploy the contract from bytecode.
// -----------------------------------------------------------------------

// DeployBytecode is the creation bytecode of MimirValidationRegistry as hex.
// Kept for callers that prefer string form; binary form via DeployBytecodeBytes().
var DeployBytecode = strings.TrimSpace(string(rawBytecode))

// TransactOpts returns a ready-to-use bind.TransactOpts for the client's key.
// Useful when callers need to send arbitrary transactions.
func (c *Client) TransactOpts(ctx context.Context) (*bind.TransactOpts, error) {
	nonce, err := c.ec.PendingNonceAt(ctx, c.from)
	if err != nil {
		return nil, fmt.Errorf("get nonce: %w", err)
	}
	tip, err := c.ec.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("suggest tip: %w", err)
	}
	head, err := c.ec.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("get head: %w", err)
	}
	baseFee := head.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(0)
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)

	opts, err := bind.NewKeyedTransactorWithChainID(c.privKey, c.chainID)
	if err != nil {
		return nil, fmt.Errorf("build transactor: %w", err)
	}
	opts.Context = ctx
	opts.Nonce = new(big.Int).SetUint64(nonce)
	opts.GasTipCap = tip
	opts.GasFeeCap = feeCap
	opts.GasLimit = 0 // let go-ethereum estimate
	return opts, nil
}

// -----------------------------------------------------------------------
// internal helpers
// -----------------------------------------------------------------------

func parseABI() (abi.ABI, error) {
	// The embedded JSON is a raw array; go-ethereum's abi.JSON() expects
	// the array directly.
	return abi.JSON(strings.NewReader(string(rawABI)))
}

func (c *Client) sendTx(ctx context.Context, data []byte) (common.Hash, error) {
	nonce, err := c.ec.PendingNonceAt(ctx, c.from)
	if err != nil {
		return common.Hash{}, fmt.Errorf("nonce: %w", err)
	}

	gasLimit, err := c.ec.EstimateGas(ctx, ethereum.CallMsg{
		From: c.from,
		To:   &c.contract,
		Data: data,
	})
	if err != nil {
		return common.Hash{}, fmt.Errorf("estimate gas: %w", err)
	}
	// Add 20 % buffer
	gasLimit = gasLimit * 12 / 10

	tip, err := c.ec.SuggestGasTipCap(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("suggest tip: %w", err)
	}

	head, err := c.ec.HeaderByNumber(ctx, nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("head: %w", err)
	}
	baseFee := head.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(0)
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   c.chainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       gasLimit,
		To:        &c.contract,
		Data:      data,
	})

	signer := types.LatestSignerForChainID(c.chainID)
	signed, err := types.SignTx(tx, signer, c.privKey)
	if err != nil {
		return common.Hash{}, fmt.Errorf("sign tx: %w", err)
	}

	if err := c.ec.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, fmt.Errorf("send tx: %w", err)
	}

	return signed.Hash(), nil
}

// validateABIJSON is used by tests to confirm the embedded ABI parses.
func validateABIJSON() error {
	var v interface{}
	return json.Unmarshal(rawABI, &v)
}
