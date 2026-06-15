# Mimir — Agent Contract

Audience: any coding agent (Codex, Claude Code, etc.) working in this repo. Mimir is a
**software product**, not a Claude Code plugin suite — there are no skills/subagents to
load. This file orients an agent that is editing the code.

Author: Enchanter Labs · Code: Apache-2.0 · Spec: CC0-1.0

## What Mimir is

An MCP-layer provenance standard: it binds *request + result + sources* under one Ed25519
signature over the RFC-8785 (JCS) canonical form of
`(tool_id, tool_version, invoked_at, invoked_by, request_digest, result_digest, sources[])`,
with restaked-stake slashing if a signed field later proves false. One spec, three reference
verifiers, on-chain settlement (ERC-8004 shape) live on Sepolia.

## Source of truth

- **The spec is canonical**, the implementations conform to it: `spec/` (and
  `state/specs/provenance-envelope/index-v2.1.mdx`). Change behavior in the spec first.
- `architecture.md` — system overview (Quality Oracle AVS, actors, on-chain settlement).
- `README.md` — product framing and current status.

## Components

| Path | Language | Role |
|------|----------|------|
| `issuer/` | Go | Envelope issuer (signing, σ-bound + assertion gate) |
| `anchor/` | JS/Node | On-chain anchor / contracts glue (`compile.js`) |
| `scoring/` | Python | σ-bound scoring oracle (5-axis × 8-SAT) |
| `bench/` | Go | Benchmarks (`bench.go`, concurrency tests) |
| `spec/` | Markdown | The org-neutral standard (CC0) |
| Rust/TS verifiers | Rust, TS | Independent parsers of the canonical form |

## Build & test (Makefile)

- `make test` — full suite: `test-issuer test-anchor test-rust test-adversarial`
- `make compile` / `make verify-build` — build + reproducibility check
- `make docker` — `docker-issuer` + `docker-scoring`
- `make sbom` — emit SBOM

Run the narrow target for the component you touched (e.g. `make test-issuer`) before `make test`.

## Conventions (do not violate)

- **Honest-numbers contract.** Every quantitative figure (latency, gas, σ-bound, AUROC) is a
  **design target** unless it is a measured production number; tag design targets `[design target]`.
  Never present a target as a guarantee or a measurement.
- **Canonical form is load-bearing.** Any change touching the signed tuple, RFC-8785-JCS
  canonicalization, or digest construction must keep all three verifiers (Go/Rust/TS) agreeing —
  run `make test-rust` and the issuer/anchor tests.
- **Adversarial vectors must stay green** (`make test-adversarial`) — they encode the threat model.
- Author/attribution in any spec, RFC, or public artifact reads **Enchanter Labs**.
