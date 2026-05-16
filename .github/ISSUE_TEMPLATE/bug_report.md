---
name: Bug report
about: Report a defect in the issuer, scoring service, anchor contract, or any
  reference implementation.
title: "bug: <one-line description>"
labels: bug
---

<!--
Do NOT use this template for security vulnerabilities. See SECURITY.md and email
security@enchanter.ai instead.
-->

## What component
- [ ] Spec ([`spec/index.mdx`](../../spec/index.mdx))
- [ ] Issuer (Go HTTP service)
- [ ] Issuer — KMS layer (AWS / ephemeral / mock)
- [ ] Issuer — MCP schema (`/v1/attest-mcp`)
- [ ] Scoring (TypeScript)
- [ ] Anchor (Solidity contract)
- [ ] Anchor (Go client + simulated-EVM tests)
- [ ] Rust verifier
- [ ] TypeScript reference SDK
- [ ] Adversarial test vectors
- [ ] Bench
- [ ] Demo / MCP interop
- [ ] Docs

## What you did

<!-- Exact commands. Cut-and-paste from your terminal. -->

```
$ <your command>
```

## What you expected

<!-- e.g. "envelope signature should verify" -->

## What actually happened

<!-- Paste the full output / error. Trim only if very long. -->

```
<output>
```

## Reproduction harness used

Did you reproduce against a clean clone?

- [ ] Yes — `git clone git@github.com:enchanter-ai/mimir.git`
- [ ] No — describe your local mods

Commit SHA: `<paste the commit you tested>` (use `git rev-parse HEAD`)

## Environment

- OS: <e.g. Ubuntu 22.04, macOS 14.5, Windows 11>
- Go: <`go version` output>
- Node: <`node --version` output>
- Rust: <`rustc --version` output>
- Python: <`python --version` output>

## Additional context

<!-- Anything else: hypothesis about root cause, related issues, etc. -->
