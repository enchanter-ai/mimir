# Mimir — Security Policy

## Reporting a vulnerability

If you believe you've found a security issue in Mimir, **do not open a public GitHub issue.** Instead:

1. **Email:** `security@enchanter.ai` (PGP key fingerprint published at <https://github.com/enchanter-ai/.well-known>).
2. **Subject line:** `MIMIR-SEC: <short description>`.
3. **Body:** include the impact, reproduction steps, affected commit SHA, and your preferred coordinated-disclosure timeline.

We acknowledge new reports within **24 hours** (business days) and aim to confirm the issue and provide a fix or mitigation within **14 days** for HIGH/CRITICAL, **30 days** for MEDIUM, **90 days** for LOW.

---

## What counts as in-scope

### Always in-scope
- **Cryptographic correctness** — signature forgery, key extraction, replay-attack bypass, canonical-form ambiguity (Go ↔ Rust ↔ TypeScript impls disagree on the same envelope).
- **Solidity contract** ([`anchor/contracts/MimirValidationRegistry.sol`](anchor/contracts/MimirValidationRegistry.sol)) — any path to forging a registration, slashing an honest operator, or bypassing operator gating.
- **Issuer HTTP service** — input-validation failures, panic-on-malformed-input, KMS key misuse, JWK publishing race conditions.
- **Scoring service** — prompt-injection paths that flip a HOLD-tier result to DEPLOY, or vice versa.
- **AWS KMS integration** ([`issuer/kms/aws.go`](issuer/kms/aws.go)) — any deviation from the documented AWS API contract that could surface as a verifiability failure.

### Sometimes in-scope (escalate first)
- **Dependency vulnerabilities** — only when a viable exploit chain exists in Mimir. Pure CVE reports without an exploit path are tracked but not bounty-eligible. Send these to Dependabot via a PR instead.
- **Operational gaps** — missing rate limits, missing observability, missing JWK-rotation procedure. Important, but tracked as `ops` issues rather than `security`.

### Out-of-scope
- **DDoS / rate limiting** at the HTTP layer. Mimir assumes the operator places a WAF / CDN in front of the issuer.
- **Denial of service via expensive scoring requests.** The scoring service is a paid-API consumer; operators must implement their own rate limits.
- **Findings against the mock contracts** ([`anchor/contracts/MockServiceManager.sol`](anchor/contracts/MockServiceManager.sol), [`MockSlasher.sol`](anchor/contracts/MockSlasher.sol)). These are explicitly do-not-deploy and have no access control by design.
- **Anthropic / Claude model behavior.** Out of scope as the upstream judge; report to Anthropic directly.
- **EigenLayer core contracts.** Audited separately by Layr-Labs.

---

## Responsible disclosure

We commit to:

1. **Acknowledging your report** within 24 business hours.
2. **Naming you in the fix announcement** unless you prefer otherwise.
3. **Not pursuing legal action** for good-faith research that follows this policy.
4. **Coordinating CVE assignment** for HIGH/CRITICAL findings.
5. **Disclosing the issue publicly** at the same time the fix lands in `main`, unless coordinated disclosure has been agreed otherwise.

We ask in return:

1. **Do not run automated exploits against live deployments** without prior coordination.
2. **Do not disclose publicly** before the 90-day window expires (or sooner if a fix has landed).
3. **Do not access, modify, or destroy data** belonging to other parties.
4. **Provide reasonable detail** so we can reproduce and fix.

---

## Bounty

There is **no formal bug-bounty program yet.** Bounty eligibility and amounts will be decided post-mainnet-deploy, after the first audit lands. Until then, we will:

- Send a public attribution + thank-you in the fix commit.
- Add your name to a future `SECURITY_HALL_OF_FAME.md`.
- Forward severe findings to the appropriate auditor (Trail of Bits, OpenZeppelin, etc.) under their disclosure programs if they have one.

---

## Audit status

The codebase has **not yet been audited by a third party**. See [`AUDIT_PREP.md`](AUDIT_PREP.md) for the engagement package and [`PRODUCTION_READINESS.md`](PRODUCTION_READINESS.md) for what's been internally verified.

---

## Reproduction harness

Auditors and security researchers can reproduce the full test surface in ~3 minutes:

```bash
git clone git@github.com:enchanter-ai/mimir.git
cd mimir

(cd issuer && go test ./...)                            # all PASS
(cd anchor/go && CGO_ENABLED=0 go test ./...)            # 12/12 PASS
(cd spec/reference-impl-rust && cargo test)              # 6/6 PASS
python spec/test-vectors-adversarial/verify-all.py       # 12/12 PASS
```

See [`AUDIT_PREP.md`](AUDIT_PREP.md) § 8 for the full harness.

---

## Contact

- **Security:** `security@enchanter.ai`
- **General:** open a [GitHub Discussion](https://github.com/enchanter-ai/mimir/discussions)
- **Maintainer:** [Enchanter Labs](https://github.com/enchanter-ai)
