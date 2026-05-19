# Mimir — Live Deployments

Public registry of deployed `MimirValidationRegistry` contracts.

Each entry is verifiable by anyone: visit the Etherscan link, confirm the bytecode matches what `node anchor/compile.js` produces at the listed commit SHA.

---

## AWS KMS — production signing keys

### prod / eu-west-1 — Ed25519 — 2026-05-19

| Field | Value |
|---|---|
| **Key ARN** | `arn:aws:kms:eu-west-1:256097482700:key/a5ae51a1-006d-4c72-9546-321a1c690452` |
| **Alias** | `alias/mimir-issuer-prod` |
| **Region** | `eu-west-1` (Ireland) |
| **Key spec** | `ECC_NIST_EDWARDS25519` (Ed25519) |
| **Key usage** | `SIGN_VERIFY` |
| **Signing algorithm** | `ED25519_SHA_512` |
| **Rotation** | Disabled (Mimir rotates via JWK Set; see [`scripts/rotate-key.py`](../scripts/rotate-key.py)) |
| **IAM role** | `arn:aws:iam::256097482700:role/mimir-issuer-prod` |
| **IAM policy** | `arn:aws:iam::256097482700:policy/mimir-issuer-kms-use-prod` (kms:Sign + kms:GetPublicKey + kms:DescribeKey, scoped to this key only) |
| **Account** | `256097482700` |
| **Provisioned by** | aws-cli (2026-05-19) — matches the Terraform module shape in [`deploy/aws-kms/main.tf`](../deploy/aws-kms/main.tf) |

**Measured performance** (end-to-end including HTTP + KMS round-trip from outside eu-west-1):
- Cold start: ~308 ms
- Steady state: 97–112 ms p50
- External Ed25519 verify (PyNaCl, local): 15 ms

**Smoke test confirmed**:
- Issuer started with `KMS_MODE=aws KMS_KEY_ARN=... AWS_REGION=eu-west-1`
- `GET /v1/key` returns the KMS ARN as `kid`, raw 32-byte pub key as `x`
- `POST /v1/attest` produces an envelope whose signature externally verifies
  against the published JWK with PyNaCl (independent crypto library)
- 4/4 attest calls returned 200 with valid signatures

---

## Sepolia testnet (chain_id 11155111)

### v0.1.0 — Permissionless mode (smoke test)

| Field | Value |
|---|---|
| **Contract** | [`0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117`](https://sepolia.etherscan.io/address/0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117) |
| **Mode** | PERMISSIONLESS (`avsModeEnabled() == false`) |
| **Source-verified** | ✅ Etherscan ([code tab](https://sepolia.etherscan.io/address/0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117#code)) — solc 0.8.20+commit.a1b79de6, optimizer runs=200, MIT |
| **Deployer** | `0xC777261913DcB9a64C8fC82c392d8B26B97BcB0E` |
| **Deploy tx** | [`0x0bbdc55…6ac2`](https://sepolia.etherscan.io/tx/0x0bbdc5528575b944179e3fc37007ad48111b407de45d067c8be96b809fc86ac2) |
| **Deploy block** | 10863180 |
| **Deploy date** | 2026-05-16 |
| **Bytecode size** | 3055 bytes |
| **Deploy gas** | 717252 |
| **Service manager** | `0x0` (permissionless) |
| **Slasher** | `0x0` (permissionless) |
| **Source commit** | TBD (will fill on the docs PR that lands this file) |
| **Compiler** | solc 0.8.20, optimizer runs=200, no external imports |

#### Verified round-trip

The post-deploy verification script ran the full lifecycle against this contract on 2026-05-16:

| Step | Transaction | Block | Gas | Outcome |
|---|---|---|---|---|
| AnchorEnvelope | [`0xab5ddff…c9fa`](https://sepolia.etherscan.io/tx/0xab5ddff141f532a45eae3ce82ef99b47b8721927fd8a3e1ac7f419e071fec9fa) | 10863182 | 91313 | OK |
| RevokeAnchor (fraud proof) | [`0x32bee1f…027a`](https://sepolia.etherscan.io/tx/0x32bee1fb5ec6681ee108f6f8d0eedfbdff4ec16f47a6c80d81a43bc3dcd9027a) | 10863183 | 32358 | OK |

**Confirmed:** register → verify returns the issuer + expiry → `IsValid` true → revoke → `IsValid` false.

#### How to use this deployment

```go
import anchor "github.com/enchanter-ai/mimir/anchor"

c, _ := anchor.New(
    "https://ethereum-sepolia.publicnode.com",
    privKeyHex,
    common.HexToAddress("0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117"),
)
txHash, _ := c.AnchorEnvelope(ctx, envelopeDigest, expiry)
```

Anyone with a funded Sepolia wallet can `register` / `revoke` against this contract — it is permissionless by design (no operator gating, no slashing). Use it for SDK testing and protocol-shape validation.

---

### v0.1.0 — AVS mode — 2026-05-19

Second instance on Sepolia, deployed in AVS mode (operator-gated `register`, slashing-on-revoke). Wired to mock EigenLayer contracts deployed alongside:

| Contract | Address |
|---|---|
| **MimirValidationRegistry (AVS)** | [`0xF67f9A7574883a2BDd1841004eC2cc189D616F8a`](https://sepolia.etherscan.io/address/0xF67f9A7574883a2BDd1841004eC2cc189D616F8a) |
| **MockServiceManager** | [`0x8292921a02936fc6F16A95151b6AEE4375B2222F`](https://sepolia.etherscan.io/address/0x8292921a02936fc6F16A95151b6AEE4375B2222F) |
| **MockSlasher** | [`0xb65E2e62692243f2c2C20fe02438F0406206d5Eb`](https://sepolia.etherscan.io/address/0xb65E2e62692243f2c2C20fe02438F0406206d5Eb) |

**Mode:** AVS (`avsModeEnabled() == true`)
**slashWad:** `1e17` (= 10% of allocation per accepted fraud proof)
**Deploy date:** 2026-05-19
**Deployer:** `0xC777261913DcB9a64C8fC82c392d8B26B97BcB0E`

#### 7/7 lifecycle confirmed on-chain

Ran [`anchor/go/cmd/verify-avs`](../anchor/go/cmd/verify-avs/main.go) against the live deployment:

| Step | Outcome |
|---|---|
| `MockServiceManager.registerOperator(caller)` | block 10880219, 45k gas, OK |
| `AnchorEnvelope(random_digest, expiry)` (now operator-gated) | block 10880220, 96k gas, OK |
| `VerifyAnchor(digest)` reads back issuer + expiry; revoked=false | view call OK |
| `IsValid(digest)` returns true | view call OK |
| `RevokeAnchor(digest, proof)` flips revoked AND fires `Slasher.slash()` | block 10880221, 62k gas, OK |
| `MockSlasher.totalSlashed(caller)` returns 1e17 (matches slashWad) | view call OK |
| Re-verify: revoked=true, IsValid=false | view calls OK |

**What this proves:** the full AVS-mode contract wiring works on a real Ethereum-equivalent chain — operator gating, slashing trigger, slash-event recording, post-revoke state transitions all behave as the simulated-EVM tests predicted.

**What this does NOT prove:** integration with real EigenLayer. The `IEigenLayer.sol` shape we ship is a narrow Mimir-side abstraction; real EigenLayer v2 `AllocationManager.slash()` takes a `SlashingParams` struct with a different ABI.

**Real-EigenLayer alignment lands here as an adapter pattern (v0.1.1+):**
- [`anchor/contracts/EigenLayerSlasherAdapter.sol`](../anchor/contracts/EigenLayerSlasherAdapter.sol) — thin Solidity contract that implements Mimir's `ISlasher` and forwards each slash to a real EigenLayer v2 `AllocationManager` via the canonical `SlashingParams` struct. Operators deploy this alongside the Mimir registry pointing at the AllocationManager for their target network (mainnet / Hoodi).
- [`anchor/contracts/MockAllocationManager.sol`](../anchor/contracts/MockAllocationManager.sol) — exact-shape mock for tests.
- [`anchor/go/eigenlayer_adapter_test.go`](../anchor/go/eigenlayer_adapter_test.go) — 4/4 PASS: confirms the adapter packs `SlashingParams` correctly (right operator, operatorSetId, per-strategy wads, hex-encoded reasonHash in description).

To wire Mimir against real EigenLayer:
1. Deploy `EigenLayerSlasherAdapter` with `_allocationManager = <real AllocationManager address>`, `_operatorSetId = <your AVS's set id>`, `strategies = [<your strategy addresses>]`
2. Deploy `MimirValidationRegistry` with `_slasher = <adapter address>`
3. Register the adapter as authorized to slash at the real `AllocationManager` (EigenLayer operator-registration flow)

The Mimir registry itself stays unchanged — same `ISlasher` interface, swap the implementation.

---

## Holesky testnet

**Holesky deprecated by the Ethereum Foundation (2025).** All public RPCs returned 0/5 working in our probe; Alchemy still routes the Holesky subdomain but the EF-recommended successor is **Hoodi** (launched 2025, positioned as EigenLayer-friendly). The Sepolia AVS deploy above serves the same "AVS-mode lifecycle works on a real chain" demonstration without depending on a deprecated network.

---

## Ethereum mainnet

*Not deployed. **Do not deploy to mainnet without an audit.** See [`AUDIT_PREP.md`](../AUDIT_PREP.md).*

---

## Verifying any of these yourself

```bash
git clone git@github.com:enchanter-ai/mimir.git
cd mimir/anchor

# Recompile contract bytecode from source (deterministic).
npm install --no-save solc@0.8.20
node compile.js
# This writes anchor/go/abi/MimirValidationRegistry.bin.

# Pull the deployed bytecode from chain.
cd go
HOLESKY_RPC_URL=https://ethereum-sepolia.publicnode.com \
  go run ./cmd/verify
# (Set CONTRACT_ADDRESS + HOLESKY_PRIVATE_KEY env vars first.)
```

If the bytecode at the listed Etherscan address matches what `compile.js` produces from the source at the same commit, the deployment is genuine and corresponds to the auditable source code.
