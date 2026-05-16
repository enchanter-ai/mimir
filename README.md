# Mimir — Verifiable Provenance for MCP Tool Calls

> *"Wisdom is preserved when its source can be cited and its claims verified."*

Mimir is the **MCP Tool-Call Provenance Oracle**: a service that signs cryptographically-verifiable receipts for every tool-call result an MCP agent produces. The receipts bind together the tool's identity, the request, the result content, and the upstream sources — under one signature that any third party can verify without trusting Mimir.

## What's in this repository

| Path | Purpose |
|---|---|
| [`spec/`](./spec) | The **MCP Tool-Call Provenance Envelope** specification — protocol-level Standards Track draft, the contract every implementation conforms to. Open [`spec/spec.pdf`](./spec/spec.pdf) for the printed version. |
| [`issuer/`](./issuer) | The **Mimir issuer service** — a Go HTTP server that constructs and signs provenance envelopes per the spec. MVP-grade. |
| [`scoring/`](./scoring) | The **scoring engine** — a TypeScript service that runs the Enchanter Labs convergence framework (5-axis × 8-assertion × σ-bound) and produces structured verdicts. Calls Claude Sonnet 4.6; ships with `MOCK_MODE=1` for offline demos. |
| [`spec/reference-impl-ts/`](./spec/reference-impl-ts) | The **reference TypeScript SDK** for envelope production and verification. Independent of the issuer service; intended for direct embedding in MCP clients and servers. |
| [`architecture.md`](./architecture.md) | The **Mimir AVS architecture** — system components, data flow, threat model, scaling targets, on-chain interaction model. |
| [`docs/stack-decision.md`](./docs/stack-decision.md) | Production tech-stack choices (Go + TypeScript + AWS KMS + Postgres + Redpanda + EigenLayer) with rationale per layer. |
| [`docs/hardening-roadmap.md`](./docs/hardening-roadmap.md) | The path from MVP to production: 17 gaps with severity, effort, and acceptance criteria. |
| [`demo.py`](./demo.py) | End-to-end demo — starts both services, runs a sample `tools/call` through scoring + signing, verifies the resulting signature with an external Ed25519 library. |

## Quickstart — run the end-to-end demo

```bash
python demo.py
```

The demo starts both services locally, runs one tool-call through the pipeline (scoring with `MOCK_MODE=1`, then signing), and verifies the resulting Ed25519 signature externally. Expected output ends with:

```
[OK] SIGNATURE VERIFIED -- envelope is cryptographically valid
```

## What's novel

| Layer | What exists today | What Mimir adds |
|---|---|---|
| Identity | ERC-8004 attests agent existence | Per-tool-call attestation at MCP message granularity |
| Signing | JWT / COSE / JOSE sign metadata | Bound request + result + sources under one signature |
| Quality | Mira does multi-model factual consensus | σ-bound dispersion check across 5 honest-numbers axes |
| Economics | Trust the operator | Restaked-stake slashing on σ-bound dispute replay |
| Validation | One-step trust decision | Three explicit levels (well-formed → cryptographic → trust-anchored) |
| Auth | Session identity is out of scope | DPoP-anchored `ClientIdentityProof` extension |
| Behavioral | MALT is a research dataset | MALT runs as a production monitor over the attestation stream |

See [`spec/spec.pdf`](./spec/spec.pdf) §§ 1–5 for the full design surface; § 15 for the explicit threat model.

## Status

| Component | Status |
|---|---|
| Spec | v2.1 Draft. Three independent reviews applied; all defects accepted. |
| Issuer (Go) | MVP. Builds clean, 5/5 tests pass, signs envelopes that externally verify with PyNaCl Ed25519. |
| Scoring (TS) | MVP. `tsc --noEmit` clean; `MOCK_MODE` returns deterministic DEPLOY for offline demos. |
| Reference SDK (TS) | Draft. Covers spec §§ 9 / 10 / 12 algorithms. 17 hardening gaps documented in [`docs/hardening-roadmap.md`](./docs/hardening-roadmap.md). |
| Architecture | Documented. 8 open questions deferred to sub-roadmaps. |
| Production deployment | Not started. Stack chosen ([`docs/stack-decision.md`](./docs/stack-decision.md)); next step is the issuer hardening + KMS integration. |

## License

- **Spec** (everything in [`spec/`](./spec)): CC0-1.0 — public domain.
- **Code** (everything else): Apache-2.0.

Both are designed so production deployments can build on Mimir without legal review at adoption time.

## Authored by

**Enchanter Labs.** [github.com/enchanter-ai](https://github.com/enchanter-ai).
