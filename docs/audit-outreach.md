# Smart-contract audit outreach

Pre-written email + scope summary the operator sends to candidate auditors. Saves a back-and-forth round on basic facts about Mimir.

---

## Candidate auditors (2026-05)

| Firm | Tier-A fit | Tier-B fit | Notable Mimir-adjacent work |
|---|---|---|---|
| **Trail of Bits** | ✅ Yes | ✅ Yes | Compound, Uniswap, MakerDAO, EigenLayer (some) |
| **OpenZeppelin** | ✅ Yes | ✅ Yes | Most ERC-XX standards; AVS-pattern reviews |
| **Sigma Prime** | ✅ Yes | ✅ Yes | Lido, Ethereum consensus clients |
| **NCC Group** | ❌ skip Tier-A | ✅ Strong fit | Cryptographic protocol review (JCS, JWS, DPoP) |
| **Spearbit** | ✅ Yes (community-driven) | partial | Faster turnaround; rotating reviewer pool |
| **Quantstamp** | partial | partial | Best for solidity-only; weaker on Go/Rust crypto code |

Recommend **Trail of Bits or OpenZeppelin for Tier A** (smart contract only), **NCC Group for Tier B** (full crypto-correctness across Go + Rust + Solidity).

---

## Email template (Tier A — smart contract only)

> Subject: Smart-contract audit inquiry — Mimir Validation Registry (Apache-2.0)
>
> Hi <firm contact>,
>
> Enchanter Labs is preparing a smart-contract audit engagement for **Mimir**, an open-source MCP tool-call provenance oracle. We'd like to discuss scoping with your team.
>
> **What the contract is:**
> - `MimirValidationRegistry` — an ERC-8004-shape on-chain anchor for off-chain signed attestations, plus a wiring for EigenLayer-style operator slashing on accepted fraud proofs.
> - ~3000 bytes runtime bytecode, Solidity ^0.8.20, optimizer runs=200.
> - Two operating modes (permissionless + AVS); the AVS mode talks to a generic `IServiceManager.isOperator()` + `ISlasher.slash()` interface intended to be wired against EigenLayer's `AllocationManager` on Holesky/mainnet.
> - Source: https://github.com/enchanter-ai/mimir
> - Audit-pinned commit: **v0.1.0** (see https://github.com/enchanter-ai/mimir/releases/tag/v0.1.0)
>
> **Engagement package** (please review before our kickoff call):
> - [`AUDIT_PREP.md`](https://github.com/enchanter-ai/mimir/blob/main/AUDIT_PREP.md) — threat model (8 adversaries), attack-surface map per file path, three suggested audit tiers with priced scope sketches.
> - [`PRODUCTION_READINESS.md`](https://github.com/enchanter-ai/mimir/blob/main/PRODUCTION_READINESS.md) — internal correctness audit (7 gaps closed).
> - [`anchor/contracts/`](https://github.com/enchanter-ai/mimir/tree/main/anchor/contracts) — Solidity sources.
> - [`anchor/go/`](https://github.com/enchanter-ai/mimir/tree/main/anchor/go) — Go client + 12 simulated-EVM tests against mocked EigenLayer interfaces.
> - Live deployment: `0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117` on Sepolia (bytecode strict-match-verified against v0.1.0).
>
> **Tier A scope we'd like to engage on:**
> - Full review of `MimirValidationRegistry.sol`, `IEigenLayer.sol` (interface conformance), and the Go client's ABI encoding / nonce handling.
> - Two-week engagement; deliverable is a signed report referencing the v0.1.0 commit SHA.
> - We'll address every BLOCKER + HIGH finding and re-engage you for verification.
>
> **Budget envelope** (per the AUDIT_PREP.md Tier A sketch): $30K–60K.
>
> **Timing:** We're targeting an engagement in the next 4–6 weeks; mainnet deploy is hard-gated on a passing report.
>
> Would your team have capacity? Happy to schedule a 30-minute scoping call. Best dates from us: <propose 3 slots>.
>
> Thanks,
> Enchanter Labs

---

## Email template (Tier B — full cryptographic correctness)

Same shell, with the Scope paragraph replaced by:

> **Tier B scope we'd like to engage on:**
> - Everything in Tier A, plus:
>   - All three RFC 8785 canonicalize impls (Go in `issuer/canonicalize/`, Rust in `spec/reference-impl-rust/src/lib.rs`, Python in `demo.py`) — byte-for-byte equivalence across Unicode normalization, number representation, nested-key ordering with mixed types, and the 12 adversarial vectors in `spec/test-vectors-adversarial/`.
>   - Ed25519 signing path correctness (AWS KMS-mode in `issuer/kms/aws.go`, the wire-faithful fake in `issuer/kms/aws_fake.go`, and the round-trip integration test in `issuer/kms_integration_test.go`).
>   - DPoP `ClientIdentityProof` extension implementation (RFC 9449) in `issuer/clientid/` — including the JWK thumbprint (RFC 7638) computation and the EdDSA / ES256 signature verification paths.
>   - Spec § 9, § 10, § 12 — every assertion in the verification algorithm.
> - Four-week engagement.
>
> **Budget envelope:** $80K–150K.

---

## What we expect to receive from the auditor

Per `AUDIT_PREP.md` § 10:

1. **Zero BLOCKER findings.** (Required for tag promotion.)
2. **Every HIGH finding fixed in main with referenced commit SHAs.**
3. **An "audit passed" PDF** referencing v0.1.0 (or a later tag we cut after fixes).
4. **At least 5 auditor-crafted adversarial vectors** added to `spec/test-vectors-adversarial/`.
5. **A public attestation page** at the auditor's domain (e.g. `https://trailofbits.com/reports/mimir-2026`) we can link from our README.

---

## After the engagement

| Step | What we do |
|---|---|
| Findings triage | Within 24h of report receipt, open one GitHub issue per finding with the auditor's severity attached. |
| Fix landing | Each fix as its own PR linking the issue; auditor verifies. |
| Tag bump | Cut `v0.2.0` (or `v1.0.0` if the audit closes Tier A + B) referencing the audit report URL in the release notes. |
| Mainnet | Once the audit report is public, deploy `MimirValidationRegistry` to Ethereum mainnet from the v1.0.0 tag. Etherscan source-verify on the same day. |

---

## Honest note for the auditor

The codebase was **AI-assisted to a significant degree** — disclosed in `AUDIT_PREP.md` § 6 with a per-module breakdown. We've been transparent about which modules a human reviewer has and hasn't covered. The Solidity contract is the highest priority because it's the un-reviewed-by-a-Solidity-human artifact with the highest blast radius (on-chain exposure + financial implications via slashing). We've front-loaded the test suite (14/14 simulated-EVM tests, 15/15 adversarial vectors) so the auditor's time can go to design review instead of basic-correctness chasing.

We're also happy to grant the auditor read-only access to our private design notes / decision log if helpful. Just ask.
