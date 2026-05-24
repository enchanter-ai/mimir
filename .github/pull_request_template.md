<!--
Thanks for sending a PR! Please fill in the sections below. Skip those that
don't apply. Trivial PRs (typo fix, link update, dep bump) can leave most
sections blank.

For non-trivial spec changes, an issue/Discussion thread is required first.
See CONTRIBUTING.md.
-->

## Summary

<!-- One sentence describing what this PR does. -->

## What changed

<!-- Bullet list of concrete changes. File-by-file if it helps. -->

## Why

<!-- The motivation: what does this enable, what does it fix, what risk
     does it mitigate? -->

## Test plan

<!-- How did you verify the change is correct? Specific commands, paste the
     output if helpful. CI will also run the full test trio. -->

- [ ] `cd issuer && go test ./...` passes
- [ ] `cd anchor/go && CGO_ENABLED=0 go test ./...` passes (14/14)
- [ ] `cd spec/reference-impl-rust && cargo test` passes (6/6)
- [ ] `python spec/test-vectors-adversarial/verify-all.py` passes (15/15)
- [ ] Added a test that fails on `main` and passes with this change (for bug fixes / new features)

## Surfaces touched

- [ ] Spec (`spec/index.mdx`)
- [ ] Issuer (Go HTTP service)
- [ ] Issuer KMS layer
- [ ] Scoring service (TypeScript)
- [ ] Anchor contracts (Solidity)
- [ ] Anchor Go client
- [ ] Rust verifier
- [ ] Adversarial test vectors
- [ ] Bench
- [ ] Docs only
- [ ] CI / tooling only

## Spec impact

- [ ] No spec change
- [ ] Spec change with backward-compatible canonical form (no version bump needed)
- [ ] Spec change that breaks canonical form (REQUIRES version bump + Discussion link)
- [ ] Both reference impls updated (`spec/reference-impl-ts`, `spec/reference-impl-rust`)

## Contract impact

- [ ] No contract change
- [ ] Contract changed; ran `cd anchor && node compile.js` and committed regenerated `anchor/go/abi/*.{json,bin}`
- [ ] Anchor tests still 14/14

## Security / audit relevance

- [ ] No security-relevant code touched
- [ ] Security-relevant code touched but no behavioral change (refactor only)
- [ ] Behavioral change in security-relevant code — see CHANGELOG below

## Checklist

- [ ] Commit messages follow Conventional Commits convention (see CONTRIBUTING.md)
- [ ] No secrets / API keys / private keys / mainnet addresses in the diff
- [ ] Updated `README.md` / `ROADMAP.md` / `docs/` if a public surface changed
- [ ] Linked the relevant issue or Discussion

## Linked issues

<!-- Closes #123, Refs #456, etc. -->

## Notes for the reviewer

<!-- Anything subtle, intentionally controversial, or temporary. -->
