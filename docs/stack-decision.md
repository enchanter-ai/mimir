# T2 — Tech Stack Recommendation: Quality Oracle AVS Production Deployment

**Author:** Enchanter Labs  
**Date:** 2026-05-13  
**Tier:** Sonnet (background)  
**Spec basis:** `state/specs/provenance-envelope/index-v2.1.mdx`  
**Reference impl constraint signal:** `reference-impl-ts/package.json` (uses `@noble/ed25519`, `@noble/curves`, `@noble/hashes`, `canonicalize`)

---

## Layer 1 — Language / Runtime: Issuer Service (Signing Path)

**Primary: Go (1.22 LTS)**

Rationale:

- **Crypto library ecosystem.** Go's standard library ships `crypto/ed25519` (RFC 8032 compliant, constant-time), `crypto/ecdsa` with P-256, and `crypto/sha256` — all audited by Google and the Go Security Team. The spec's § 15.4 requirement for constant-time comparison is met natively without a third-party dependency. `golang.org/x/crypto` extends this to additional algorithms, also well-audited.
- **KMS integration.** AWS KMS, GCP Cloud KMS, and HashiCorp Vault all publish first-party Go SDKs with PKCS#11 and KMS-backed signing support. The signing call path can be a single `kms.Sign()` RPC, keeping key material entirely in the HSM.
- **Latency profile.** Go's goroutine scheduler and near-zero GC pause profile (sub-1ms with GOGC tuning) makes p99 < 500ms achievable. Chainlink's OCR2 oracle nodes run Go; Pyth Network's price attestation nodes run Go. Both operate at comparable throughput envelopes.
- **EigenLayer SDK.** The EigenLayer Go SDK (`github.com/Layr-Labs/eigensdk-go`) is the primary maintained SDK as of 2026. It provides the BLS signing utilities, AVS registration contracts, and operator watcher loop the issuer node needs. The TypeScript SDK (`eigen-sdk-ts`) exists but lags the Go SDK in operator tooling.
- **Type safety / tooling.** Strong static typing, `go vet`, `staticcheck`, and the `govulncheck` scanner close the compile-time correctness gap.
- **Hiring pool.** Go is the lingua franca of blockchain infrastructure (geth, Cosmos SDK, Chainlink). The pool is deeper than Rust for this vertical.

Runners-up:

- **Rust (Tokio).** Strongest latency profile and best-in-class crypto library (`ring`, `ed25519-dalek`). The `alloy-rs` EVM library is excellent. Not selected because: no first-class EigenLayer SDK exists for Rust; the hiring pool for production EigenLayer operator work is almost entirely Go; integration overhead with the orchestration layer (Python/TypeScript) adds IPC complexity.
- **Node.js (LTS 22).** The existing reference implementation is TypeScript; `@noble/ed25519` and `@noble/hashes` are audited and spec-compliant. Not selected for the *signing path* because: Node's single-threaded event loop creates head-of-line-blocking risk under 100 attestations/sec; AWS KMS Node SDK async patterns introduce buffering complexity; and p99 tail latency under GC pressure is harder to bound in Node than in Go. Node.js remains the preferred runtime for the orchestration layer (Layer 2).
- **Bun.** Eliminated immediately: no SOC2/FIPS 140-2 certified KMS SDK, immature production story, no EigenLayer operator integration.

---

## Layer 2 — Language / Runtime: Off-Chain Orchestration (Scoring Engine + MALT Monitor)

**Primary: Node.js (LTS 22) with TypeScript (5.x, strict mode)**

Rationale:

- **LLM API integration.** The scoring engine invokes Anthropic's Opus/Sonnet/Haiku dispatch via the `@anthropic-ai/sdk`. That SDK is TypeScript-first. Native async/await concurrency maps cleanly onto the fan-out pattern of the 5-axis scoring pipeline.
- **Code reuse from reference implementation.** The spec reference impl (`@modelcontextprotocol/provenance-envelope-reference`) is TypeScript. The verification and registry client modules it defines can be imported directly by the orchestration layer for consumer-side validation, avoiding a full reimplementation.
- **Typing.** TypeScript strict mode with `exactOptionalPropertyTypes` and `noUncheckedIndexedAccess` catches the class of envelope field-handling bugs that feed into § 15.12 (duplicate-member injection vectors). `zod` schema parsing for incoming envelope payloads enforces the runtime layer.
- **Operational maturity.** The entire Wixie Opus/Sonnet/Haiku dispatch chain already runs on Node/TypeScript. Keeping the scoring engine on the same runtime minimises operational surface.
- **MALT monitor I/O.** MALT's behavioral monitoring is I/O-bound: polling attestation streams, querying the envelope archive, emitting alerts. This fits Node's event-loop model.

Runners-up:

- **Python 3.12.** Has the deepest ML/stats ecosystem (sklearn, scipy) for σ-bound analysis. Not selected as primary because: Python's GIL limits concurrency in the hot LLM-dispatch path; Anthropic's SDK is Python-first too, but TypeScript is where the rest of the issuer stack lives; type safety at the protocol boundary (envelope parsing) is weaker in Python even with mypy.
- **Go** (same as issuer). Would unify the runtime but adds friction at the LLM SDK boundary (Anthropic Go client is unofficial/thin) and forfeits the reference-impl reuse advantage.

**Implementation note:** the scoring engine and issuer service communicate over the message queue (Layer 5), not via shared memory. They may be deployed as separate containers from day one.

---

## Layer 3 — Key Management

**Primary: AWS KMS with FIPS 140-2 Level 3 hardware security modules (CloudHSM backing via KMS Custom Key Store)**

Rationale:

- **Algorithm support.** AWS KMS natively supports Ed25519 and ECDSA P-256 signing via `KMS:Sign`. Both map directly to the spec's required profiles (`mcp-provenance/2026-05-13-ed25519`, `mcp-provenance/2026-05-13-ecdsa-p256`). Private key material never leaves the HSM.
- **No key export.** The `kms.CreateKey` API creates non-exportable keys. The `kms.Sign` API performs the sign operation server-side and returns only the signature bytes — precisely the model required by § 15.5 (Key Compromise mitigation).
- **Attestation.** AWS KMS Custom Key Store (backed by CloudHSM) carries FIPS 140-2 Level 3 validation. SOC 2 Type II and ISO 27001 certificates are published annually.
- **Go SDK.** AWS SDK for Go v2 (`github.com/aws/aws-sdk-go-v2/service/kms`) is production-grade and actively maintained. Signing latency via KMS API over VPC endpoints is typically 5–15ms p50, well within the 500ms budget.
- **Key rotation.** The spec mandates at least annual rotation (§ 15.5). KMS Automatic Key Rotation covers this natively, with the old key version retained for historical verification during the rotation window.
- **Production case study.** Pyth Network's price attestation operators and several EigenLayer AVS operators on mainnet use AWS KMS for BLS and ECDSA signing keys.

Runners-up:

- **HashiCorp Vault (Transit Secrets Engine).** Excellent API, supports Ed25519, runs on-prem or in HCP. Not selected as primary because: adds a high-availability infrastructure dependency; FIPS 140-2 Level 3 is only achievable via HSM seal (additional complexity); Cloud KMS is simpler to operate at the AVS-node scale initially. Vault is the recommended fallback for operators who cannot use AWS (e.g., multi-cloud or air-gapped deployments).
- **AWS Nitro Enclaves / Intel SGX (Phala-style).** Technically superior for execution attestation; relevant when the spec's execution-binding component (TEE integration, roadmap component 3) is built. Not selected for the signing-path key management layer because: TEE key management requires a custom attestation workflow; the operational maturity of TEE-based KMS is lower than cloud KMS for the signing use-case alone; and the spec's § 15.5 mitigation is satisfied by KMS without the TEE complexity.
- **FROST Threshold Signatures.** Ideal for the multi-issuer K-of-N path (roadmap component 5). Not the right choice for the single-issuer signing key at launch: no mature production Go library with KMS backing exists; adds distributed coordination latency to the signing path.
- **Google Cloud KMS.** Feature-equivalent to AWS KMS for this use case. Not selected because the issuer infrastructure is presumed AWS-primary (EKS, VPC endpoints). GCP KMS is a valid peer alternative for operators on GCP.

---

## Layer 4 — Database

**Primary: PostgreSQL 16 (single database, two schema personas)**

Rationale:

The system has two access patterns that differ sharply:

| Pattern | Volume | Shape |
|---------|--------|-------|
| Envelope archive | ~100 envelopes/sec ingested, append-only | Write-heavy, sequential, audit immutable |
| Registry trust-anchor cache | Reads per envelope validation; infrequent writes | Read-heavy, point-lookups by `tool_id` + `key_id` |

Both fit PostgreSQL at the initial scale target with different table designs:

- **Envelope archive:** `UNLOGGED` or `LOGGED` append-only table with `tool_call_id` as partition key and a `created_at` range partition. Bulk insert via `COPY` or batched `INSERT`. No updates, no deletes. Partial indexes on `tool_id` for audit queries.
- **Registry cache:** standard `LOGGED` table with `(tool_id, key_id)` as primary key. Read-heavy; `pg_bouncer` connection pooling. Cache TTL enforced at application layer per § 15.13 (`did_cache_ttl`).

Single-database rationale: operational simplicity at the scale of 100 attestations/sec is high. PostgreSQL's WAL-backed durability satisfies the immutable-archive requirement. At 100 envelopes/sec, PostgreSQL easily handles the write throughput (Postgres benchmarks routinely demonstrate 10k–50k simple inserts/sec on modest hardware). Splitting to two databases adds a distributed-transaction surface with no benefit at this scale.

Runners-up:

- **FoundationDB.** Superior scalability ceiling and multi-model flexibility. Not selected because: the Go and Node.js FoundationDB client ecosystems are less mature than `pgx`; the Postgres-skilled hiring pool is larger; and the operational maturity of FDB on EKS is lower than RDS/Aurora PostgreSQL.
- **DynamoDB.** Excellent for the trust-anchor cache pattern (key-value, read-heavy). Not selected because: the envelope archive needs rich querying (range scans by `invoked_at`, audit exports); DynamoDB's scan cost model penalises this; mixing DynamoDB + Postgres splits the ops burden.

**Migration path:** if append write throughput exceeds ~5k envelopes/sec, partition the envelope archive to a dedicated `TimescaleDB` hypertable on the same PostgreSQL instance, or introduce a Parquet-backed cold storage tier. No schema change required for the registry cache.

---

## Layer 5 — Message Queue

**Primary: Redpanda (Kafka-compatible) — self-hosted on EKS**

Rationale:

Two queue segments are required:

- **Ingest → Scoring:** receives raw `tools/call` responses with their envelopes; fan-out to the 5-axis scoring pipeline. At 100 attestations/sec, peak throughput is modest (~10KB/message × 100/sec = ~1MB/sec). Requires at-least-once delivery and consumer group rebalancing.
- **Scoring → On-chain publisher:** delivers validated `(envelope, score-bundle)` pairs for the ERC-8004 registry transaction submission. Requires exactly-once semantics at the on-chain write step.

Redpanda satisfies both:

- Kafka-compatible consumer groups and partition model handle fan-out.
- Idempotent producer + transactional API enables exactly-once for the on-chain path.
- **No ZooKeeper dependency** (Redpanda is a single binary): significantly lower operational overhead on EKS vs. Apache Kafka.
- Native C++ implementation delivers p99 latency of 1–2ms locally — negligible contribution to the 500ms signing budget.
- Production case study: multiple EVM oracle networks use Redpanda (or Kafka) as the ingest spine between off-chain data aggregation and on-chain publication (Chainlink's OCR2 pipeline uses a Kafka-like internal bus).

Runners-up:

- **NATS JetStream.** Lower operational complexity than Redpanda, excellent Go client, comparable latency. Not selected because: at the time of writing, its Kafka-compatible surface is incomplete; exactly-once delivery semantics are less mature; and the Redpanda/Kafka ecosystem has broader operator familiarity in the EigenLayer/EVM oracle space.
- **PostgreSQL LISTEN/NOTIFY.** Sufficient for low-volume ingest. Not selected because: it does not support consumer groups; replay from offset is not first-class; coupling the queue to the database creates a single failure domain.
- **AWS SQS.** Mature, low operational overhead. Not selected because: SQS FIFO queue throughput is capped at 3000 messages/sec per queue without batching workarounds; no native Kafka-compatible offset replay; lock-in risk for a multi-cloud AVS operator.

---

## Layer 6 — EVM Interface (RPC Clients + Transaction Submission)

**Primary: viem (TypeScript) for the orchestration layer; `go-ethereum` (`ethclient`) for the issuer/AVS Go service**

Rationale:

The two runtimes interact with the EVM differently:

**viem (orchestration / Node.js layer):**

- Strong TypeScript types for ABI encoding: the ERC-8004 contract ABI can be statically typed, catching mis-encoded arguments at compile time.
- Built-in RPC transport abstraction with connection pooling and automatic failover across multiple provider URLs — critical for the 100 attestations/sec throughput requirement.
- Mempool-aware fee estimation via `eth_maxPriorityFeePerGas` + EIP-1559 support.
- Production adoption: used at scale by Uniswap, Aave, and most Ethereum dApp frontends. The off-chain orchestration layer reads registry state and monitors on-chain events via viem.

**go-ethereum (`ethclient`) for the issuer Go service:**

- Native Go bindings; no cgo, no FFI overhead.
- Transaction signer integrates directly with AWS KMS via the `aws-sdk-go-v2` KMS signer, enabling HSM-backed transaction signing for the on-chain publisher.
- Used in production by every major Go-based EigenLayer operator.
- `abigen`-generated contract bindings provide compile-time type safety for ERC-8004 write calls.

Runners-up:

- **ethers.js (v6).** Mature, but viem is preferred in new projects for its stronger TypeScript ergonomics and better tree-shaking.
- **alloy-rs (Rust).** Best-in-class for Rust; not applicable since the issuer layer is Go.

**RPC pooling strategy:** deploy `bloxroute` or `eRPC` as a local RPC proxy that load-balances across at least two RPC providers (Alchemy + Infura minimum; Chainstack as tertiary). The proxy handles rate-limit backoff and exposes a single endpoint to the application layer. This keeps the 100 attestations/sec EVM interface requirement from becoming a provider single-point-of-failure.

---

## Layer 7 — EigenLayer AVS Integration

**Primary: EigenLayer Go SDK (`eigensdk-go`) — operator-side integration path**

Current state (as of 2026-05-13):

The `Layr-Labs/eigensdk-go` SDK is the production path for AVS operator integration on mainnet. It provides:

- `chainio` package: typed bindings for `AVSDirectory`, `DelegationManager`, `StrategyManager`, and `EigenPod` contracts.
- `signerv2` package: pluggable signer supporting both in-memory and remote (KMS) keys — plugs directly into Layer 3 (AWS KMS).
- `operator` package: the operator registration lifecycle (`RegisterOperatorWithAVS`, deregistration, metadata URI update).
- `services/bls_aggregation`: BLS aggregate signature service for the multi-issuer attestation path.
- Event watcher: watches `OperatorRegistered`, `OperatorDeregistered`, `TaskSubmitted` events needed by the MALT monitor.

The TypeScript SDK (`eigen-sdk-ts`) exists but as of 2026 is scoped to frontend tooling and task-sender flows; it does not provide operator-side registration or BLS aggregation primitives needed for the AVS node. Use `eigensdk-go` for the operator.

**Integration pattern:**

The issuer service registers as an EigenLayer operator via `AVSDirectory.registerOperatorWithAVS`. On each attestation batch, it submits a BLS-signed task response to the `TaskManager` contract. The MALT monitor watches for slashing events on `SlashingManager` using the event watcher module in `eigensdk-go`. The AVS wrapper (roadmap component 5, Stage 2) will extend this with the custom slashing logic for σ-bound violations.

**Production case study:** EigenAI (EigenLayer's native AI inference operator network) uses `eigensdk-go` for their Go-based operator nodes, including KMS-backed BLS signing and the operator registration lifecycle.

---

## Layer 8 — Observability

**Primary: OpenTelemetry (OTEL SDK) → Grafana Cloud (Tempo + Loki + Mimir)**

Rationale:

- **Vendor-neutral instrumentation.** OTEL SDK for Go (`go.opentelemetry.io/otel`) and Node.js (`@opentelemetry/sdk-node`) provide traces, metrics, and logs via the OTEL wire protocol. No vendor SDK is embedded in application code; the destination is configurable via the OTEL Collector.
- **Grafana Cloud** combines Tempo (distributed traces), Loki (structured logs), and Mimir (long-term Prometheus-compatible metrics) under one pane. It supports OTEL ingest natively over OTLP/gRPC.
- **Signing path tracing.** The p99 < 500ms constraint requires per-operation span tagging. OTEL trace context propagated from envelope ingest through KMS sign → Redpanda publish → on-chain submit allows root-cause analysis when the SLO is violated.
- **EigenLayer operator metrics.** `eigensdk-go` exposes Prometheus-compatible metrics for operator health, stake, and task success rates. These scrape directly into Mimir.
- **Cost.** Grafana Cloud's free tier handles the initial pilot load; paid tier scales with log/trace volume without re-instrumenting.

Runners-up:

- **Datadog.** Best-in-class APM and log correlation; strong EKS integration. Not selected because vendor lock-in (Datadog SDK baked into application code) conflicts with the OTEL vendor-neutral requirement, and cost at high trace volume is significantly higher than Grafana Cloud.
- **Honeycomb.** Excellent for high-cardinality trace analysis. Not selected because Mimir/Loki already covers the metrics/logs use case, and adding Honeycomb creates a split observability surface.
- **Self-hosted Tempo+Loki+Mimir.** Architecturally identical to Grafana Cloud minus the managed control plane. Valid for operators with strict data-residency requirements; deferred until a compliance requirement drives it.

---

## Layer 9 — Deployment Platform

**Primary: Amazon EKS (managed Kubernetes) with Terraform IaC**

Rationale:

- **HSM integration.** AWS KMS Custom Key Store (CloudHSM) is native to the AWS VPC. EKS pods can assume IAM roles via IRSA (IAM Roles for Service Accounts), granting scoped `kms:Sign` permissions without credential files. This is the canonical pattern for HSM-integrated workloads on AWS.
- **Multi-region.** EKS can be deployed across `us-east-1` and `eu-west-1` with a Global Accelerator or Route53 latency-based routing in front of the ingest endpoint. The envelope archive (PostgreSQL via RDS Aurora Global Database) replicates cross-region. This satisfies RPC provider diversity by allowing each region to use geographically distinct RPC providers.
- **Redpanda on EKS.** Redpanda's Helm chart (`redpanda/redpanda`) deploys into EKS with persistent EBS volumes and runs the three-broker minimum for HA.
- **Secrets management.** AWS Secrets Manager + External Secrets Operator for Kubernetes synchronises secrets into EKS pods. No file-based secrets.
- **Terraform.** Mature IaC; Terraform AWS provider covers EKS, RDS, KMS, VPC, IAM, Secrets Manager. The EigenLayer community publishes Terraform modules for AVS node infrastructure.
- **Production case study.** EigenAI operators and Chainlink node operators run on EKS. Both require the same combination of HSM integration + multi-region + RPC diversity that the Quality Oracle AVS needs.

Runners-up:

- **GKE (Google Kubernetes Engine).** Feature-equivalent; Cloud KMS (Layer 3 runner-up) would pair with it. Not selected because AWS is the assumed primary cloud given the KMS and EKS decision being co-located in one provider.
- **Fly.io.** Excellent developer experience; not selected because: no native HSM/KMS integration at the hardware level; no multi-region database replication story at the required SLA; operator maturity for blockchain infrastructure is unproven.
- **Raw EC2 with Terraform.** Eliminates the Kubernetes control plane overhead; valid for a single-region MVP. Not selected because: Kubernetes provides the self-healing, rolling-update, and horizontal scaling primitives needed to operate the AVS at production SLA without custom runbooks.

---

## Layer 10 — CI/CD

**Primary: GitHub Actions with `cosign` (Sigstore) for image signing and `goreleaser` for Go binary releases**

Rationale:

- **GitHub Actions.** The codebase is already git-hosted; GitHub Actions is zero-additional-infrastructure. The ecosystem of OTEL, Kubernetes, AWS, and EigenLayer tooling has mature GHA actions.
- **Image signing via `cosign`.** Every Docker image pushed to ECR is signed with a Sigstore keyless signature (OIDC-backed, no long-lived key to manage). This satisfies supply-chain integrity requirements without introducing a new key-management surface. Verified at deploy time by a Kyverno or OPA Gatekeeper policy in EKS.
- **`goreleaser`.** Handles Go binary cross-compilation, GitHub Release artifact upload, and Docker image builds for the issuer service in a single declarative config file.
- **Release automation.** Conventional Commits + `release-please` (Google) automate changelog generation and version bumping on merge to `main`. A signed release tag triggers the `goreleaser` pipeline.
- **Branch protection.** `main` requires: passing CI, a code owner approval, and a `cosign verify` step on any image the release pipeline would promote.

Runners-up:

- **BuildKite.** Superior for large monorepo / parallel test matrix scenarios. Not selected because the infrastructure overhead (BuildKite agents on EKS) adds complexity before the codebase grows to need it.
- **GitLab CI.** Competitive feature set; not selected because the repo is on GitHub and migration is not justified.

---

## One-Page "Why This Stack" Summary

| Layer | Primary Choice | Constraint Satisfied |
|-------|---------------|---------------------|
| Issuer runtime | **Go** | Cryptographic correctness (stdlib constant-time crypto); EigenLayer SDK (`eigensdk-go`); p99 < 500ms (goroutine scheduler, sub-ms GC pauses) |
| Orchestration runtime | **Node.js + TypeScript** | Strong typing (strict TS); LLM API integration (Anthropic SDK); reference-impl code reuse |
| Key management | **AWS KMS + CloudHSM** | HSM/KMS integration (FIPS 140-2 Level 3); no key export; SOC2 attestation; Ed25519 + ECDSA P-256 native support |
| Database | **PostgreSQL 16** | Append-only envelope archive (WAL-backed immutability); read-heavy registry cache (indexed lookups); operational maturity |
| Message queue | **Redpanda** | EVM RPC throughput (decouples ingest from on-chain publish at 100 attest/sec); exactly-once on-chain delivery; no ZooKeeper overhead |
| EVM interface | **viem + go-ethereum** | EVM RPC throughput (viem provider pooling; `ethclient` KMS signer); strong typing (ABI bindings at compile time); replay protection (EIP-1559) |
| EigenLayer AVS | **`eigensdk-go`** | EigenLayer AVS operator integration (BLS aggregation, operator registration, slashing watcher); KMS-backed signing |
| Observability | **OTEL → Grafana Cloud** | Vendor-neutral (OTEL SDK); signing-path latency tracing; EigenLayer operator metrics |
| Deployment | **EKS + Terraform** | HSM integration (IRSA → KMS); multi-region; RPC provider diversity; secrets management (Secrets Manager + ESO) |
| CI/CD | **GitHub Actions + cosign + goreleaser** | Signed Docker images (supply-chain integrity); automated release; no long-lived CI signing keys |

### Cross-Cutting Constraint Check

**Cryptographic correctness.** No roll-your-own crypto anywhere in the stack. Go stdlib for the signing path; `@noble/*` (already in the reference impl) for the TypeScript layer where needed; libsodium-equivalent guarantees via audited libraries. The spec's § 15.4 mandate for constant-time comparison is met by Go's `subtle.ConstantTimeCompare` and by the audited KMS verification path.

**Low-latency signing (p99 < 500ms).** The signing path is: envelope construction (in-process, < 1ms) → KMS `Sign` RPC over VPC endpoint (5–15ms p50, 30ms p99) → Redpanda produce (1–2ms p99). Total: ~35ms p99 at the signing step, leaving 465ms of headroom for DID resolution caching and queue propagation.

**HSM/KMS integration.** AWS KMS Custom Key Store is the only component that touches private key material. The `kms.Sign` API is the sole signing interface in the issuer. No file-based keys, no in-memory private key bytes in production (closing reference-impl production gap #3).

**EVM RPC throughput.** Redpanda decouples the 100 attestations/sec ingest rate from the on-chain submission rate. The on-chain publisher batches transactions and uses EIP-1559 dynamic fee estimation. viem's RPC transport pool handles provider failover without the application layer seeing it.

**EigenLayer AVS operator integration.** `eigensdk-go` is the production SDK for this. The operator registration, BLS aggregation, and slashing watcher are all first-class primitives in that library.

**Operational maturity.** Every primary choice is in production use by Chainlink, Pyth, EigenAI, or equivalent oracle/AVS networks. No bleeding-edge components.

---

*This document is the T2 output for the 2026-05-13 Quality Oracle AVS kickoff. It feeds into T4 synthesis alongside T1 (architecture) and T3 (reference-impl hardening roadmap).*
