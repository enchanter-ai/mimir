# Mimir Roadmap

**Last updated:** 2026-05-16.
**Maintainer:** Enchanter Labs ([github.com/enchanter-ai](https://github.com/enchanter-ai))

---

## Where we are

| Layer | Status | Evidence |
|---|---|---|
| **Spec** — Provenance Envelope v2.1 Standards Track draft | ✅ Published (CC0) | [`spec/spec.pdf`](spec/spec.pdf), [`spec/index.mdx`](spec/index.mdx) |
| **Issuer (Go)** — HTTP signing service | ✅ MVP+; all tests pass | [`issuer/`](issuer/) |
| **AWS KMS path** | ✅ Code complete + wire-faithful fake validates the full path | [`issuer/kms/aws.go`](issuer/kms/aws.go), [`issuer/kms_integration_test.go`](issuer/kms_integration_test.go) |
| **Independent Rust verifier** | ✅ 6/6 tests including live round-trip against the Go issuer | [`spec/reference-impl-rust/`](spec/reference-impl-rust/) |
| **Adversarial test vectors** | ✅ 12/12 attacks correctly rejected | [`spec/test-vectors-adversarial/`](spec/test-vectors-adversarial/) |
| **MCP wire format** | ✅ Official Anthropic MCP SDK round-trip end-to-end | [`tests/mcp/`](tests/mcp/) |
| **Scoring (TypeScript)** — σ-bound 5-axis × 8-assertion quality rubric | ✅ Tool-call-result-appropriate rubric; calibrated against real Claude Sonnet 4.6 | [`scoring/`](scoring/) |
| **σ-bound calibration** | ✅ 50-case labeled set: 100% precision (0/23 bad→DEPLOY), 20% recall | [`scoring/calibration/calibration-report.md`](scoring/calibration/calibration-report.md) |
| **On-chain anchor** (`MimirValidationRegistry`, ERC-8004 shape) | ✅ 7/7 simulated-EVM tests | [`anchor/contracts/`](anchor/contracts/), [`anchor/go/anchor_test.go`](anchor/go/anchor_test.go) |
| **EigenLayer slashing wiring** | ✅ 5/5 AVS-mode tests against mocked IServiceManager + ISlasher | [`anchor/go/eigenlayer_test.go`](anchor/go/eigenlayer_test.go) |
| **Throughput + concurrency** | ✅ 1500 RPS sustained; 0 races under 500-goroutine stress | [`bench/`](bench/) |
| **Testnet deploy (Sepolia, permissionless mode)** | ✅ **Live** at [`0xEbdAa5a9…4117`](https://sepolia.etherscan.io/address/0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117); full lifecycle confirmed on-chain 2026-05-16 | [`docs/deployments.md`](docs/deployments.md) |
| **Production readiness audit doc** | ✅ Published | [`PRODUCTION_READINESS.md`](PRODUCTION_READINESS.md) |
| **Security audit prep doc** | ✅ Published; engagement-ready | [`AUDIT_PREP.md`](AUDIT_PREP.md) |

**Headline:** all 7 internal correctness gaps (G1–G7) are demonstrably closed at the protocol layer. The next milestones are external-action work: testnet deploy, audit engagement, and ecosystem adoption.

---

## Next 90 days

### M1 — Testnet deploy + EigenLayer AVS registration (~2 weeks)

Goal: a live `MimirValidationRegistry` on a public testnet, wired to real EigenLayer core contracts, with at least one registered operator anchoring real signed envelopes.

- [x] Run [`anchor/cmd/deploy`](anchor/go/cmd/deploy/main.go) on Sepolia in PERMISSIONLESS mode → [`0xEbdAa5a9…4117`](https://sepolia.etherscan.io/address/0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117).
- [x] Publish the contract address in [`docs/deployments.md`](docs/deployments.md).
- [ ] Etherscan source-verify the Sepolia deployment.
- [ ] Deploy a second contract instance on Holesky in AVS mode pointed at EigenLayer Holesky `ServiceManagerBase` + `AllocationManager`.
- [ ] Register the issuer's wallet as an EigenLayer operator with allocated stake.
- [ ] End-to-end live test: scoring service → issuer → `AnchorEnvelope()` → external Rust verifier.

### M2 — First external audit engagement (~6 weeks)

Goal: an independent security audit covering the smart contract + cryptographic correctness paths.

- [ ] Pick auditor (Trail of Bits / OpenZeppelin / Sigma Prime) per [`AUDIT_PREP.md`](AUDIT_PREP.md) § 7 audit tiers.
- [ ] Sign Statement of Work referencing a specific commit SHA.
- [ ] Hand over the [`AUDIT_PREP.md`](AUDIT_PREP.md) package + reproduction harness.
- [ ] Run the engagement (~4 weeks).
- [ ] Address every BLOCKER + HIGH finding; commit fixes; auditor re-verifies.
- [ ] Publish the audit report PDF + link from this repo's README.

**Cost:** Tier A audit ~$30–60K; Tier B ~$80–150K; Tier C ~$200K+.

### M3 — Public launch (~2 weeks, after M1)

Goal: announce Mimir to the MCP / agent / web3 communities and onboard the first external integrators.

- [ ] Launch blog post on enchanter-ai.com.
- [ ] Conference / podcast outreach: MCP devrel, EthCC, EigenLayer Discord.
- [ ] Reach out to top 5 MCP server projects (Claude Desktop integrators, Cursor extension authors) with the wiring guide.
- [ ] Set up a `mimir-discuss` GitHub Discussions board for spec questions.
- [ ] Demo video (3–5 min) showing the full pipeline: tool call → score → sign → verify → on-chain anchor → slash.

See [`docs/LAUNCH.md`](docs/LAUNCH.md) for the full launch checklist.

---

## Next 12 months

| Quarter | Theme | Concrete deliverable |
|---|---|---|
| Q3 2026 | **Operator network bootstrap** | 5+ independent operators running Mimir issuers with allocated EigenLayer stake; aggregate stake > 100 ETH |
| Q3 2026 | **Production hardening** | KMS-backed signing live in production; key rotation procedure documented; JWK-set publishing for transitioning keys |
| Q4 2026 | **MCP host integration** | Claude Desktop / Cursor / Cline ship Mimir-aware tool-call verification (as a vendor opt-in feature or via a plugin) |
| Q4 2026 | **Calibration v2** | 500-example labeled set with per-tool-category calibration curves; per-tool σ thresholds |
| Q4 2026 | **Mainnet deploy** | Audited contract live on Ethereum mainnet; first paid attestations |
| Q1 2027 | **DPoP / ClientIdentityProof extension** | Spec § 6.11 implemented; "trust-anchored" validation level proven end-to-end |
| Q1 2027 | **Multi-judge consensus** | Median-of-N voting for σ-bound to reduce LLM-as-judge variance |
| Q2 2027 | **Batched anchoring** | Merkle-tree batching reduces per-envelope gas from ~50k to ~5k amortized |

---

## Long-term (12+ months)

- **Multi-chain anchoring** — L2 deployments (Base, Arbitrum, Optimism); cross-chain digest mirror via canonical bridges.
- **Privacy-preserving envelopes** — selective disclosure of envelope content via zk-proofs over the canonical form.
- **Reputation graph** — operator reputation scores derived from σ-bound histories + slash records; queryable for routing decisions.
- **Compliance modules** — SOC2 / ISO 42001 evidence pipelines built on top of envelope archives.

---

## How to contribute

The repo is Apache-2.0 (code) + CC0 (spec) and welcomes external contributions:

1. **Spec questions:** open a Discussions thread tagged `spec`. We'll respond within a week.
2. **Implementation bugs:** open an issue with a minimal reproducer; the [`AUDIT_PREP.md`](AUDIT_PREP.md) § 8 reproduction harness covers most scenarios.
3. **Cross-language verifiers:** the canonical spec is `spec/index.mdx`; the Rust impl is the reference for "what a faithful verifier looks like." TypeScript / Java / Go-native impls welcome.
4. **Audit findings:** if you find a security issue, see [`SECURITY.md`](SECURITY.md) (TODO — Phase 7 deliverable).

---

## Decision log

Significant cross-cutting decisions are documented inline in their respective subsystems:

- σ threshold of 0.75 (was 0.45): see [`scoring/src/score.ts`](scoring/src/score.ts) `DEPLOY_SIGMA_THRESHOLD` rationale comment; full data in [`scoring/calibration/calibration-report.md`](scoring/calibration/calibration-report.md).
- σ computed over content axes only (safety excluded): same file.
- Tool-call-result-appropriate SAT assertions (replacing the prompt-quality ones): [`scoring/src/rubric.ts`](scoring/src/rubric.ts).
- Constructor signature `(IServiceManager, ISlasher, slashWad)` for the registry: see [`anchor/contracts/MimirValidationRegistry.sol`](anchor/contracts/MimirValidationRegistry.sol) constructor comment.
