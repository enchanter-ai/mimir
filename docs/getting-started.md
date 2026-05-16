# Mimir — Getting Started

You have an MCP tool that returns results. You want every result to ship with a cryptographically-verifiable provenance receipt so downstream consumers can confirm what the tool actually did, who ran it, and what sources it used. This guide gets you from zero to a signed envelope in ~15 minutes.

---

## What you'll have at the end

A live local pipeline that:

1. Accepts a tool-call result via HTTP.
2. Scores it with Claude Sonnet 4.6 on a 5-axis × 8-assertion rubric.
3. Wraps the request + result + sources in a canonical envelope, signs it with Ed25519.
4. Returns the envelope, which any third party can verify against the issuer's published public key.

Optionally:

5. Anchors the envelope digest on Sepolia testnet (already deployed at [`0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117`](https://sepolia.etherscan.io/address/0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117)).

---

## Prerequisites

| What | Why | How |
|---|---|---|
| Go 1.22+ | Issuer + anchor services | <https://go.dev/dl/> |
| Node 22+ | Scoring service (TypeScript) | <https://nodejs.org/> |
| Python 3.11+ | Demo + adversarial vectors | already on most systems |
| `ANTHROPIC_API_KEY` *(optional)* | Real scoring instead of `MOCK_MODE` | <https://console.anthropic.com> |

You do NOT need Foundry, Anvil, AWS, an Ethereum wallet, or any subscription. Everything works locally on a clean clone.

---

## Step 1 — Clone and run the test trio

```bash
git clone git@github.com:enchanter-ai/mimir.git
cd mimir

# Issuer + KMS + canonicalize + MCP schema
(cd issuer && go test ./...)                              # all PASS

# Anchor (contract + EigenLayer wiring against simulated EVM)
(cd anchor/go && CGO_ENABLED=0 go test ./...)              # 12/12 PASS

# Independent Rust verifier round-trips against the Go issuer
(cd spec/reference-impl-rust && cargo test)                # 6/6 PASS

# 12 adversarial vectors — verifier must reject all
python spec/test-vectors-adversarial/verify-all.py         # 12/12 PASS
```

If all four pass, you have a working clone of the protocol stack.

---

## Step 2 — Run the end-to-end demo (no API key needed)

```bash
python demo.py
```

This:

1. Starts the scoring service on `:9090` with `MOCK_MODE=1` (returns a deterministic DEPLOY-tier stub).
2. Starts the Go issuer on `:8080`.
3. Sends a sample tool-call result through `POST /v1/score` → `POST /v1/attest`.
4. Fetches the issuer's JWK and verifies the envelope's Ed25519 signature with PyNaCl (a different cryptographic library than the one signing — independent verification).
5. Cleans up both subprocesses.

Expected last line:

```
[OK] SIGNATURE VERIFIED -- envelope is cryptographically valid
```

---

## Step 3 — Replace MOCK_MODE with real Claude scoring

If you have an `ANTHROPIC_API_KEY`:

```bash
# Start the scoring service against real Claude (in one terminal):
cd mimir/scoring
export ANTHROPIC_API_KEY=<your-key>
npx tsx src/server.ts &

# Start the issuer (in another):
cd mimir/issuer
ISSUER_PORT=8090 go run .

# Run the live POC:
cd mimir/scoring/calibration
python poc_translate.py
# → 5-axis scoring (~20s) → Ed25519 signing (~10ms) → external verify (~20ms)
# → "*** DEPLOY VERDICT achieved end-to-end with real Claude ***"
```

This produces real DEPLOY-tier envelopes from real Claude judgment. Cost: ~$0.05 of API credits per scored envelope.

For a full empirical calibration with confusion-matrix + threshold sweep, see [`scoring/calibration/calibration-report.md`](../scoring/calibration/calibration-report.md) (50-case set, 100% precision, 20% recall at σ < 0.75).

---

## Step 4 — Wrap your own MCP tool

The official Anthropic MCP Python SDK example is in [`tests/mcp/`](../tests/mcp/). Pattern:

```python
from mcp.server.fastmcp import FastMCP
import requests, json

mcp = FastMCP("my-attested-tool-server")

@mcp.tool()
def fetch_document(url: str, format: str = "text") -> str:
    # 1. Run the actual tool.
    result_text = my_fetch_impl(url, format)

    # 2. Wrap result + request in the MCP wire format the issuer expects.
    body = {
        "tool_id": "did:web:mycompany.com:tools:fetch-document",
        "tool_version": "1.0.0",
        "request": {
            "jsonrpc": "2.0", "id": 1, "method": "tools/call",
            "params": {"name": "fetch_document", "arguments": {"url": url, "format": format}},
        },
        "result": {
            "jsonrpc": "2.0", "id": 1,
            "result": {"content": [{"type": "text", "text": result_text}]},
        },
    }
    envelope = requests.post("http://localhost:8080/v1/attest-mcp", json=body).json()["envelope"]

    # 3. Return the result + the cryptographic receipt together.
    return json.dumps({"document": result_text, "envelope": envelope})

if __name__ == "__main__":
    mcp.run()
```

Any MCP client (Claude Desktop, Cursor, Cline, ...) that calls your tool now gets back a result PLUS a signed envelope. The client can independently verify the envelope using the issuer's JWK from `GET /v1/key`.

---

## Step 5 (optional) — Anchor envelopes on Sepolia

The contract is already deployed at [`0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117`](https://sepolia.etherscan.io/address/0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117). To anchor your own envelope digests:

```bash
# Generate a wallet (or use your existing one):
cd mimir/anchor/go
go run ./cmd/genkey

# Fund the address from a Sepolia faucet (https://sepolia-faucet.pk910.de).

# Anchor:
HOLESKY_RPC_URL=https://ethereum-sepolia.publicnode.com \
HOLESKY_PRIVATE_KEY=<hex> \
CONTRACT_ADDRESS=0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117 \
go run ./cmd/verify    # runs the full lifecycle: anchor → verify → revoke
```

Each `register` call is ~90k gas (~$0 on testnet). Anyone can later call `verify(digest)` on the contract to confirm the envelope was anchored by you.

For production: deploy your own contract in **AVS mode** wired to real EigenLayer Holesky core — see [`anchor/DEPLOY.md`](../anchor/DEPLOY.md).

---

## Step 6 — Switch to AWS KMS for production keys

In dev mode, the issuer uses an in-process Ed25519 keypair that dies when the process restarts. For production:

1. Provision an AWS KMS Ed25519 key (`KeySpec: ECC_NIST_EDWARDS25519`, `KeyUsage: SIGN_VERIFY`).
2. Grant your issuer's IAM role `kms:Sign` + `kms:GetPublicKey` on the key ARN.
3. Start the issuer with:
   ```
   KMS_MODE=aws
   KMS_KEY_ARN=arn:aws:kms:us-east-1:123456789012:key/...
   AWS_REGION=us-east-1
   ```

The HTTP API is identical. The published JWK `kid` becomes the KMS ARN instead of an ephemeral UUID, giving downstream consumers a stable identity to anchor trust against.

Wire-faithful tests for this path live in [`issuer/kms/aws_test.go`](../issuer/kms/aws_test.go) — they exercise the full AWS API surface without requiring real credentials.

---

## Where to go next

- **Verify the protocol on your own terms** — [`spec/spec.pdf`](../spec/spec.pdf) is the canonical specification (CC0). Write your own verifier in any language and run it against the live Sepolia contract.
- **Hook into MCP hosts** — the [`tests/mcp/`](../tests/mcp/) pattern works with Claude Desktop, Cursor, Cline. Register the MCP server with your favorite host and start emitting attested tool calls.
- **Audit the code** — [`AUDIT_PREP.md`](../AUDIT_PREP.md) is the engagement package for Trail of Bits / OpenZeppelin / Sigma Prime.
- **Track the roadmap** — [`ROADMAP.md`](../ROADMAP.md) lists the next 90 days (testnet → audit → mainnet → launch).
- **Get help** — [GitHub Discussions](https://github.com/enchanter-ai/mimir/discussions) for spec questions; `security@enchanter.ai` for vulnerabilities.

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| `ANTHROPIC_API_KEY environment variable is not set` | Export it before starting the scoring server, OR set `MOCK_MODE=1` for offline tests. |
| Demo prints `[FAIL] SIGNATURE INVALID` | Almost always indicates a canonical-form divergence between the Go signer and the Python verifier. File a bug — this would be a significant correctness issue. |
| Anchor tests fail with "no required module" | `cd anchor/go && CGO_ENABLED=0 go mod tidy` then re-run. |
| Rust verifier fails the round-trip test | Check the Go issuer started cleanly on the port the test expects. If it's a deterministic byte-level mismatch, file a bug. |
| Calibration probe API errors after a few calls | Anthropic rate-limited you. Wait a minute and retry, or lower `CALIB_CONCURRENCY`. |
