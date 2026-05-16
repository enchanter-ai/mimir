# MCP Tool-Call Provenance Envelope — Reference Implementation

> **DRAFT** — This is a skeleton reference implementation suitable for spec validation and integration testing. It is NOT production-grade. See [Production Gaps](#production-gaps) for what must be addressed before shipping in a real system.

Package: `@modelcontextprotocol/provenance-envelope-reference`  
Spec: `state/specs/provenance-envelope/index-v2.mdx`  
Status: `Draft`

---

## Overview

This package implements the core algorithms of the MCP Tool-Call Provenance Envelope specification (v2):

| Module | Spec reference | What it does |
|--------|---------------|-------------|
| `src/types.ts` | § 6, § 12.2, § 12.3 | TypeScript interfaces for all envelope structures |
| `src/canonicalize.ts` | RFC 8785 / § 9.2 | Canonical JSON form (JCS) |
| `src/digest.ts` | § 9.1 | `request_digest` and `result_digest` computation |
| `src/produce.ts` | § 9.3 | Build and sign an envelope |
| `src/verify.ts` | § 10.1, § 10.2, § 10.4 | Validate at three progressive levels |
| `src/registry-client.ts` | § 12.1 | Fetch trust-anchor records from a registry |
| `src/errors.ts` | § 12.7, § 13 | VE/PE/RE error codes as `const enum` + error classes |

---

## Installation

```bash
npm install @modelcontextprotocol/provenance-envelope-reference
```

---

## Quick Start

### Producing an envelope

```typescript
import { produce } from "@modelcontextprotocol/provenance-envelope-reference";
import type { Ed25519SigningKey, Source } from "@modelcontextprotocol/provenance-envelope-reference";

// Your Ed25519 private key (32-byte seed)
const signingKey: Ed25519SigningKey = {
  alg: "Ed25519",
  privateKey: myPrivateKeyBytes,
  keyId: "did:web:example.com#keys-1",
};

const sources: Source[] = [
  {
    type: "web",
    url: "https://en.wikipedia.org/wiki/Eiffel_Tower",
    retrieved_at: new Date().toISOString(),
  },
];

const envelope = await produce(
  requestParams,   // tools/call request params object
  resultContent,   // tools/call result content (array/object)
  signingKey,
  {
    tool_call_id: "tc_01HXYZK4Q7M9N1J5R8B2C6D3E0",
    tool_id: "did:web:example.com:tools:wiki-search",
    tool_version: "1.4.2",
    invoked_by: "did:agent:0xf3a9c2",
    sources,
  },
);

// Attach envelope to response
return { result: { content: resultContent }, provenance: envelope };
```

### Verifying an envelope

```typescript
import { verify } from "@modelcontextprotocol/provenance-envelope-reference";

const outcome = await verify(
  envelope,
  observedRequestParams,
  observedResultContent,
  {
    expectedToolCallId: "tc_01HXYZK4Q7M9N1J5R8B2C6D3E0",
    resolveKey: async (toolId, keyId) => {
      // Resolve DID document for toolId, locate key at keyId
      // Return a VerificationKey or null
      return myDIDResolver(toolId, keyId);
    },
  },
);

if (outcome.level === "cryptographically_valid") {
  console.log("Envelope is valid");
} else {
  console.error("Validation failed:", outcome.errors);
}
```

### Registry lookup (Trust-Anchored, § 10.3)

```typescript
import { RegistryClient, verify } from "@modelcontextprotocol/provenance-envelope-reference";

const registry = new RegistryClient({
  endpoint: "https://registry.enchanterlabs.io",
});

const trustAnchorRecord = await registry.lookup(
  envelope.tool_id,
  envelope.signature.protected_header.key_id,
);

const outcome = await verify(
  envelope,
  observedRequestParams,
  observedResultContent,
  {
    resolveKey: myDIDResolver,
    trustAnchorRecord,
  },
);

if (outcome.level === "trust_anchored") {
  console.log("Envelope is trust-anchored");
}
```

---

## Error Codes

Three tiers of error codes are defined in `src/errors.ts`:

| Tier | Codes | Used for |
|------|-------|----------|
| `VECode` | VE-001 … VE-013 | Envelope validation failures |
| `PECode` | PE-001 … PE-004 | Protocol-level producer errors |
| `RECode` | RE-001 … RE-008 | Registry response errors |

---

## Tests

```bash
npm test
```

Tests cover:

- `test/canonicalize.test.ts` — RFC 8785 determinism: `{a:1,b:2}` equals `{b:2,a:1}`
- `test/produce-verify.test.ts` — Ed25519 roundtrip; tamper detection on result and signature; tool_call_id mismatch

---

## Production Gaps

The following gaps MUST be addressed before using this package in production:

### 1. Duplicate JSON member detection (§ 10.1 step 1, VE-011)

`JSON.parse()` silently overwrites duplicate object keys. This means an envelope with duplicate member names will **not** be rejected at Level 1 as the spec requires. A strict JSON parser that throws on duplicate members is needed. Options:

- Write a custom `JSON.parse` reviver that tracks keys
- Use a library such as `json-bigint` in strict mode
- Parse via a streaming JSON parser that surfaces duplicate-key events

Until this is fixed, the implementation does not fully satisfy § 10.1 step 1 and § 15.12 (VE-011).

### 2. DID resolution

The `resolveKey` callback in `verify()` is caller-supplied. This package does NOT include a DID resolver. Production code must implement full `did:web` and `did:key` resolution per [DID-CORE] before claiming Level 2 or Level 3 conformance.

### 3. Key management / KMS integration

`produce()` accepts raw private key bytes. Production code MUST store private keys in an audited KMS (HSM, cloud KMS, or a hardware token) and never pass them as in-memory byte arrays.

### 4. Multi-issuer trust-anchor policy (§ 12.6)

`verify()` accepts a single `RegistryLookupResponse`. The spec recommends K-of-N (default K=2, N=3) distinct registries for Trust-Anchored validation. This skeleton does not implement multi-issuer aggregation.

### 5. Source hash verification (§ 10.3 step 5)

When `sources[i].hash` is present, the spec requires the consumer to fetch `sources[i].url` and verify the declared hash. This implementation emits a warning but does not perform the fetch or hash check.

### 6. Clock skew policy

The default clock-skew tolerance is 300 seconds. Production deployments should calibrate this against their actual NTP synchronization policy.

### 7. Test coverage

The test suite covers the happy path and basic error cases. A complete conformance test suite with the test vectors referenced in the spec (e.g., `t-strip-01`, `t-replay-01`, `t-downgrade-01`) has not been written.

### 8. CBOR serialization (Appendix A.2)

The CBOR-encoded envelope format is not implemented.

---

## Supported Profiles

| Profile Identifier | Algorithm | Signature format |
|-------------------|-----------|----------------|
| `mcp-provenance/2026-05-13-ed25519` | Ed25519 | 64-byte raw, 86 base64url chars |
| `mcp-provenance/2026-05-13-ecdsa-p256` | ES256 (ECDSA P-256) | 64-byte raw R\|\|S, 86 base64url chars |

---

## License

CC0-1.0 OR Apache-2.0
