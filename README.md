# Mimir

## TL;DR

**Verifiable provenance for MCP tool-call results.** Mimir signs a cryptographic receipt for every tool-call result an MCP agent produces — binding together the tool's identity, the request, the result, and the upstream sources under one Ed25519 signature. A separate σ-bound scoring service runs the result through Claude Sonnet 4.6 across 5 quality axes and 8 SAT assertions. An on-chain `MimirValidationRegistry` (ERC-8004 shape, EigenLayer-aware) anchors envelope digests + routes accepted fraud proofs to a slasher that reduces the issuer's restaked allocation. Live on Sepolia today.

## Origin

Mimir grew out of a deep-research arc that asked *"what could give big impact in the agent world that doesn't yet exist?"* — the answer that survived three independent LLM reviewer passes (GPT-5.5, Opus 4.7, Gemini 3) and an MVP prototype was: **per-tool-call provenance with economic-security teeth.** Today's stack solves "I attest that this agent exists" (ERC-8004) and "I sign this JWT" (JOSE/COSE), but no public standard binds *request + result + sources* under one signature at the MCP-message layer, with on-chain slashing if the receipt later turns out to be wrong.

Named after the Norse well of wisdom whose price was Odin's eye: knowledge worth preserving has to cost something to fabricate.

## Who this is for

- **MCP server authors** who want their tool outputs to carry a cryptographic guarantee a third party can verify without trusting the server.
- **MCP clients / agents** (Claude Desktop, Cursor, Cline) that want to surface provenance metadata to users + downstream consumers.
- **Restaking / AVS operators** who want a turnkey on-chain anchor + slashing target for off-chain attestations.
- **Spec implementers** — anyone writing an independent verifier from the published Standards Track draft.
- **Auditors** evaluating the cryptographic + economic-security claims of an agent provenance system.

## Contents

| Path | Purpose |
|---|---|
| [`spec/`](spec/) | The **MCP Tool-Call Provenance Envelope** specification — v2.1 Standards Track draft (CC0). [`spec/spec.pdf`](spec/spec.pdf) is the rendered PDF. |
| [`spec/reference-impl-rust/`](spec/reference-impl-rust/) | **Independent Rust verifier** written from the spec alone — proves the spec is implementable without reading the Go code. |
| [`spec/reference-impl-ts/`](spec/reference-impl-ts/) | **TypeScript SDK** for envelope production + verification, embeddable into MCP clients/servers. |
| [`spec/test-vectors-adversarial/`](spec/test-vectors-adversarial/) | **15 adversarial test vectors** (signature tampering, replay-window, alg downgrade, field-tampering on every signed field). |
| [`issuer/`](issuer/) | **Go HTTP issuer** with three KMS backends (ephemeral / mock / AWS KMS), DPoP ClientIdentityProof, JWK rotation, per-IP rate-limit, structured tracer. |
| [`scoring/`](scoring/) | **TypeScript scoring service** routing tool-call results through Claude Sonnet 4.6 on a 5-axis × 8-assertion σ-bound rubric. |
| [`scoring/calibration/`](scoring/calibration/) | 50-case labeled calibration set — empirical proof that the rubric discriminates quality at 100% precision. |
| [`anchor/`](anchor/) | **On-chain settlement** — `MimirValidationRegistry` (ERC-8004 shape) + `EigenLayerSlasherAdapter` + mocks. Live on Sepolia. |
| [`bench/`](bench/) | Throughput + concurrency benchmark — 1500 RPS sustained, 0 races at 500-goroutine stress. |
| [`tests/mcp/`](tests/mcp/) | End-to-end interop using the official Anthropic MCP SDK. |
| [`examples/mcp-server-starter/`](examples/mcp-server-starter/) | 60-line starter template — fork this for your first Mimir-attested MCP tool. |
| [`deploy/aws-kms/`](deploy/aws-kms/) | Terraform + shell scripts for AWS KMS provisioning. |
| [`demo.py`](demo.py) | One-command end-to-end demo (MOCK_MODE, no credentials needed). |

## The Numbers

| | |
|---|---|
| Spec sections | 26 |
| Spec adversarial test vectors | **15/15 PASS** |
| Issuer Go test suite | 8 packages, all green |
| Anchor Go test suite (simulated EVM) | **14/14 PASS** |
| Rust verifier (independent impl) | **6/6 PASS**, including live round-trip against the Go issuer |
| Calibration set | **50 cases**, **100% precision** (0/23 bad cases reached DEPLOY), 20% recall |
| Throughput | **1500 RPS sustained**, 0 races at 500 goroutines |
| AWS KMS sign latency | **97–112 ms p50** (Ed25519 in eu-west-1, end-to-end including HTTP) |
| Live deployments on Sepolia | 4 contracts (permissionless + AVS + EigenLayer adapter stack) |
| GitHub Actions CI | **6/6 jobs green every push** |
| Commits on `main` | 133+ |
| Release tags | v0.1.0 (initial) · v0.1.1 (EigenLayer adapter) |

## How It Works

```
                     ┌─────────────────────────────────────────────────────────────┐
                     │   MCP client (Claude Desktop / Cursor / Cline / API agent)  │
                     └──────────────────────────┬──────────────────────────────────┘
                                                │ JSON-RPC tools/call
                                                ▼
                     ┌─────────────────────────────────────────────────────────────┐
                     │   Your MCP server (forked from examples/mcp-server-starter) │
                     └─────────┬──────────────────────────────────────┬────────────┘
                               │                                      │
                               │ POST /v1/score                       │ POST /v1/attest-mcp
                               ▼                                      ▼
                     ┌────────────────────┐                ┌────────────────────────┐
                     │  Mimir scoring     │                │   Mimir issuer (Go)    │
                     │  (TypeScript)      │                │                        │
                     │  Claude Sonnet 4.6 │                │  Canonicalize (8785)   │
                     │  5-axis σ-bound    │                │  Digest req + result   │
                     │  8 SAT assertions  │                │  Sign Ed25519 via KMS  │
                     └────────────────────┘                └──────────┬─────────────┘
                                                                     │
                                                                     │ envelope
                                                                     ▼
                                            ┌──────────────────────────────────────┐
                                            │   Independent verifier (Rust / Go /  │
                                            │   PyNaCl) recomputes canonical form  │
                                            │   and checks signature against JWK   │
                                            └────────────────┬─────────────────────┘
                                                             │ result_digest
                                                             ▼ (optional)
                                            ┌──────────────────────────────────────┐
                                            │  MimirValidationRegistry on-chain    │
                                            │  ERC-8004 shape, EigenLayer slashing │
                                            │  via EigenLayerSlasherAdapter        │
                                            └──────────────────────────────────────┘
```

Three validation levels per spec § 12:
1. **Syntactically well-formed** — JSON parses, required fields present.
2. **Cryptographically valid** — Ed25519 signature verifies against the published JWK.
3. **Trust-anchored** — DPoP `ClientIdentityProof` extension proves who actually invoked the tool (spec § 6.11).

## What Makes Mimir Different

### One signature binds request + result + sources

Every existing standard signs only metadata: JWT signs the claims, COSE signs the header + payload, ERC-8004 signs the agent's identity. **Mimir signs the whole tool-call context under one canonical form** — RFC 8785 JCS over `(tool_id, tool_version, invoked_at, invoked_by, request_digest, result_digest, sources[])`. Any tampered field breaks the signature, and the 15-vector adversarial suite proves every signed field is bound (`tool_id`, `invoked_by`, `sources`, the digests, the timestamp, all of them).

### σ-bound dispersion check across honest-numbers axes

Existing quality-scoring systems return a single number; Mimir's scoring rubric returns 5 axes (clarity, specificity, faithfulness, safety, structure) + 8 SAT assertions and only emits a DEPLOY verdict when *σ across content axes < 0.75 AND overall ≥ 9.0 AND every axis ≥ 7.0 AND 8/8 assertions pass*. The empirical calibration set (50 hand-labeled cases) shows **100% precision** — every case the rubric DEPLOY'd was genuinely good, zero false positives across 23 deliberately-bad cases including hallucination, sycophancy, evasion, incompleteness, and format-mismatch.

### Restaked-stake slashing on σ-bound dispute replay

`MimirValidationRegistry` anchors envelope digests on-chain. Anyone who can produce a fraud proof for a registered envelope calls `revoke(digest, proof)`. In AVS mode this fires `slasher.slash(operator, wadSlashed, reasonHash)` — and via the `EigenLayerSlasherAdapter`, that becomes a real EigenLayer v2 `AllocationManager.slash(SlashingParams)` call that reduces the issuer's restaked allocation. **The economic-security claim is not narrative — the call path is proven live on Sepolia with 4/4 `SlashingParams` field assertions reading back correctly from on-chain state.**

### Three explicit validation levels, not one trust decision

Most attestation systems force a binary: trust the issuer or don't. Mimir distinguishes well-formed (anyone can check the JSON shape) from cryptographically-valid (signature checks out against the published key) from trust-anchored (DPoP proves the actual client invoked the tool). A consumer reading an envelope decides which level its threat model requires.

### Restaking-primitive agnostic via adapter pattern

Mimir's `ISlasher` interface stays narrow: `slash(operator, wadSlashed, reasonHash)`. Operators bridge to whichever restaking primitive they use — EigenLayer today (via `EigenLayerSlasherAdapter`), Symbiotic / Karak / Babylon tomorrow (via parallel adapters). The Mimir registry contract itself doesn't change; the adapter handles the primitive-specific ABI.

### Honest-numbers contract through-and-through

Every claim in this README is backed by a runnable test or a live on-chain artifact. No marketing math: the σ-bound bar of 0.75 was empirically calibrated against a 50-case labeled set (not picked because it sounded good); the 1500 RPS throughput was measured under a 500-goroutine concurrency stress test; the live AWS KMS latency of 97–112 ms p50 was measured against the actual production-shape KMS key in eu-west-1. If a number is in this repo, you can reproduce it.

## The Full Lifecycle

1. **A user invokes an MCP tool via Claude Desktop / Cursor / Cline.** The MCP client speaks JSON-RPC 2.0 `tools/call` to a registered server.
2. **Your MCP server runs the tool** and gets the result.
3. **The server POSTs (request, result) to Mimir's scoring service.** Claude Sonnet 4.6 scores the result across 5 axes + 8 assertions and returns a verdict (DEPLOY / HOLD / FAIL) with σ + per-axis breakdowns.
4. **If verdict is DEPLOY, the server POSTs to the issuer's `/v1/attest-mcp`.** The issuer canonicalizes the request + result per RFC 8785, computes SHA-256 digests, assembles the envelope, and signs it with Ed25519 via AWS KMS (or an in-process key in dev mode).
5. **The server returns the result + the signed envelope to the MCP client.** The client sees both — it can use the result as normal, and any downstream consumer can verify the envelope.
6. **(Optional) Anchor on-chain.** The server (or any downstream party) calls `MimirValidationRegistry.register(digest, issuer, expiry)`. The envelope's `result_digest` is now globally referenceable.
7. **(Optional, fraud dispute) Revoke.** Anyone who can prove the envelope was fraudulent calls `revoke(digest, proof)`. In AVS mode this fires the slasher, reducing the issuer's restaked allocation by the configured `slashWad` (default 10%).
8. **(Future) Cross-implementation verify.** A Rust / browser-JS / Java verifier built from the spec PDF alone can independently recompute the canonical form and check the signature. The 6/6 Rust round-trip test today is proof this works.

## Install

Choose one of three paths depending on what you're doing:

### Just trying it out (no install)

```bash
git clone git@github.com:enchanter-ai/mimir.git
cd mimir
python demo.py    # MOCK_MODE — no API key, no AWS, no chain — runs in ~30 sec
```

Produces `[OK] SIGNATURE VERIFIED -- envelope is cryptographically valid` at the end.

### Forking the starter into your real product

```bash
git clone git@github.com:enchanter-ai/mimir.git
cp -r mimir/examples/mcp-server-starter ./my-attested-tool
cd my-attested-tool
# Edit server.py — replace the `expensive_lookup` body with your real tool
# Register the server in claude_desktop_config.json — see docs/integrate-claude-desktop.md
```

### Running a production issuer

```bash
# 1. Provision AWS KMS:
cd mimir/deploy/aws-kms
terraform init && terraform apply -var='environment=prod'

# 2. Build + run the issuer Docker image:
cd ..
docker build -f issuer/Dockerfile -t mimir-issuer:prod .
docker run --rm -p 8080:8080 \
  -e KMS_MODE=aws \
  -e KMS_KEY_ARN=$(terraform -chdir=deploy/aws-kms output -raw kms_key_arn) \
  -e AWS_REGION=eu-west-1 \
  mimir-issuer:prod

# 3. (Optional) Deploy the on-chain anchor:
cd anchor/go
HOLESKY_RPC_URL=https://ethereum-sepolia.publicnode.com \
HOLESKY_PRIVATE_KEY=<hex> \
  go run ./cmd/deploy-eigenlayer
```

See [`deploy/aws-kms/README.md`](deploy/aws-kms/README.md) and [`anchor/DEPLOY.md`](anchor/DEPLOY.md) for the full production walkthroughs.

## Quickstart

Run the **test trio** to confirm everything works on your machine:

```bash
# 1. Issuer (Go): canonicalize + sign + KMS + MCP schema
(cd issuer && go test ./...)                              # all PASS

# 2. Anchor (Go): contract deploys + 14/14 simulated-EVM tests
(cd anchor/go && CGO_ENABLED=0 go test ./...)             # 14/14 PASS

# 3. Independent Rust verifier vs live Go issuer
(cd spec/reference-impl-rust && cargo test)               # 6/6 PASS

# 4. 15 adversarial vectors — verifier must reject 14, accept 1 (whitespace canon)
python spec/test-vectors-adversarial/verify-all.py        # 15/15 PASS

# 5. End-to-end demo (MOCK_MODE — no credentials)
python demo.py                                            # [OK] SIGNATURE VERIFIED
```

Total wall time on a modern laptop: **~3 minutes**. If any of those fail, the issue is reproducible from a clean clone — open an issue with the failing command.

For a live POC against real Claude Sonnet 4.6 scoring (requires `ANTHROPIC_API_KEY` in `.env`):

```bash
cd mimir
(cd scoring && npx tsx src/server.ts &)
(cd issuer && go run . &)

python scoring/calibration/poc_translate.py
# → real DEPLOY verdict from real Claude + real Ed25519 signature + real external verify
```

50-case calibration probe (~$2.50 of Claude credits, ~5 min wall time):

```bash
python scoring/calibration/run_calibration.py
python scoring/calibration/analyze_calibration.py
# → writes scoring/calibration/calibration-report.md
```

---

## License

- **Spec** (everything in [`spec/`](spec/)): **CC0-1.0** — public domain.
- **Code** (everything else): **Apache-2.0**.

Both are designed so production deployments can build on Mimir without legal review at adoption time.

## Authored by

**[Enchanter Labs](https://github.com/enchanter-ai).** For audit inquiries see [`AUDIT_PREP.md`](AUDIT_PREP.md). For roadmap discussion see [`ROADMAP.md`](ROADMAP.md). For security disclosures see [`SECURITY.md`](SECURITY.md).
