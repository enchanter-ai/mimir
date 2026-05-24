# Mimir — Public Launch Checklist

Pre-launch readiness for going public with the Mimir provenance oracle.

---

## A. Code-side readiness (must be true before launch)

- [x] Spec v2.1 published (CC0)
- [x] Issuer Go service builds + passes all tests
- [x] Scoring TS service builds + passes typecheck
- [x] Anchor contract compiles + 14/14 simulated-EVM tests pass
- [x] Independent Rust verifier round-trips against the Go issuer
- [x] 15/15 adversarial vectors correctly handled (14 REJECT + 1 canonical-acceptance VERIFY)
- [x] σ-bound rubric empirically calibrated against 50-case labeled set (100% precision)
- [x] AUDIT_PREP.md published
- [x] ROADMAP.md published
- [x] LICENSE clear (Apache-2.0 code / CC0 spec)
- [ ] **SECURITY.md** — vulnerability disclosure policy + responsible-disclosure process (TODO before launch)
- [ ] **CONTRIBUTING.md** — how external contributors submit PRs / spec proposals (TODO before launch)
- [ ] **CODE_OF_CONDUCT.md** — community guidelines (TODO before launch)
- [ ] **Holesky deploy** — at least PERMISSIONLESS-mode contract live, Etherscan-verified

---

## B. Documentation readiness

- [x] Top-level `README.md` clearly answers "what does this do?" in 3 sentences
- [x] Quickstart section that gets a new contributor to "tests passing locally" in <5 minutes
- [x] `architecture.md` — system diagram + component map
- [x] `docs/stack-decision.md` — why each tech choice
- [x] `docs/hardening-roadmap.md` — known production gaps
- [x] `anchor/DEPLOY.md` — operator runbook for deploying the contract
- [x] `anchor/README.md` — anchor + EigenLayer wiring overview
- [x] `scoring/README.md` — scoring service overview
- [x] `issuer/README.md` — issuer service overview
- [x] `spec/reference-impl-ts/README.md` — TS SDK
- [x] `PRODUCTION_READINESS.md` — gap-closure evidence
- [x] `AUDIT_PREP.md` — audit-engagement package
- [ ] `docs/getting-started.md` — 15-min tutorial for "I want my MCP tool to ship signed envelopes" (TODO)
- [ ] `docs/deployments.md` — registry of live contract addresses across networks (TODO; will populate after first Holesky deploy)

---

## C. Brand + presentation

- [x] Enchanter Labs as canonical author
- [x] Logo on spec PDF cover
- [ ] **GitHub repo description + topics:**
  - Description: "Verifiable provenance for MCP tool-call results — signed envelopes + σ-bound quality scoring + on-chain anchoring with EigenLayer slashing."
  - Topics: `mcp`, `provenance`, `eigenlayer`, `ed25519`, `attestation`, `agent`, `ai-safety`, `cryptographic-verification`
- [ ] **GitHub repo About section:** link to spec PDF; link to PRODUCTION_READINESS.md
- [ ] **Social preview image** (1200×630 PNG; Mimir wordmark + tagline)
- [ ] **README badges** for: license, CI status, latest spec version, audit status
- [ ] **Demo video (3–5 min):** records the full pipeline from `python demo.py` through a real DEPLOY verdict, with voiceover
- [ ] **Animated diagram** for the homepage showing envelope flow: tool call → score → sign → verify → anchor

---

## D. Launch announcement channels

- [ ] **Blog post** on enchanter-ai.com — long-form: motivation, design, comparison table (Mimir vs JWT-based attestation vs ERC-8004 alone vs Mira)
- [ ] **HN submission** — Show HN format; honest about MVP status; link to spec PDF
- [ ] **Twitter / X thread** — 8-tweet narrative with screenshot of the σ-bound verdict
- [ ] **EigenLayer Discord announcement** — in #avs-builders
- [ ] **Anthropic MCP Discord** — in the MCP community channels (DMs to project leads first)
- [ ] **ETH Research forum post** — academic-flavored, focuses on the σ-bound consensus mechanism
- [ ] **Outreach to top 10 MCP server authors** — email/DM with the wiring guide + offer to help integrate

---

## E. Post-launch operational readiness

- [ ] **GitHub Discussions** enabled with three boards: `spec`, `implementations`, `operators`
- [ ] **`security@enchanter.ai`** mailbox routes to a real human within 24h
- [ ] **CI pipeline** — `foundations-verify.yml`-style workflow that runs the test trio (Go + Rust + adversarial) on every PR
- [ ] **Dependency monitoring** — Dependabot or Renovate enabled; alerts on CVE in `aws-sdk-go-v2`, `go-ethereum`, `@anthropic-ai/sdk`, `ed25519-dalek`
- [ ] **Issue templates** — bug report, feature request, spec clarification, security disclosure (private)
- [ ] **PR template** referencing CONTRIBUTING.md + AUDIT_PREP.md commit-SHA convention
- [ ] **Operator status page** — a dashboard showing active operators, total stake, last-N envelopes anchored

---

## F. Legal + IP

- [x] Apache-2.0 license on code (LICENSE root)
- [x] CC0 dedication on spec (LICENSE root § "spec/")
- [x] Authorship attributed to "Enchanter Labs" per the standing convention
- [ ] **Trademark check** — confirm "Mimir" is usable in the AI / web3 space (Mimir is also a Datadog product; we should clarify naming will not collide if Mimir becomes commercially adjacent)
- [ ] **Patent disclosure** — none filed; spec is CC0 so no patent grant required. Confirm there's no pending US/EU filing that would chill adoption.

---

## G. Pre-launch dry run

48 hours before public announcement:

- [ ] Run `python demo.py` on a fresh clone of `enchanter-ai/mimir`. It must end with `[OK] SIGNATURE VERIFIED`.
- [ ] Run `(cd anchor/go && go test ./...)` — 14/14 pass.
- [ ] Run `(cd spec/reference-impl-rust && cargo test)` — 6/6 pass.
- [ ] Run `python spec/test-vectors-adversarial/verify-all.py` — 15/15 PASS.
- [ ] Run `python scoring/calibration/poc_translate.py` with `ANTHROPIC_API_KEY` set — real DEPLOY verdict.
- [ ] Hit the Holesky-deployed contract via `anchor/cmd/verify` — full round-trip.
- [ ] Verify all linked URLs in README, ROADMAP, AUDIT_PREP resolve.
- [ ] Verify the spec PDF renders cleanly on iPhone / iPad / Chrome / Firefox.
- [ ] Verify the social-preview image looks correct when the repo URL is pasted into Twitter / Discord / LinkedIn.

---

## H. Day-of-launch sequence

1. **08:00 UTC** — final commit pushed to `main`; tag `v0.1.0`.
2. **08:30** — Etherscan link confirmed live + verified.
3. **09:00** — blog post goes live on enchanter-ai.com.
4. **09:05** — Twitter thread (scheduled).
5. **09:10** — HN submission.
6. **09:15** — Discord announcements (EigenLayer, MCP).
7. **09:30** — Direct outreach emails to top-10 integrators.
8. **All day** — monitor `security@enchanter.ai`, GitHub Issues, HN comments. Respond to security claims within 1 hour.

---

## I. Success metrics (first 30 days post-launch)

| Metric | Target |
|---|---|
| GitHub stars | ≥ 200 |
| External contributors (PRs merged) | ≥ 3 |
| Independent verifier impls (community-built) | ≥ 1 |
| Holesky-deployed contracts (community operators) | ≥ 3 |
| First external integration in the wild | ≥ 1 MCP server |
| Critical security disclosures received | 0 BLOCKER, ≤ 2 HIGH |
