# Mimir Anchor — Testnet / Mainnet Deploy Runbook

Operator playbook for deploying `MimirValidationRegistry` to a real chain.

The contract has been proven against go-ethereum's simulated EVM with 14/14 tests passing (7 permissionless + 5 EigenLayer-AVS slashing + 2 EigenLayer-adapter call-translation). This runbook covers the live-chain step. ~15 minutes once credentials are in hand.

---

## 1. Prerequisites

| What | How to get it |
|---|---|
| **Funded testnet wallet** | Holesky faucet: [holesky-faucet.pk910.de](https://holesky-faucet.pk910.de/) or [Alchemy Holesky faucet](https://www.alchemy.com/faucets/ethereum-holesky). Need ~0.01 ETH. |
| **RPC endpoint** | Free Holesky RPC: `https://ethereum-holesky.publicnode.com`, or [Alchemy](https://www.alchemy.com/), or [Infura](https://www.infura.io/). |
| **Go 1.22+** | [go.dev/dl](https://go.dev/dl/) |
| **Etherscan account** *(optional, for source verification)* | [etherscan.io/login](https://etherscan.io/login) |

You do **not** need Foundry, Anvil, or `solc` installed — the creation bytecode is already embedded in `anchor/go/anchor.go` (compiled with solc 0.8.20; see `anchor/compile.js` to regenerate after contract edits).

---

## 2. Deploy in permissionless mode (recommended first deploy)

This anchors the contract on Holesky in dev mode: anyone can `register` and `revoke`, no on-chain slashing. Lets you verify the deploy + ABI without involving EigenLayer.

```bash
cd mimir/anchor/go

export HOLESKY_RPC_URL=https://ethereum-holesky.publicnode.com
export HOLESKY_PRIVATE_KEY=<your-hex-private-key>

go run ./cmd/deploy
```

Expected output:

```
==== Mimir anchor deploy ====
  rpc           : https://ethereum-holesky.publicnode.com
  chain_id      : 17000
  deployer      : 0xYourWalletAddress...
  balance       : 0.012345 ETH
  mode          : PERMISSIONLESS
  ...
==== DEPLOY OK ====
  contract      : 0xDeployedContractAddress...
  block         : 5123456
  gas used      : ~700000
  etherscan     : https://holesky.etherscan.io/address/0xDeployed...
```

Save the contract address — you'll need it for the next step and for any downstream use.

---

## 3. Verify the deploy round-trips

Same dir, runs the full lifecycle (register → verify → isValid → revoke) against the live contract:

```bash
export CONTRACT_ADDRESS=0xDeployedContractAddress...

go run ./cmd/verify
```

Expected:

```
==== Mimir anchor verify ====
  contract  : 0xDeployedContractAddress...
  digest    : 0x<random-32-byte-hex>
  expiry    : <unix-timestamp + 24h>

  [step 1] AnchorEnvelope        OK   tx 0x...
  [step 2] VerifyAnchor          issuer=0xYourAddr expiry=<ts> revoked=false
  [step 3] IsValid               true
  [step 4] RevokeAnchor          OK   tx 0x...
                                 revoked now: true

==== VERIFY OK ====
```

Each tx will cost ~$0.01–$0.05 of testnet ETH (~50k gas at testnet gas prices).

---

## 4. Deploy in AVS mode (EigenLayer slashing wired live)

Once permissionless deploy works, switch to AVS mode pointing at real EigenLayer core contracts:

```bash
# Replace with the actual EigenLayer Holesky deployment addresses.
# As of 2026-Q1, find them at https://github.com/Layr-Labs/eigenlayer-contracts/blob/dev/script/output/holesky/eigenlayer_addresses.json
export SERVICE_MANAGER=0x<AVS ServiceManagerBase on Holesky>
export SLASHER=0x<EigenLayer AllocationManager on Holesky>
export SLASH_WAD=100000000000000000   # 1e17 = 10%; tune to your AVS risk profile

go run ./cmd/deploy
```

Then verify with **`SKIP_REVOKE=1`** — revocation in AVS mode requires the caller to be authorized at the real `AllocationManager`, which the deployer typically is not:

```bash
SKIP_REVOKE=1 CONTRACT_ADDRESS=0xDeployed... go run ./cmd/verify
```

This proves:
- The contract was deployed in AVS mode (`avsModeEnabled() == true`).
- `register()` succeeds for the deployer **only if** the deployer is a registered operator at the `ServiceManager`. If not, the call reverts with `NotAnOperator` — that's the expected behavior and the next step is to register your operator with EigenLayer (out of scope for this runbook; see [EigenLayer docs](https://docs.eigenlayer.xyz)).

---

## 5. Etherscan source verification (optional)

Once deployed, link the source so anyone can audit the on-chain bytecode against the GitHub source:

```bash
# Get the contract source as a single file (no imports — Mimir registry vendors all interfaces).
cd mimir/anchor
cat contracts/IEigenLayer.sol contracts/MimirValidationRegistry.sol > /tmp/mimir-flat.sol

# Then on Holesky Etherscan:
#   Contract → Verify and Publish → "Solidity (Single file)"
#   Compiler: 0.8.20+commit.<exact-hash>
#   Optimization: Yes, runs=200
#   License: MIT
#   Paste /tmp/mimir-flat.sol
```

The `compile.js` script uses `runs=200` and `solc 0.8.20`.

---

## 6. Mainnet deploy (after audit)

**Do not deploy to Ethereum mainnet before passing a smart-contract audit.** See [`AUDIT_PREP.md`](../AUDIT_PREP.md) for the engagement scope.

When ready, the same commands work with:

```bash
export HOLESKY_RPC_URL=https://eth-mainnet.g.alchemy.com/v2/<key>
export HOLESKY_PRIVATE_KEY=<mainnet-wallet>
# Chain ID will be 1; the script auto-detects and links to etherscan.io.
```

Mainnet costs: ~$5–50 in real ETH per deploy depending on gas prices.

---

## 7. Troubleshooting

| Symptom | Likely cause + fix |
|---|---|
| `parse HOLESKY_PRIVATE_KEY: ...` | Key has 0x prefix — script strips it, but make sure it's 64 hex chars (32 bytes). Common mistake: pasting an address instead of a private key. |
| `deployer balance is zero` | Wallet not funded yet. Get Holesky ETH from a faucet (see § 1). |
| `deployment reverted` | Constructor reverted. Most likely cause: SERVICE_MANAGER set but SLASHER not (or vice versa). They must both be set or both unset. |
| `receipt timeout after 5m0s` | Public RPC is slow or your tx was dropped. Try a different RPC endpoint, or bump the gas tip via [`SuggestGasTipCap`](https://github.com/ethereum/go-ethereum/blob/master/ethclient/ethclient.go). |
| `NotAnOperator` revert on `register()` (AVS mode) | Deployer is not registered as an operator at the configured `ServiceManager`. This is the expected guardrail. Register the operator via EigenLayer core, or test in PERMISSIONLESS mode first. |
| `IssuerMustBeCaller` revert on `register()` (AVS mode) | Trying to anchor on behalf of someone else. The `issuer` parameter must equal `msg.sender` in AVS mode (anti-spoofing). |

---

## 8. After deploy

- [ ] Save the contract address in your secret store; do not embed in client-side code without a deploy-time review.
- [ ] Etherscan source-verify (optional but improves trust posture).
- [ ] Add the deployed address to [`docs/deployments.md`](../docs/deployments.md) in the Mimir repo so downstream consumers can find it.
- [ ] If running AVS mode, register your operator with EigenLayer + delegate stake.
- [ ] Set up monitoring: watch `Revoked` and `SlashTriggered` events on the contract; they're the canary signal for any envelope fraud claim against your issuer.
