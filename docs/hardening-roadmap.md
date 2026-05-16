# T3 — Production-Hardening Roadmap for the TypeScript Reference Implementation

*Author: Enchanter Labs — 2026-05-13*

---

## Executive summary

The c3 TypeScript reference implementation (`@modelcontextprotocol/provenance-envelope-reference` v0.1.0-draft) correctly implements the core cryptographic pipeline: RFC 8785 JCS canonicalization, SHA-256 request/result digests, Ed25519 and ECDSA P-256 signing and verification via `@noble/ed25519` and `@noble/curves/p256`, three-level progressive validation (well-formed → cryptographically valid → trust-anchored), and a full VE/PE/RE error-code taxonomy. It is suitable for spec conformance testing and integration harness work. It is not suitable as the Quality Oracle issuer's signing core. The gaps fall into four classes: security-critical omissions that create exploitable attack surface (strict JSON parsing, constant-time audit, memory hygiene, DoS hardening), missing production infrastructure (KMS integration, rate-limit handling, multi-issuer quorum, DID cache TTL enforcement, TLS/DNSSEC source-hash fetching), incomplete spec coverage (missing `provenance/get` and `provenance/list` handlers, no `ClientIdentityProof` verification, no CBOR path, no `issuer_signature` verification on trust-anchor records), and test gaps (no test-vector replay, no fuzzer, low error-code coverage). Closing all BLOCKER and HIGH gaps requires approximately 35–55 eng-days; the full list through LOW is 60–90 eng-days, parallelizable across three tracks.

---

## Gap inventory (sorted by severity)

### Gap 1 — Strict JSON parser rejecting duplicate object members

- **Severity:** BLOCKER
- **Current state:** `canonicalize.ts` explicitly documents the gap: `JSON.parse()` silently overwrites duplicate keys before any duplicate-detection logic can fire. An envelope with `"tool_id": "legitimate", "tool_id": "attacker"` passes Level 1 validation with no error, allowing the VE-011 exploit described in § 15.12.
- **Required state:** Any input path that accepts an envelope as a JSON string (HTTP body, `provenance/get` response, test fixture loader) MUST parse with a strict parser that throws on duplicate members at any nesting level. Rejection must emit `VE-011`. The check must fire before canonicalization.
- **Spec reference:** § 10.1 step 1, § 15.12, VE-011
- **Effort estimate:** S (< 2 eng-days)
- **Proposed approach:** Replace bare `JSON.parse` call-sites with a thin wrapper using a tracking reviver: on each key encountered, store `[path, key]` in a `Set`; on collision, throw `ValidationError(VECode.DuplicateMember)`. Alternatively use `json-parse-even-better-errors` or a streaming parser (e.g. `clarinet`) with duplicate-key events. The reviver approach has zero extra dependencies and is easiest to audit. Apply the wrapper at every boundary where untrusted JSON enters: `verify()` input, `RegistryClient.sendRpc()` response parse, and the optional `provenance/verify` method handler.
- **Dependencies:** None
- **Acceptance criteria:** The three test vectors `t-dup-01` (top-level), `t-dup-02` (nested in `signature`), `t-dup-03` (nested in `source`) each return `{ level: "invalid", errors: ["VE-011"] }`. No false positives on valid envelopes.

---

### Gap 2 — Real keypair generation and KMS-backed signing

- **Severity:** BLOCKER
- **Current state:** `produce()` accepts a `SigningKey` with `privateKey: Uint8Array` — raw private key bytes passed from the caller. There is no key-generation helper, no KMS abstraction, and no HSM integration. `types.ts` exposes `Ed25519SigningKey.privateKey` as a plain byte array. The README notes this as Gap 3.
- **Required state:** Production issuer nodes MUST NOT hold private key bytes in process memory. Signing must be delegated to an audited KMS (AWS KMS, GCP Cloud HSM, Azure Key Vault, or an on-prem PKCS#11 HSM). The SDK must expose a `SignerInterface` (or equivalent abstract class) so that `produce()` calls `signer.sign(canonical_bytes)` rather than performing raw `ed25519.sign(...)` inline. A reference implementation of the interface backed by `@noble/ed25519` in a test KeyVault is acceptable for integration tests, but production wiring must use the interface boundary.
- **Spec reference:** § 15.5 (Key Compromise mitigation requires HSM-grade key storage); § 9.3 step 8
- **Effort estimate:** M (2–5 eng-days) for the interface abstraction and a test-signer. KMS adapter implementation depends on T2's key-management recommendation; estimate separately per chosen backend (likely M per backend).
- **Proposed approach:** Define `interface KmsSigner { sign(message: Uint8Array): Promise<Uint8Array>; keyId: string; alg: SignatureAlgorithm; }`. Refactor `produce()` to accept `KmsSigner` instead of `SigningKey`. Provide `NobleEd25519Signer` and `NoblePp256Signer` wrappers for test use. Ship a `@enchanterlabs/provenance-kms-aws` optional package as a separate module once T2 picks AWS KMS.
- **Dependencies:** T2 tech-stack recommendation for key management layer
- **Acceptance criteria:** `produce()` type signature accepts `KmsSigner`. Raw `SigningKey` with `privateKey: Uint8Array` is removed from the public API or deprecated behind a `TestOnlySigner` guard. The integration test suite passes using `NobleEd25519Signer`. No private key bytes appear in heap profiler output during a sign operation.

---

### Gap 3 — Constant-time signature verification audit

- **Severity:** BLOCKER
- **Current state:** `verify.ts` line 239 carries the comment "constant-time per § 15.4 — noble libraries use constant-time ops." This is an assertion, not a verified audit. The ECDSA P-256 path in `verify.ts` reconstructs a `{ r, s }` object from raw `BigInt` values via `Buffer.from(...).toString("hex")` before passing to `p256.verify`. The intermediate `BigInt` construction is variable-length and may produce timing side-channels before the library's constant-time inner loop runs.
- **Required state:** A formal audit of every comparison in the verification hot path confirming no early-exit branches exist on signature material. The P-256 raw-to-struct reconstruction must be rewritten to avoid branching on key/signature bytes. The audit result must be documented in a `security/CONSTANT-TIME-AUDIT.md` file so future maintainers do not regress it.
- **Spec reference:** § 15.4
- **Effort estimate:** S–M (1–3 eng-days including audit + rewrite of P-256 reconstruction path)
- **Proposed approach:** Replace the `BigInt("0x" + ...)` reconstruction with a direct `Uint8Array` pass to `p256.verify` using the `@noble/curves` low-level API that accepts raw bytes (avoiding BigInt conversion). Verify with `process.hrtime.bigint()` timing tests run under constant payload length. Add a CI job that runs the timing test suite (e.g. `dudect-node`) on the verify path for each algorithm.
- **Dependencies:** Gap 1 (must close the parser gap before fuzzing the verify path)
- **Acceptance criteria:** Timing test suite shows no statistically significant latency variation correlated with signature byte values across 1 million samples per algorithm. Audit document committed to `security/`.

---

### Gap 4 — Memory-safe handling of signature material (zeroize after use)

- **Severity:** BLOCKER
- **Current state:** `produce.ts` holds the private key in `signingKey.privateKey` (a `Uint8Array`) and the computed `signatureBytes` in a local variable for the duration of the function call. Neither is zeroed after use. In Node.js, GC-managed `Uint8Array` backing stores can linger in heap for indefinite periods, exposing key material in crash dumps, heap snapshots, and coredumps.
- **Required state:** After signing, `signatureBytes` MUST be zeroed in place. If the signer interface (Gap 2) is adopted, raw private key bytes will no longer be in-process; this gap then reduces to zeroing the intermediate signature buffer. Both must be addressed.
- **Spec reference:** § 15.5 (key compromise mitigation); general cryptographic engineering practice
- **Effort estimate:** S (< 1 eng-day once Gap 2 interface is in place)
- **Proposed approach:** After `base64urlEncode(signatureBytes)` returns, call `signatureBytes.fill(0)`. For the `KmsSigner` path (Gap 2) this is the only in-process secret material. Add a `SecureBuffer` wrapper that calls `fill(0)` in a `finally` block. Node.js does not expose `mlock`; document this limitation explicitly.
- **Dependencies:** Gap 2 (KMS interface adoption)
- **Acceptance criteria:** Code review confirms every `Uint8Array` holding signature bytes or raw key material has a `fill(0)` on all exit paths (normal and exceptional). Verified by grep in CI.

---

### Gap 5 — Reject envelopes exceeding a size cap (DoS hardening)

- **Severity:** BLOCKER
- **Current state:** `verify()` accepts `envelope: unknown` with no size check. A malicious caller can pass a 100 MB JSON object; canonicalization and SHA-256 hashing will process it before rejection, exhausting CPU and memory.
- **Required state:** `verify()` and any JSON parsing boundary MUST reject inputs that exceed a configurable size limit before performing any parsing or cryptographic work. Default cap: 64 KB (well above any legitimate envelope; spec envelopes are typically < 4 KB).
- **Spec reference:** § 15 (DoS hardening, implied); general operational security
- **Effort estimate:** S (< 1 eng-day)
- **Proposed approach:** Add a `maxEnvelopeSizeBytes` option to `VerifyOptions` (default: 65536). When the input is a `string`, check `string.length * 2` (worst-case UTF-16 bytes) before parsing. When already an object, serialize to estimate size if needed, or apply the cap at the HTTP transport boundary in the issuer server. Emit `VE-001` (profile unsupported is a reasonable proxy; alternatively define an operational error code outside the spec taxonomy and document the extension).
- **Dependencies:** None
- **Acceptance criteria:** `verify()` called with an object whose JSON serialization exceeds the cap returns `{ level: "invalid" }` in < 1 ms. Load test at 100 req/s with 1 MB payloads shows no OOM or latency spike above 5 ms.

---

### Gap 6 — Multi-issuer K-of-N median implementation in Trust-Anchored validation

- **Severity:** HIGH
- **Current state:** `verify()` accepts a single `RegistryLookupResponse` via `options.trustAnchorRecord`. The multi-issuer logic from § 12.6 (K-of-N, independent `issuer_signature` verification per registry, single-revocation-invalidates-all rule, median `issued_at`) is entirely absent. The README documents this as Gap 4.
- **Required state:** A `MultiIssuerVerifier` that accepts an array of `RegistryLookupResponse` objects, independently verifies each `issuer_signature`, enforces K-of-N quorum (configurable K and N, default K=2, N=3), applies the single-revocation veto, and computes the median `issued_at`. The existing `verify()` single-record path remains for backward compatibility but must be marked as providing only single-registry (K=1) trust guarantees.
- **Spec reference:** § 12.6, § 10.3
- **Effort estimate:** M (3–5 eng-days)
- **Proposed approach:** Add `multiIssuerTrustAnchorRecords?: RegistryLookupResponse[]` to `VerifyOptions`. When present and length ≥ K, run the multi-issuer path. `issuer_signature` verification requires resolving each registry's key from the `issuer` DID in the record — this depends on the DID resolver (Gap 7), so stub with a caller-supplied `resolveIssuerKey` callback initially. Implement median `issued_at` as `records[Math.floor(K/2)].record.issued_at` after sorting K agreeing records by `issued_at`. Enforce the veto: if any record in the array is a revocation with `revoked_at ≤ envelope.invoked_at`, return `cryptographically_valid` with a revocation warning regardless of K quorum.
- **Dependencies:** Gap 7 (issuer_signature verification requires DID resolution); Gap 1 (strict parser must run on registry responses too)
- **Acceptance criteria:** Unit tests: K=2/N=3 with 2 valid records passes; K=2/N=3 with 1 valid + 1 revocation fails to reach trust_anchored; K=2/N=3 with same `registry_id` from two endpoints counts as K=1. Integration test against mock registries with forged `issuer_signature` is rejected.

---

### Gap 7 — Source-hash fetching with TLS/DNSSEC pinning per § 15.13

- **Severity:** HIGH
- **Current state:** `verify.ts` Level 3 loop emits a warning for each `sources[i].hash` present but does not fetch the URL or verify the hash. The README documents this as Gap 5.
- **Required state:** When `sources[i].hash` is present and the Consumer has opted into Level 3 source-hash verification, the SDK MUST fetch `sources[i].url` over TLS with certificate verification enforced, compute the declared hash algorithm over the response body, and compare. For `did:web` source URLs, DNSSEC validation SHOULD be performed when available. Hash mismatch must be surfaced as a non-fatal warning per § 10.3 step 5. An opt-in flag (`verifySourceHashes: boolean`) controls whether fetches occur; default false for offline environments.
- **Spec reference:** § 10.3 step 5, § 15.13, § 6.10
- **Effort estimate:** M (2–4 eng-days for the fetch-and-verify loop; DNSSEC integration is L if pursued)
- **Proposed approach:** Implement a `SourceHashVerifier` class that accepts a `fetcher` (defaulting to `globalThis.fetch`) and a `dnsResolver` (optional). For each source with a `hash`, call `fetcher(src.url, { signal: AbortSignal.timeout(5000) })`, read the body as `Uint8Array`, compute `sha256(body)`, and compare. Use Node's built-in TLS verification (enabled by default). DNSSEC: optionally integrate `dns.promises.resolve` with the `dnssec` option available in Node >= 22, or accept a caller-supplied resolver. Emit structured warnings into `ValidationOutcome.warnings` with the source URL and mismatch details.
- **Dependencies:** Gap 5 (size cap must apply to fetched source bodies too)
- **Acceptance criteria:** Test with a mock server returning correct and incorrect content. Correct hash produces no warning. Incorrect hash produces a structured warning (not an error). Fetch timeout of 5 s is enforced. TLS certificate errors cause the source to be treated as unverified (warning, not error).

---

### Gap 8 — DID document cache freshness enforcement per § 15.13 (`did_cache_ttl`)

- **Severity:** HIGH
- **Current state:** The `resolveKey` callback in `verify()` is entirely caller-supplied. There is no caching layer, no TTL enforcement, and no cache-invalidation logic in the SDK. Callers can supply a no-op resolver that always returns a stale key; the SDK cannot detect this.
- **Required state:** The SDK must ship a `DIDResolver` class that caches resolved DID documents in memory (or optionally to a persistent store), enforces a `did_cache_ttl` (default 300 seconds per § 15.13), and refreshes stale entries before returning a key. The resolver must support `did:web` (HTTPS fetch of `/.well-known/did.json`) and `did:key` (inline decode). The `resolveKey` callback contract must be updated to accept a `DIDResolver` instance.
- **Spec reference:** § 15.13 (`did_cache_ttl`), § 10.2 step 4–7, VE-003
- **Effort estimate:** M (3–5 eng-days)
- **Proposed approach:** Implement `DIDResolver` backed by an `LRUCache<string, { document: DIDDocument; fetchedAt: number }>`. On `resolve(did)`, check if `Date.now() - entry.fetchedAt > did_cache_ttl * 1000`; if stale or missing, re-fetch. For `did:web`, build the URL from the DID components and fetch with TLS verification. For `did:key`, decode inline. Expose `DIDResolver` as a concrete implementation of a `KeyResolver` interface; callers can still supply their own. Default `did_cache_ttl = 300`.
- **Dependencies:** Gap 7 (TLS enforcement policy is shared)
- **Acceptance criteria:** Cache hit path returns key without network call. After TTL expiry, resolver re-fetches. Mock a DID document rotation mid-test; verify the resolver picks up the new key after TTL. VE-003 is returned when the `key_id` is absent from the resolved document.

---

### Gap 9 — Rate-limit handling per § 12.7 RE-006

- **Severity:** HIGH
- **Current state:** `registry-client.ts` maps JSON-RPC error codes to `RECode` values, so RE-006 will be correctly thrown as a `RegistryError(RECode.RateLimited, ...)`. However, there is no retry-with-backoff logic, no `Retry-After` header parsing, no request queue, and no concurrency limiter in `RegistryClient`. In the Quality Oracle's high-throughput path (100 attestations/sec target), naive parallel registry lookups will immediately hit rate limits.
- **Required state:** `RegistryClient` must implement exponential backoff with jitter on RE-006 responses, parse the `Retry-After` value from `error.data` when present, and expose a configurable `maxRetries` and `baseDelayMs`. A token-bucket or leaky-bucket request queue should cap concurrent outbound registry calls. The backoff state must be per-endpoint (multiple registries can have independent limits).
- **Spec reference:** § 12.7 RE-006; § 12.6 (multi-issuer requires parallel registry calls, amplifying this)
- **Effort estimate:** M (2–4 eng-days)
- **Proposed approach:** Wrap `sendRpc` in a retry loop. On `RegistryError(RECode.RateLimited)`, extract `retryAfterMs` from `error.data`, sleep `retryAfterMs ?? (baseDelayMs * 2^attempt + jitter)`, then retry. Add `RateLimiter` (token-bucket) using `async-sema` or a hand-rolled implementation. Expose `maxConcurrent` and `maxRetries` on `RegistryClientOptions`.
- **Dependencies:** None (can be built in parallel with other work)
- **Acceptance criteria:** Under simulated RE-006 responses, client backs off correctly and succeeds on retry within `maxRetries`. Concurrent call count never exceeds `maxConcurrent`. Unit test confirms `Retry-After: 2000` from error data is respected.

---

### Gap 10 — `issuer_signature` verification on trust-anchor and revocation records

- **Severity:** HIGH
- **Current state:** `verify.ts` Level 3 checks the trust-anchor record's `found`, `revocation`, and time-window fields, but never calls `issuer_signature` verification on the `TrustAnchorRecord` or `RevocationRecord` returned by the registry. An attacker who can MITM the registry response can swap in an arbitrary trust-anchor record with a forged `tool_id`; the SDK accepts it.
- **Required state:** Before relying on any `TrustAnchorRecord` or `RevocationRecord`, the SDK MUST verify `issuer_signature` against the registry's own public key. The registry's key is obtained from its `registry.json` discovery document (§ 12.8), which must be fetched and cached separately from lookup responses. This is the critical missing enforcement step for Level 3 to be meaningful.
- **Spec reference:** § 12.2 validity constraints, § 12.3 validity constraints, § 12.8
- **Effort estimate:** M (3–5 eng-days, including discovery document cache)
- **Proposed approach:** Add a `RegistryKeyStore` that caches `registry.json` documents by `registry_id` with a 24 h TTL. On Level 3 verification, retrieve the registry's `registry_key_jwk`, reconstruct the record without `issuer_signature`, canonicalize via RFC 8785, and verify using the appropriate algorithm. Re-use the same `@noble` verification stack already in `verify.ts`. Wire this into `MultiIssuerVerifier` (Gap 6).
- **Dependencies:** Gap 6 (multi-issuer path is the primary consumer), Gap 8 (DID resolution for issuer key)
- **Acceptance criteria:** A forged `TrustAnchorRecord` with a manipulated `tool_id` but intact `issuer_signature` from a different record is rejected. A correctly issued record verifies. Test with a mock registry key.

---

### Gap 11 — `provenance/get` and `provenance/list` protocol method handlers

- **Severity:** HIGH
- **Current state:** The SDK implements `produce()` and `verify()` as library functions. There are no MCP JSON-RPC method handlers for `provenance/get` (§ 7) or `provenance/list` (§ 7). For the Quality Oracle, the issuer node must serve these methods so consumers can re-fetch envelopes by `tool_call_id`.
- **Required state:** A `ProvenanceStore` interface (in-memory reference implementation, pluggable for Redis/Postgres backends) that stores signed envelopes keyed by `tool_call_id`. JSON-RPC handler functions for `provenance/get` (returns envelope or PE-002a/PE-002b/PE-003) and `provenance/list` (returns paginated `tool_call_ids` ordered by `invoked_at`, cursor-stable). The handlers must emit the correct `error.data` shape per § 13.
- **Spec reference:** § 7 (`provenance/get`, `provenance/list`); § 13 (PE-002a, PE-002b, PE-003, PE-004 `error.data` shape)
- **Effort estimate:** M (3–5 eng-days)
- **Proposed approach:** Implement `InMemoryProvenanceStore` with a `Map<string, ProvenanceEnvelope>`. Cursor: base64url-encoded `{ offset: number; sessionId: string }`. Handlers are thin wrappers that call the store and return properly shaped JSON-RPC responses. The store interface is the extension point; the Quality Oracle will wire Redis/Postgres here.
- **Dependencies:** Gap 1 (store serialization must use strict JSON)
- **Acceptance criteria:** `provenance/get` with a known `tool_call_id` returns the envelope. `provenance/get` with an unknown ID returns PE-002a. `provenance/list` returns pages in `invoked_at` ascending order. Cursor from page N retrieves page N+1 correctly after new envelopes are added.

---

### Gap 12 — Error-code completeness: VE-005, VE-012, VE-013 not checked; `error.data` shape missing

- **Severity:** HIGH
- **Current state:** Reviewing `verify.ts`:
  - `VE-005` (`invoked_by_mismatch`) has no check; the `invoked_by` field is read but only validated as a non-empty string, not against the caller's known agent identity.
  - `VE-012` (`unknown_field_stripped`) is defined in `errors.ts` but never emitted; there is no stripped-field detection.
  - `VE-013` (`session_id_mismatch`) is defined but never emitted; session-binding is not implemented.
  - In `verify.ts`, three required string fields (`tool_version`, `invoked_at`, `invoked_by`) emit ad-hoc strings like `"VE-field:tool_version"` rather than proper typed `VECode` values.
  - `ProducerError` and `ValidationError` lack the structured `error.data` shape (§ 13) needed for wire-compatible JSON-RPC error responses.
- **Required state:** All 13 VE codes and 5 PE codes must be emittable on the correct condition. The three ad-hoc strings must be replaced with correct `VECode` assignments. The `ProducerError` class must serialize to a JSON-RPC `error.data` object per the § 13 shape. An `invoked_by` check option must be added to `VerifyOptions`.
- **Spec reference:** § 13, VE-005, VE-012, VE-013, PE-002a `error.data`
- **Effort estimate:** S–M (2–3 eng-days)
- **Proposed approach:** Add `expectedInvokedBy?: string` to `VerifyOptions` and emit `VE-005` when it mismatches. For `VE-012`, implement unknown-field detection by comparing parsed envelope keys against the known field set and emitting `VE-012` when required fields are absent after a previous valid parse (stripped-field scenario requires request-scoped state; simplest approach: check field set during Level 1). For `VE-013`, add `expectedSessionId?: string` and emit when provided. Fix the three ad-hoc string errors to use `VECode` constants. Add a `toJsonRpcError()` method to `ProducerError` that emits the `{ code, message, data: { pe_code, method, tool_call_id?, session_id? } }` shape.
- **Dependencies:** None
- **Acceptance criteria:** A test for every VE code triggers the correct code. A test for PE-002a verifies the `error.data` shape matches the § 13 example. Zero ad-hoc error strings remain in `verify.ts`.

---

### Gap 13 — Observability hooks (OpenTelemetry spans, metrics, structured logs)

- **Severity:** MEDIUM
- **Current state:** No tracing, no metrics, no structured logging in any module. The issuer's signing path will be opaque in production.
- **Required state:** OpenTelemetry spans wrapping `produce()` and `verify()` at minimum: `provenance.produce` span with attributes `alg`, `tool_id`, `version`; `provenance.verify` span with attributes `level`, `error_code` (if any), `tool_id`. A `PrometheusExporter`-compatible counter for `provenance_envelopes_produced_total` and `provenance_envelopes_verified_total` labeled by `level` and `error_code`. Structured JSON logging via `pino` or `winston` at key decision points (Level 1 pass, Level 2 fail, registry lookup, source hash mismatch).
- **Spec reference:** Operational requirement; no direct spec section but required for production AVS
- **Effort estimate:** M (2–4 eng-days)
- **Proposed approach:** Accept an optional `tracer: opentelemetry.Tracer` and `meter: opentelemetry.Meter` in a package-level `configure({ tracer, meter })` call. If not configured, no-op. Use `@opentelemetry/api` (peer dep, zero bundle overhead when unused). Wrap `produce()` and `verify()` bodies in `tracer.startActiveSpan(...)`.
- **Dependencies:** None (can be built in parallel)
- **Acceptance criteria:** With an in-memory `SimpleSpanProcessor`, a produce+verify roundtrip produces two spans with correct attributes. Metrics counters increment correctly for valid and invalid envelopes. Structured log output includes `{ level, tool_id, error_code }` on Level 2 failures.

---

### Gap 14 — Test coverage: test-vector replay, per-error-code coverage, fuzz harness

- **Severity:** MEDIUM
- **Current state:** Tests cover the Ed25519 happy path, basic tamper detection, and tool_call_id mismatch. No ECDSA P-256 roundtrip test. No test-vector file (`assets/test-vectors.json` referenced in the README does not exist). No fuzz harness. Error-code coverage: VE-001, VE-002, VE-008, VE-009, VE-010 are tested; the remaining 8 VE codes, all 5 PE codes, and all 8 RE codes have no coverage. The README acknowledges this as Gap 7.
- **Required state:** (a) A `assets/test-vectors.json` file containing the canonical test vectors for the attack scenarios referenced in § 15 (`t-strip-01`, `t-replay-01`, `t-downgrade-01`, `t-dup-01/02/03`, `t-subst-01/02`, `t-did-poison-01`, `t-revoke-01`). (b) A test that iterates the test-vectors file and asserts expected outcomes. (c) Per-error-code coverage: every VE/PE/RE code must have at least one test. (d) A fuzzer entry point wrapping `verify()` using `@fuzzitdev/jsfuzz` or `jazzer.js`, runnable for ≥ 24 hours on the CI/CD nightly job.
- **Spec reference:** § 15 (test vector references throughout)
- **Effort estimate:** L (5–10 eng-days total: 3 for test vectors, 2 for error-code coverage, 3 for fuzzer setup)
- **Proposed approach:** Author `test-vectors.json` manually from the spec's § 15 attack scenarios; each vector has `{ id, description, envelope_json, expected_level, expected_errors[] }`. Write `test/vectors.test.ts` that loads and replays all vectors. For error-code coverage, add a `test/error-codes.test.ts` with one describe block per code. For fuzzing, create `fuzz/verify-fuzz.ts` with `module.exports = { fuzz: async (buf) => { try { await verify(JSON.parse(buf.toString()), ...) } catch {} } }`.
- **Dependencies:** Gaps 1, 3, 5, 6, 12 (the codes being covered must exist)
- **Acceptance criteria:** `npm test` passes all vector tests. Coverage report shows ≥ 90% branch coverage on `verify.ts`. Every VE/PE/RE code appears in at least one test assertion. Fuzzer runs 24 h on nightly CI without crashing.

---

### Gap 15 — Cryptographic test-vector roundtrip (sign every `t-xxx-xx` vector)

- **Severity:** MEDIUM
- **Current state:** `produce.ts` has no regression test proving that the signing output for a known input matches a known reference output. The ECDSA P-256 path's `bigIntToBytes` and `hexPadTo32` helpers are hand-rolled and untested in isolation.
- **Required state:** For each supported algorithm, a known-answer test (KAT) that provides a fixed private key seed, a fixed envelope body, and asserts the exact base64url-encoded signature value. The P-256 helper functions (`bigIntToBytes`, `hexPadTo32`) must have unit tests covering edge cases (leading-zero R or S component, odd-length hex).
- **Spec reference:** § 9.3, § 11 (Profile Identifier registry — signature byte format is normative)
- **Effort estimate:** S (1–2 eng-days)
- **Proposed approach:** Add `test/crypto-kat.test.ts`. Compute reference signatures offline using a reference implementation (e.g. Python `cryptography` library) and hard-code them as hex strings. Assert `Buffer.from(envelope.signature.value, "base64url").toString("hex") === reference_hex`. Also add property-based tests: sign then verify must always succeed; tamper one byte of the signature and verify must always fail.
- **Dependencies:** None
- **Acceptance criteria:** KAT passes for Ed25519 and ES256. `bigIntToBytes` and `hexPadTo32` edge-case unit tests pass. A property-based roundtrip test passes over 10,000 randomly generated messages.

---

### Gap 16 — CBOR serialization (Appendix A.2)

- **Severity:** LOW
- **Current state:** Not implemented. README documents this as Gap 8.
- **Required state:** Optional CBOR serialization/deserialization of the provenance envelope per Appendix A.2, using the COSE label mapping in § 6.1. Gated behind a compile-time flag or an `encodeCbor(envelope): Uint8Array` export. Not required for JSON-only Quality Oracle issuer.
- **Spec reference:** Appendix A.2, § 6.1 (COSE labels)
- **Effort estimate:** M (2–4 eng-days)
- **Proposed approach:** Use `cbor-x` or `cborg` for CBOR encoding. Map fields to COSE integer labels per § 6.1. Export from a separate entry point (`@modelcontextprotocol/provenance-envelope-reference/cbor`) to avoid pulling CBOR into the default bundle.
- **Dependencies:** None
- **Acceptance criteria:** CBOR-encoded envelope roundtrips to identical JSON. Media type `application/mcp-provenance+cose` is produced on encode.

---

### Gap 17 — `ClientIdentityProof` verification (§ 6.7.1–6.7.2)

- **Severity:** LOW (required for Trust-Anchored envelopes claiming agent identity; MEDIUM if the issuer intends to populate `invoked_by` from proof)
- **Current state:** `produce()` accepts `invoked_by` as a plain string with no verification. The seven-step ClientIdentityProof verification procedure (§ 6.7.2) is not implemented. An issuer that populates `invoked_by` from unverified caller input violates § 6.7 normative MUST.
- **Required state:** A `ClientIdentityProofVerifier` that runs the seven verification steps: check `v` field, parse the JWS, verify the JWS signature against the `kid`-resolved key, check `iat`/`exp` window, check `srv_nonce`, check `aud` matches this issuer, return the verified `agent_did`. `produce()` should accept a `ClientIdentityProof` input and internally run verification before populating `invoked_by`.
- **Spec reference:** § 6.7, § 6.7.1, § 6.7.2
- **Effort estimate:** M–L (3–8 eng-days, partly because the canonical definition is still in a design doc, not finalized in the spec)
- **Dependencies:** The `c5-output.md` design document containing the normative mechanism must be reviewed first; spec text in § 6.7.1–6.7.2 is currently forward-referenced.
- **Acceptance criteria:** A valid ClientIdentityProof results in `invoked_by` being set to the verified `agent_did`. An expired or signature-invalid proof causes `produce()` to fall back to the `did:enchanter:unverified` placeholder per § 6.7 clause (3).

---

## Sequencing

The gaps form three parallel tracks with a few critical-path dependencies.

**Track A — Security-critical (must close before any key material is loaded)**

```
Gap 1 (strict JSON)  →  Gap 3 (constant-time audit)  →  Gap 5 (size cap)
                                        ↓
                                  Gap 4 (zeroize) ← Gap 2 (KMS interface) [parallel]
```

Gap 1 unblocks Gap 3 (the constant-time audit requires the strict-parse input boundary). Gaps 2 and 4 are independent but Gap 4 is trivial once Gap 2 lands. Gap 5 is independent of 1–4 and can ship in the same sprint.

**Track B — Spec completeness (can proceed in parallel with Track A)**

```
Gap 12 (error codes)  →  Gap 11 (provenance/get, /list)  →  Gap 6 (multi-issuer)
                                                                     ↓
                                                              Gap 10 (issuer_signature)
                                                                     ↓
                                                              Gap 8 (DID cache TTL)
                                                                     ↑
                                                              Gap 7 (source hash fetch)
```

Gap 12 is a prerequisite for Gap 11 (the method handlers need correct error codes). Gap 6 depends on Gap 7 and Gap 10. Gap 8 (DID resolver) is a shared dependency of Gaps 7 and 10.

**Track C — Quality and observability (independent; proceed after Track A is in progress)**

```
Gap 9 (rate limit)    — independent, ship early
Gap 13 (OTel)         — independent, ship early
Gap 15 (KATs)         — independent, ship immediately
Gap 14 (test vectors + fuzz)  ← depends on Gaps 1, 3, 5, 6, 12 being closed
Gap 16 (CBOR)         — lowest priority, ship last
Gap 17 (ClientIdentityProof) — pending upstream spec finalization
```

**Recommended sprint order (3-person team):**

| Sprint | Work |
|--------|------|
| Sprint 1 (1 week) | Gap 1, Gap 5, Gap 15, Gap 9 |
| Sprint 2 (1 week) | Gap 2 (interface), Gap 4, Gap 3 (audit), Gap 12 |
| Sprint 3 (2 weeks) | Gap 8, Gap 7, Gap 11, Gap 13 |
| Sprint 4 (2 weeks) | Gap 6, Gap 10, Gap 14 (vectors + fuzz bootstrap) |
| Sprint 5 (1 week) | Gap 14 (fuzz 24h run, coverage gating), Gap 17 (if spec finalized) |
| Post-launch | Gap 16 (CBOR) |

---

## Risk register

**R1 — `@noble` constant-time guarantees are partially undocumented.**
The `@noble/ed25519` and `@noble/curves` libraries are widely used and informally constant-time, but as of 2026-05 there is no published formal security proof or audit report covering the Node.js BigInt intermediate path in the ECDSA verifier. The c3 comment "noble libraries use constant-time ops" is an assertion without a cited audit. *Mitigation:* Commission an independent cryptographic library review covering the P-256 verify path, or replace the ECDSA verifier with a WASM-compiled BoringSSL binding for the production issuer node.

**R2 — `canonicalize` npm package is a single-author package without a recent security audit.**
The package implements RFC 8785 correctly in practice but has no published CVE history or SLSA attestation. A supply-chain compromise here breaks all envelope signatures. *Mitigation:* Vendor the package or pin to a specific commit hash in `package.json`. Evaluate replacing with a 40-line inline implementation (RFC 8785 is simple enough that the reference code in the spec appendix is < 50 lines).

**R3 — Spec sections § 6.7.1–6.7.2 (ClientIdentityProof) are forward-referenced to a design doc, not yet inlined.**
The normative definition lives in `state/roadmaps/2026-05-13-close-remaining-gaps/c5-output.md`. If that doc changes before the spec is updated, Gap 17 implementation may be built against a moving target. *Mitigation:* Defer Gap 17 until the spec's last-call deadline (2026-08-13) triggers a stabilization pass.

**R4 — No `assets/test-vectors.json` exists yet.**
The spec references 15+ named test vectors (`t-strip-01`, `t-replay-01`, etc.) but the file does not exist in the repository. Authoring the vectors requires resolving ambiguities (e.g., exact byte layouts for the downgrade attack) against the spec prose. *Mitigation:* Block test-vector authoring on a spec reading session with the Enchanter Labs spec author. Target: vectors file committed by end of Sprint 3.

**R5 — Spec sections not implemented at all (beyond documented gaps).**
The following areas have zero implementation and are not called out in the README:
- § 12.8 Registry discovery document fetching and `registry_id` consistency check
- § 12.4.1 Merkle-root verification of trust-anchor feed
- § 6.7 `invoked_by` authenticated-source enforcement (§ 6.7 clauses 1–3, not just Gap 17)
- § 15.9 Cross-session replay detection (VE-013 session-id binding)
- § 6.5 `tool_version` semver format validation (currently accepted as any non-empty string)
- Transformation name validation against the Transformation Registry (§ 6.11, § 14)
These are not in the gap inventory above because they are either LOW severity for the initial issuer build or blocked on spec stabilization. They must be tracked before claiming full conformance.

---

## Conformance test plan

**Phase 1 — Unit and integration (Sprint 1–4)**

All 25 VE/PE/RE error codes are exercised by a dedicated test. Every spec § 10 validation step has at least one positive and one negative test. The test-vector file covers all named attack scenarios from § 15. ECDSA P-256 and Ed25519 KATs pass with reference signatures. The `provenance/get` and `provenance/list` handlers are tested against the example request/response JSON from § 7. Multi-issuer K-of-N tests cover: K met, K not met (insufficient registries), single revocation veto, duplicate `registry_id` collapses to K=1.

**Phase 2 — Spec vector replay**

Each `t-xxx-xx` vector from `assets/test-vectors.json` is replayed in CI on every PR. The CI job fails if any vector produces an outcome other than its declared `expected_level` and `expected_errors`. This gates every merge.

**Phase 3 — Fuzz harness (continuous, starts Sprint 4)**

The fuzz target wraps `verify(JSON.parse(buf), ...)` with strict JSON parsing enabled. Runs for ≥ 24 hours on a nightly CI job using `jazzer.js`. Any crash or assertion failure pages on-call. A secondary fuzz target wraps `produce(...)` with randomized `requestParams` and `resultContent` to catch canonicalization panics.

**Phase 4 — Manual security review**

Before the issuer node goes to testnet, perform a manual confused-deputy attack: supply an envelope where `tool_id = "did:web:attacker.com"` and `signature.protected_header.key_id = "did:web:legitimate.com#keys-1"`. Confirm VE-003 is returned (the key is not found in the attacker DID document). Attempt a key-rotation-fork replay as described in § 15.18. Attempt a SSRF via `signature.protected_header.key_id` containing an internal hostname; confirm the DID resolver does not fetch internal-network addresses. Document each attack attempt and its observed outcome in `security/MANUAL-REVIEW.md`.

**Phase 5 — Conformance declaration**

After all BLOCKER and HIGH gaps are closed and all phase 1–4 tests pass: tag the package `v1.0.0-rc.1` and publish a conformance statement against `index-v2.1` listing: supported conformance class (Conforming Envelope Producer + Consumer), supported profiles (`mcp-provenance/2026-05-13-ed25519`, `mcp-provenance/2026-05-13-ecdsa-p256`), K-of-N policy (K=2, N=3), `did_cache_ttl` (300 s), clock-skew tolerance (300 s), `maxEnvelopeSizeBytes` (65536). The statement explicitly lists unimplemented optional features (CBOR, full ClientIdentityProof) to avoid false conformance claims.
