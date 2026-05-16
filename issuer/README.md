# MCP Provenance Issuer Service — MVP

HTTP service that receives a `tools/call` result and constructs a signed **Provenance Envelope** per [MCP Provenance spec v2.1 § 6](../../design/mcp-provenance-spec-v2.1.md).

## MVP constraints (read before deploying)

| Item | Status |
|------|--------|
| Signing key | **In-memory ephemeral** Ed25519 keypair. Regenerated on every restart. No KMS. |
| Scoring | **Stub** — always returns `validation_level: "cryptographically_valid"`. Real scoring engine integration is the next milestone. |
| `invoked_by` | **Stub** — always `did:enchanter:unverified` (v2.1 § 6.7 clause 3). Real caller authentication is a future milestone. |
| `tool_call_id` | **UUID stub** — generated server-side. Real call-ID comes from the MCP client (future). |
| Sources | **Stub** — single placeholder source. Replaced when the scoring engine is integrated. |
| Persistence | None. All state is transient. |

**DO NOT use the ephemeral key in production.** Verifiers will lose trust if the key rotates without notice.

## Quickstart

```bash
# 1. Fetch dependencies
make tidy

# 2. Start the server
make run
# Server listens on :8080 (override with ISSUER_PORT env var)

# 3. In another terminal, fire the demo request
make demo
```

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/attest` | Build and sign a Provenance Envelope |
| `GET` | `/v1/healthz` | Liveness check |
| `GET` | `/v1/key` | Active public key in JWK format |

### POST /v1/attest

Request body:

```json
{
  "request":      { ...tools/call parameters... },
  "result":       { ...tools/call result content... },
  "tool_id":      "did:web:example.com:tools:my-tool",
  "tool_version": "1.0.0"
}
```

Response:

```json
{
  "envelope": {
    "version":         "mcp-provenance/2026-05-13-ed25519",
    "tool_call_id":    "...",
    "tool_id":         "did:web:example.com:tools:my-tool",
    "tool_version":    "1.0.0",
    "invoked_at":      "2026-05-13T12:00:00Z",
    "invoked_by":      "did:enchanter:unverified",
    "request_digest":  "sha-256:<hex>",
    "result_digest":   "sha-256:<hex>",
    "sources":         [...],
    "signature": {
      "protected_header": { "alg": "Ed25519", "key_id": "ephemeral-..." },
      "value":            "<base64url-no-padding>"
    }
  },
  "validation_level": "cryptographically_valid"
}
```

## Running tests

```bash
make test
```

Tests cover:
- `TestEnvelopeRoundtrip` — full sign + external verify cycle
- `TestRequestDigestDeterministic` — same input → same digest (twice)
- `TestCanonicalFormOrderingIndependent` — `{a,b}` and `{b,a}` produce identical JCS bytes
- `TestCanonicalFormNestedSort` — recursive key sorting
- `TestEnvelopeFields` — all required v2.1 § 6 fields are populated

## Architecture

```
main.go                 — HTTP server, keypair init, route registration
types/types.go          — Wire-format structs (JSON tags match v2.1 spec verbatim)
envelope/builder.go     — BuildEnvelope: digest, assemble, canonicalize, sign
canonicalize/canonicalize.go  — RFC 8785 JCS (inline, no external dep)
issuer_test.go          — Unit + integration tests
sample/request.json     — Sample tools/call payload for `make demo`
```

## Crypto

- **Library**: `github.com/oasisprotocol/curve25519-voi` — production-audited, constant-time Ed25519 implementation.
- **Signing input**: RFC 8785 canonical JSON of the envelope with `signature.value` absent.
- **Signature encoding**: base64url, no padding.

## Next milestones

1. KMS integration (AWS KMS or Vault) — persistent key, rotation support.
2. Real scoring engine — replace stub sources with evidence-backed entries.
3. MCP client call-ID passthrough — replace UUID stub with client-supplied `tool_call_id`.
4. `invoked_by` authentication — verify caller DID before signing.
