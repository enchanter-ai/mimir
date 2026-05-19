# Mimir — Changelog

All notable changes to this project are documented here. Conforms to [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versioning follows [SemVer 2.0](https://semver.org/).

The `spec/` directory is versioned independently — see `spec/index.mdx` frontmatter for spec-level revisions.

---

## [Unreleased]

### Added (v0.1.1-pending)

- **`EigenLayerSlasherAdapter.sol`** — production-shape adapter that implements Mimir's narrow `ISlasher` interface by translating to EigenLayer v2's `AllocationManager.slash(SlashingParams)`. Operators deploy this alongside the registry when they want slashes routed to a real EigenLayer AllocationManager; Mimir's registry stays restaking-primitive-agnostic.
- **`MockAllocationManager.sol`** — real-EigenLayer-shape mock implementing `slash(SlashingParams)` for tests + on-chain demonstration of the adapter pattern.
- **`anchor/go/cmd/deploy-eigenlayer`** — one-shot deploy + verify program: deploys the full 4-contract stack (MockAllocationManager, EigenLayerSlasherAdapter, MockServiceManager, MimirValidationRegistry) and runs a live `register → anchor → revoke → confirm slash` lifecycle with on-chain assertions.
- **Live Sepolia EigenLayer-adapter deployment** at [`0x633E3e37068a6205DD662a4b8b3637e860e49E42`](https://sepolia.etherscan.io/address/0x633E3e37068a6205DD662a4b8b3637e860e49E42) — full lifecycle proven; all four `SlashingParams` fields verified correct against on-chain `MockAllocationManager`.
- 4 new Go tests (`eigenlayer_adapter_test.go`): call-translation correctness + 3 defensive-input rejections. Anchor module is now **14/14 PASS**.
- `scripts/probe-rpcs.py` — added Hoodi network probes (chain_id 560048; Ethereum Foundation successor to Holesky).
- `docs/deployments.md` — v0.1.1 entry with EigenLayer adapter on-chain assertions table + reproduction commands.

### Migration notes

- Mimir's `ISlasher` interface (in `IEigenLayer.sol`) is **unchanged**; no breaking change to the registry's slasher-call shape.
- Operators wanting to integrate with real EigenLayer should deploy `EigenLayerSlasherAdapter` pointing at the canonical `AllocationManager` for their network (mainnet / Hoodi — see https://github.com/Layr-Labs/eigenlayer-contracts/tree/dev/script/output for the addresses).

---

## [0.1.0] — 2026-05-16

The initial public release. The full Mimir protocol stack — spec, three independent implementations, on-chain settlement, scoring rubric, calibration data, operator tooling — is live and reproducible from a clean clone.

### Added

#### Protocol + spec
- Provenance Envelope **v2.1 Standards Track draft** (CC0). 26 sections covering wire format, canonicalization (RFC 8785), three-level validation model, threat model, MCP wire-format binding, ClientIdentityProof (DPoP), and on-chain anchoring.
- **Rendered PDF** (`spec/spec.pdf`) with hand-authored SVG diagrams and Enchanter Labs cover.
- **35 happy-path test vectors** + **12 adversarial vectors** (`spec/test-vectors-adversarial/`) covering signature tampering, replay-window violations, algorithm downgrade, key-id swap, canonical-form whitespace handling, and unknown-field handling.
- **Reference TypeScript SDK** (`spec/reference-impl-ts/`) for envelope production + verification, embeddable in MCP clients.
- **Independent Rust verifier** (`spec/reference-impl-rust/`) written from the spec alone — 6/6 tests including live round-trip against the Go issuer. Proof that the spec is implementable without reading the Go source.

#### Issuer service (Go)
- HTTP service exposing `/v1/attest`, `/v1/attest-mcp` (real MCP JSON-RPC 2.0 `tools/call`), `/v1/healthz`, `/v1/key`, `/v1/keys`, `/.well-known/jwks.json`.
- **KMS layer** with three pluggable backends — ephemeral (dev), mock (test), AWS KMS (production). The AWS path is fully wired against `aws-sdk-go-v2/service/kms@v1.51.1` with a wire-faithful `AWSKMSFake` that validates the DER-encoded SubjectPublicKeyInfo round-trip, the `MessageType: RAW + SigningAlgorithm: ED25519_SHA_512` contract, and the raw-64-byte-signature return shape — without requiring real AWS credentials to exercise.
- **MCP wire-format schema validator** (`/v1/attest-mcp`) with strict JSON-RPC 2.0 conformance.
- **JWK Set + key rotation** — historical keys appear at `/v1/keys` with `status: retired | revoked`. Verifiers can validate envelopes signed under any non-revoked key. Operator workflow scripted at `scripts/rotate-key.py`.
- **DPoP ClientIdentityProof extension** (RFC 9449) — accepts EdDSA + ES256 proofs from either the `DPoP` HTTP header or a `client_identity_proof` body field. On valid proof, `envelope.invoked_by` becomes `did:jwk:<RFC-7638-thumbprint>` and `validation_level` flips to `trust_anchored`. On invalid proof, the request is rejected with HTTP 400 — no silent fallback.
- **Per-IP token-bucket rate limiter** with X-Forwarded-For support, healthz bypass, env-tunable RPS + burst (`ISSUER_RATELIMIT_RPS`, `ISSUER_RATELIMIT_BURST`).
- **Structured logging + Tracer interface** (`telemetry/`) — JSON logs via Go 1.22 `log/slog`, span instrumentation on `handle_attest` and `build_envelope` hot paths.

#### Scoring service (TypeScript)
- Fastify HTTP service on `/v1/score` that routes tool-call results through Claude Sonnet 4.6.
- **5-axis σ-bound rubric** (clarity, specificity, faithfulness, safety, structure) + **8-assertion gate** (request_addressed, cites_source, no_hallucination_markers, no_sycophancy, no_hedges, complete_for_request, format_matches_request, bounded_uncertainty).
- `MOCK_MODE=1` for offline / CI use; returns a deterministic DEPLOY-tier stub.
- **Production-empirical σ threshold of 0.75** (was the heuristic Wixie-derived 0.45). Calibrated against a 50-case labeled set with 100% precision (0/23 bad cases reached DEPLOY) and 20% recall.

#### On-chain anchor (Solidity + Go)
- **`MimirValidationRegistry`** (Solidity ^0.8.20) — ERC-8004 Validation Registry shape + EigenLayer-style slashing wiring. Two operating modes selected at construction:
  - **Permissionless** — anyone can register / revoke. Used for development and the current Sepolia deployment.
  - **AVS** — operator-gated `register` (via `IServiceManager.isOperator`), `revoke` triggers `ISlasher.slash()` with a configurable wadSlashed (default 1e17 = 10%).
- **Go client** (`anchor/go/`) — `RPCClient` interface working against both `ethclient.Client` and `simulated.Client`. ABI + creation bytecode + runtime bytecode + immutable references + CBOR metadata all embedded for offline use.
- **12/12 simulated-EVM tests** — 7 permissionless-mode lifecycle tests + 5 AVS-mode slashing tests with mocked `IServiceManager` + `ISlasher` matching the real EigenLayer v2 interfaces.
- **Deployed live on Sepolia** at [`0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117`](https://sepolia.etherscan.io/address/0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117). Full lifecycle (anchor → verify → revoke → re-verify) confirmed on-chain. **Bytecode strict-match verified** via `scripts/verify_build.py` — masks immutables (288 bytes across 3 constructor args) and CBOR metadata (43-byte suffix), then asserts byte-equality with `eth_getCode`.
- **Genkey CLI** (`anchor/go/cmd/genkey`) — mint a fresh testnet wallet, print the address + private key in copy-paste-ready format.

#### Tooling + infrastructure
- **End-to-end demo** (`demo.py`) — single-command pipeline: spawn both services → score → sign → externally verify with PyNaCl. Returns `[OK] SIGNATURE VERIFIED`.
- **Anthropic MCP SDK interop test** (`tests/mcp/mcp_server.py` + `mcp_client.py`) — official SDK round-trip from real `tools/call` through the issuer's MCP schema validator.
- **Throughput + concurrency bench** (`bench/`) — 1500 RPS sustained, 0 races at 500-goroutine stress. Now distinguishes 429s from real failures (set `ISSUER_RATELIMIT_RPS=0` for max-throughput runs).
- **Docker images** — multi-stage distroless builds for both services; `docker-compose.yml` stack with healthchecks.
- **GitHub Actions CI** — issuer + anchor + Rust verifier + adversarial vectors + scoring typecheck, all green on every push to `main`.
- **Dependabot** — Go (×3), npm (×2), Cargo, GitHub Actions; AWS SDK + go-ethereum grouped to avoid noise.
- **Build reproducibility** — `make verify-build` recompiles bytecode and asserts strict equality with on-chain deployment. `make sbom` emits CycloneDX SBOMs per ecosystem.

#### Documentation
- `README.md` — 30s demo, test trio, novelty positioning, production status.
- `architecture.md` — system diagram + component map.
- `PRODUCTION_READINESS.md` — G1-G7 closure evidence with reproduction commands.
- `AUDIT_PREP.md` — three audit tiers, threat model (A1-A8), code-provenance disclosure, engagement-letter checklist.
- `ROADMAP.md` — 90-day milestones, 12-month plan, decision log.
- `docs/RUNBOOK.md` — 8 incident playbooks (key compromise, API down, reorg, OOM, RPC, DPoP drift, slasher, calibration drop).
- `docs/LAUNCH.md` — public-launch checklist with day-of sequence + 30-day success metrics.
- `docs/getting-started.md` — 15-minute integrator tutorial.
- `docs/deployments.md` — live contract registry.
- `docs/integrate-claude-desktop.md` — recipe for wiring Mimir into Claude Desktop's MCP config.
- `SECURITY.md` + `CONTRIBUTING.md` + `CODE_OF_CONDUCT.md` — community + disclosure infrastructure.

### Known limitations

- **No third-party security audit yet.** See `AUDIT_PREP.md` for the engagement package; pre-launch beta only.
- **No mainnet deploy.** Hard-gated on audit completion.
- **σ-bound recall is 20%** at the calibrated threshold — DEPLOY is rare by design, not a bug. Pipeline still works at all verdicts.
- **Real OTLP exporter not bundled.** Operators use the structured-JSON log stream via an otel-collector sidecar; the issuer doesn't vendor the otel-go SDK in-process.
- **AVS-mode live deploy still pending.** Holesky's public RPCs were unstable as of 2026-05-16; the Sepolia deploy is in permissionless mode. AVS-mode wiring is proven against mocks (5/5 tests pass).

---

## Format

This changelog is `git tag -a`-driven. To cut a new version:

1. Move the items in `## [Unreleased]` into a new `## [X.Y.Z] — YYYY-MM-DD` section.
2. `git tag -a vX.Y.Z -m "Release vX.Y.Z"` then `git push --tags`.
3. The tag becomes the audit-pinned commit-SHA referenced by `AUDIT_PREP.md` § 11.

[Unreleased]: https://github.com/enchanter-ai/mimir/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/enchanter-ai/mimir/releases/tag/v0.1.0
