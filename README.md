# Mimir — Verifiable Provenance for MCP Tool Calls

> *"Wisdom is preserved when its source can be cited and its claims verified."*

Mimir is the **MCP Tool-Call Provenance Oracle**: a service that signs cryptographically-verifiable receipts for every tool-call result an MCP agent produces. The receipts bind together the tool's identity, the request, the result, and the upstream sources — under one signature that any third party can verify without trusting Mimir.

A separate scoring service runs a 5-axis × 8-assertion σ-bound quality rubric backed by Claude Sonnet 4.6. An on-chain `MimirValidationRegistry` (Solidity, ERC-8004 shape) anchors envelope digests and routes accepted fraud proofs to an EigenLayer-style `ISlasher` to reduce the issuer's restaked allocation — economic security on top of cryptographic verifiability.

---

## 30-second example

```bash
git clone git@github.com:enchanter-ai/mimir.git && cd mimir
python demo.py    # MOCK_MODE — no API key required
# → [OK] SIGNATURE VERIFIED -- envelope is cryptographically valid
```

That single command starts both services, scores a sample tool call, signs an envelope, and externally verifies the Ed25519 signature using PyNaCl (an independent crypto library that doesn't share code with the signer). The verifying step proves: a third party with only the spec PDF, the envelope, and the issuer's published public key can confirm the envelope is authentic.

---

## What's in this repository

| Path | Purpose |
|---|---|
| [`spec/`](spec/) | The **MCP Tool-Call Provenance Envelope** specification — protocol-level Standards Track draft. CC0. Open [`spec/spec.pdf`](spec/spec.pdf) for the printed version. |
| [`spec/reference-impl-rust/`](spec/reference-impl-rust/) | **Independent Rust verifier** written from the spec alone. 6/6 tests pass, including a live round-trip against the Go issuer. This proves the spec is implementable without reading our Go code. |
| [`spec/reference-impl-ts/`](spec/reference-impl-ts/) | Reference **TypeScript SDK** for envelope production + verification. Embeddable into MCP clients/servers. |
| [`spec/test-vectors-adversarial/`](spec/test-vectors-adversarial/) | 12 attack vectors (signature tamper, replay-window, alg downgrade, key-id swap, ...). A correct verifier rejects all of them. |
| [`issuer/`](issuer/) | **Go HTTP issuer service.** Two endpoints: `/v1/attest` (internal shape) and `/v1/attest-mcp` (real MCP JSON-RPC 2.0 `tools/call`). KMS-backed signing with three backends: in-memory ephemeral (dev), mock (test), AWS KMS (production). |
| [`scoring/`](scoring/) | **TypeScript scoring service.** Routes tool-call results through Claude Sonnet 4.6 for a 5-axis × 8-assertion verdict (DEPLOY / HOLD / FAIL). Calibrated against a 50-case labeled set. |
| [`scoring/calibration/`](scoring/calibration/) | The labeled calibration set + the analysis that derived the production σ threshold. 100% precision, 20% recall at σ<0.75. |
| [`anchor/`](anchor/) | **On-chain settlement layer.** `MimirValidationRegistry` (Solidity ^0.8.20) anchors envelope digests + routes fraud proofs to EigenLayer slashing. 12/12 simulated-EVM tests pass. |
| [`bench/`](bench/) | Throughput + concurrency benchmark. **1500 RPS sustained, 0 races at 500-goroutine stress.** |
| [`tests/mcp/`](tests/mcp/) | End-to-end interop using the **official Anthropic MCP Python SDK**: a real MCP server calls the issuer; a real MCP client calls the server; PyNaCl verifies the envelope. |
| [`demo.py`](demo.py) | One-command end-to-end demo (MOCK_MODE — no API key needed). |
| [`architecture.md`](architecture.md) | System diagram + component map. |
| [`PRODUCTION_READINESS.md`](PRODUCTION_READINESS.md) | Audit doc: G1–G7 gap-closure evidence with reproduction commands. |
| [`AUDIT_PREP.md`](AUDIT_PREP.md) | Security-audit engagement package: threat model, attack surface, dep inventory, three audit tiers. |
| [`ROADMAP.md`](ROADMAP.md) | Where we are, next 90 days, next 12 months. |

---

## Run the test trio

These three commands cover the protocol layer end-to-end:

```bash
# 1. Issuer (Go): canonicalize + sign + KMS round-trip + MCP schema
(cd issuer && go test ./...)                           # all PASS

# 2. Anchor (Go): contract deploys + 7 permissionless + 5 AVS slashing tests
(cd anchor/go && CGO_ENABLED=0 go test ./...)           # 12/12 PASS

# 3. Independent Rust verifier vs live Go issuer
(cd spec/reference-impl-rust && cargo test)             # 6/6 PASS

# 4. Adversarial attack vectors
python spec/test-vectors-adversarial/verify-all.py      # 12/12 PASS
```

Total wall time on a modern laptop: **~3 minutes**.

---

## Run the live POC (requires `ANTHROPIC_API_KEY`)

```bash
export ANTHROPIC_API_KEY=<your-key>
(cd scoring && npx tsx src/server.ts &)
(cd issuer && go run . &)

# Real-Claude scoring → Ed25519 sign → external verify
python scoring/calibration/poc_translate.py
# → "*** DEPLOY VERDICT achieved end-to-end with real Claude ***"
```

50-case calibration (~$2.50 of credits, ~5 min wall time):

```bash
python scoring/calibration/run_calibration.py
python scoring/calibration/analyze_calibration.py
# → writes calibration-report.md
```

---

## What's novel

| Layer | What exists today | What Mimir adds |
|---|---|---|
| Identity | ERC-8004 attests agent existence | **Per-tool-call attestation at MCP message granularity** |
| Signing | JWT / COSE / JOSE sign metadata | **Bound request + result + sources under one signature** |
| Quality | Mira does multi-model factual consensus | **σ-bound dispersion check across 5 honest-numbers axes** |
| Economics | Trust the operator | **Restaked-stake slashing on σ-bound dispute replay** (EigenLayer) |
| Validation | One-step trust decision | **Three explicit levels:** well-formed → cryptographic → trust-anchored |
| Auth | Session identity is out of scope | **DPoP-anchored `ClientIdentityProof` extension** (spec; impl Q1 2027) |

See [`spec/spec.pdf`](spec/spec.pdf) §§ 1–5 for the full design surface and § 15 for the explicit threat model.

---

## Production status (2026-05-16)

| Gap | Status |
|---|---|
| G1 Independent verifier (interop)        | ✅ Rust verifier round-trips against Go issuer (6/6) |
| G2 Adversarial resistance                | ✅ 12/12 attack vectors rejected |
| G3 Key custody (AWS KMS)                 | ✅ Wire-faithful fake validates HTTP path; AWS provisioning external |
| G4 On-chain anchor + EigenLayer slashing | ✅ 12/12 simulated-EVM tests; Holesky deploy script ready |
| G5 MCP wire format                       | ✅ Official Anthropic MCP SDK round-trip end-to-end |
| G6 Throughput + concurrency              | ✅ 1500 RPS sustained, 0 races at 500-goroutine stress |
| G7 σ-bound calibration                   | ✅ 50-case labeled set: 100% precision, 20% recall |

Three operator steps remain to flip from "proven locally" to "live in production":

1. **AWS KMS provisioning + IAM policy** (~30 min)
2. **Holesky testnet deploy** (~15 min — scripts in [`anchor/cmd/`](anchor/go/cmd/); runbook at [`anchor/DEPLOY.md`](anchor/DEPLOY.md))
3. **MCP host registration** (Claude Desktop / Cursor / Cline — ~10 min per host)

Full evidence in [`PRODUCTION_READINESS.md`](PRODUCTION_READINESS.md).

---

## License

- **Spec** (everything in [`spec/`](spec/)): **CC0-1.0** — public domain.
- **Code** (everything else): **Apache-2.0**.

Both are designed so production deployments can build on Mimir without legal review at adoption time.

---

## Authored by

**Enchanter Labs.** [github.com/enchanter-ai](https://github.com/enchanter-ai).

For audit inquiries see [`AUDIT_PREP.md`](AUDIT_PREP.md). For roadmap discussion see [`ROADMAP.md`](ROADMAP.md). For security disclosures see `SECURITY.md` (in progress).
