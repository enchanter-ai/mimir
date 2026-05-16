# Contributing to Mimir

Mimir is an Apache-2.0 (code) + CC0 (spec) project maintained by [Enchanter Labs](https://github.com/enchanter-ai). External contributions are welcome — we maintain this guide so you can land a PR efficiently.

---

## What kind of contribution fits where

### Spec changes — go through Discussions first
The Provenance Envelope specification ([`spec/index.mdx`](spec/index.mdx)) is a Standards Track draft. Changing the wire shape, the canonical-form rules, or the validation algorithm affects every downstream implementation. For these:

1. **Open a GitHub Discussion** in the `spec` board with the proposed change and the reasoning.
2. Wait for maintainer feedback (usually within a week).
3. Once consensus is reached, send the PR with the spec change AND updates to both reference implementations ([`spec/reference-impl-ts/`](spec/reference-impl-ts/) and [`spec/reference-impl-rust/`](spec/reference-impl-rust/)) AND the Go canonicalize/envelope code.

### Implementation bugs — file an issue, then PR
Bugs in the Go issuer, TypeScript scoring service, Solidity contracts, or any reference impl:

1. Open an issue with a **minimal reproducer**. The reproduction harness from [`AUDIT_PREP.md`](AUDIT_PREP.md) § 8 covers most paths.
2. PR the fix with a test that fails on `main` and passes with your change.

### New verifier implementations — PRs welcome
A Java, Python, C, browser-JS, or any-other-language verifier of the envelope spec is a high-value contribution. Such impls live alongside the Rust verifier under `spec/reference-impl-<lang>/`. Drop them anywhere logical.

### Documentation — direct PRs OK
Fixing typos, clarifying explanations, adding tutorials: send directly. No issue required.

### Security findings — DO NOT open a public issue
See [`SECURITY.md`](SECURITY.md). Use `security@enchanter.ai`.

---

## Development setup

```bash
git clone git@github.com:enchanter-ai/mimir.git
cd mimir
```

Prerequisites by component:

| Component | Tooling |
|---|---|
| Issuer (Go) | Go 1.22+ |
| Scoring (TypeScript) | Node 22+, npm |
| Anchor (Solidity) | Node + npm (for `solc-js` via `anchor/compile.js`); no Foundry needed |
| Anchor (Go client + tests) | Go 1.22+, CGO disabled (`CGO_ENABLED=0`) |
| Rust verifier | Rust 1.75+ via rustup |
| Adversarial vectors + demo | Python 3.11+, `pip install pynacl requests aiohttp` |
| Real-Claude calibration | + `ANTHROPIC_API_KEY` env var |

---

## The test contract

Every PR must keep these tests green:

```bash
# Issuer + KMS + MCP schema
(cd issuer && go test ./...)

# Anchor (contract + EigenLayer wiring)
(cd anchor/go && CGO_ENABLED=0 go test ./...)

# Independent Rust verifier
(cd spec/reference-impl-rust && cargo test)

# Adversarial attack vectors
python spec/test-vectors-adversarial/verify-all.py

# (If you touched scoring/) — type-check
(cd scoring && npx tsc --noEmit)
```

CI runs all of these on every PR (see [`.github/workflows/tests.yml`](.github/workflows/tests.yml)).

If your PR touches the contract source, **also** regenerate the embedded bytecode:

```bash
cd anchor
npm install --no-save solc@0.8.20    # once
node compile.js                       # writes go/abi/*.{json,bin}
```

Commit the regenerated ABI + bytecode alongside the source change.

---

## Commit message convention

Conventional Commits. Type prefixes used in this repo:

| Type | When |
|---|---|
| `feat` | New feature visible to a consumer |
| `fix` | Bug fix |
| `refactor` | Code change with no behavior change |
| `docs` | Documentation only |
| `test` | Test-only changes |
| `chore` | Build / tooling / dep bumps |
| `perf` | Performance improvement |
| `revert` | Reverts a prior commit |

Optional scope per repo convention: `issuer(kms): ...`, `scoring(rubric): ...`, `anchor(sol): ...`, `spec(ref-rust): ...`.

**Subject line:** ≤ 80 chars, imperative mood ("add X", not "added X" or "adds X"). Body wrapped at 72 chars if present.

---

## PR review checklist (the maintainer will tick these)

- [ ] Issue/Discussion referenced for non-trivial changes
- [ ] Tests added or updated; CI passes
- [ ] If spec changed: both reference impls updated; canonical form unchanged OR explicitly versioned
- [ ] If contract changed: bytecode regenerated and committed; 12/12 anchor tests still pass
- [ ] If a public API surface changed: README + the relevant module's README updated
- [ ] Commit messages follow the convention above
- [ ] No secrets, no API keys, no Holesky/mainnet private keys committed (the `.gitignore` excludes `.env*`; check your diff)

---

## Style + conventions

- **Go:** `gofmt`-clean. Standard `golang.org/x/lint`-style naming. Errors wrapped with `fmt.Errorf("op: %w", err)`. Avoid panics in non-test code.
- **TypeScript:** strict mode. Zod for runtime validation at trust boundaries. `pino` for structured logs.
- **Solidity:** ^0.8.20. Custom errors (not `require` strings) for revert paths. Storage layout documented inline. No external deps beyond what's already vendored.
- **Rust:** `cargo fmt`-clean. `thiserror` for error types. No `unwrap()` in library code.

---

## What we WILL NOT accept

- **Code without tests.** Every PR that adds behavior adds a test.
- **Mock-only "fixes" to genuine bugs.** If a test passes only because the mock was loosened, that's a regression dressed as a fix.
- **Spec changes that fork the canonical form** without bumping the spec version.
- **Cosmetic-only commits in critical paths** (canonicalize, signing, verify) without a clear rationale — these are review-cost amplifiers in security-critical code.
- **Bundled changes.** Each PR does one thing. Spec change + impl change + docs is fine; spec change + unrelated refactor + new feature is not.

---

## Getting feedback before sending

If you're not sure your idea fits, open a Discussion first. Maintainer response usually within a week. We'd rather give early "go / no-go" than have you spend a weekend on something that won't land.

---

## License + attribution

By submitting a PR you confirm your work is original (or you have rights to it) and you grant the project the right to distribute it under the Apache-2.0 (code) or CC0 (spec) license, as applicable to the file you're touching. The repo's LICENSE file is authoritative.

Contributor credit is via the commit history — we do not maintain a separate AUTHORS file.

---

## Contact

- Maintainer: [Enchanter Labs](https://github.com/enchanter-ai)
- Discussions: <https://github.com/enchanter-ai/mimir/discussions>
- Security: `security@enchanter.ai` ([`SECURITY.md`](SECURITY.md))
