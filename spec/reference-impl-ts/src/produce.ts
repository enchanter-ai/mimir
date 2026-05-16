/**
 * produce.ts — Produce Signature algorithm (§ 9.3).
 *
 * produce(request, result, signingKey, options) → ProvenanceEnvelope
 *
 * DRAFT — for spec validation only.
 */

import * as ed25519 from "@noble/ed25519";
import { p256 } from "@noble/curves/p256";
import { bytesToHex } from "@noble/hashes/utils";
import { canonicalBytes } from "./canonicalize.js";
import { computeRequestDigest, computeResultDigest } from "./digest.js";
import {
  type ProvenanceEnvelope,
  type EnvelopeForSigning,
  type SigningKey,
  type Source,
  type ProduceOptions,
  SUPPORTED_PROFILES,
  PROFILE_TO_ALG,
} from "./types.js";
import { ProducerError, PECode } from "./errors.js";

// ─── Helpers ────────────────────────────────────────────────────────────────

/** Base64url encode without padding (RFC 4648 § 5, RFC 7515 § 2). */
function base64urlEncode(bytes: Uint8Array): string {
  // Node.js Buffer.from + base64url encoding
  return Buffer.from(bytes)
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=/g, "");
}

/** Extract the algorithm-suite suffix from a profile identifier. */
function algSuffixFromVersion(version: string): string {
  // "mcp-provenance/2026-05-13-ed25519" → "ed25519"
  const match = /^mcp-provenance\/\d{4}-\d{2}-\d{2}-(.+)$/.exec(version);
  if (!match || !match[1]) {
    throw new ProducerError(
      PECode.NotSupported,
      `Invalid profile identifier: ${version}`,
    );
  }
  return match[1];
}

// ─── Canonical form for signing (§ 9.2) ────────────────────────────────────

/**
 * Compute the canonical bytes of the envelope with `signature.value` removed.
 * `signature.protected_header` MUST remain present (§ 9.2 step 2).
 */
function envelopeCanonicalBytes(envelope: ProvenanceEnvelope): Uint8Array {
  const forSigning: EnvelopeForSigning = {
    ...envelope,
    signature: {
      protected_header: envelope.signature.protected_header,
      value: null,
    },
  };
  return canonicalBytes(forSigning);
}

// ─── produce() ─────────────────────────────────────────────────────────────

/**
 * Options for the `produce` function.
 */
export interface FullProduceOptions extends ProduceOptions {
  /** DID of the invoking agent (§ 6.7). */
  invoked_by: string;
  /** DID of the tool (§ 6.4). */
  tool_id: string;
  /** Semver of the tool (§ 6.5). */
  tool_version: string;
  /** tool_call_id echoed from the originating request (§ 6.3). */
  tool_call_id: string;
  /** Upstream sources (§ 6.9). Must be non-empty. */
  sources: Source[];
  /** Invocation timestamp in RFC 3339 UTC. Defaults to now. */
  invoked_at?: string;
}

/**
 * Produce a signed provenance envelope (§ 9.3).
 *
 * @param requestParams - The `params` object from the originating `tools/call` request.
 * @param resultContent - The `content` field from the `tools/call` result.
 * @param signingKey    - The producer's private signing key.
 * @param opts          - Required envelope metadata + optional fields.
 * @returns A fully signed ProvenanceEnvelope ready to attach to the response.
 */
export async function produce(
  requestParams: unknown,
  resultContent: unknown,
  signingKey: SigningKey,
  opts: FullProduceOptions,
): Promise<ProvenanceEnvelope> {
  // § 9.3 step 5: derive alg from version
  const version =
    opts.version ?? "mcp-provenance/2026-05-13-ed25519";

  if (!SUPPORTED_PROFILES.has(version)) {
    throw new ProducerError(
      PECode.NotSupported,
      `Unsupported profile: ${version}`,
    );
  }

  const algSuffix = algSuffixFromVersion(version);
  const expectedAlg = PROFILE_TO_ALG[algSuffix];
  if (!expectedAlg) {
    throw new ProducerError(
      PECode.NotSupported,
      `Unknown algorithm suffix in profile: ${algSuffix}`,
    );
  }

  if (signingKey.alg !== expectedAlg) {
    throw new ProducerError(
      PECode.NotSupported,
      `Signing key algorithm (${signingKey.alg}) does not match profile (${expectedAlg})`,
    );
  }

  if (!opts.sources || opts.sources.length === 0) {
    throw new ProducerError(
      PECode.NotSupported,
      "sources MUST be a non-empty array (§ 6.9)",
    );
  }

  if (!opts.tool_call_id) {
    throw new ProducerError(
      PECode.SessionBoundary,
      "tool_call_id is required — do not emit an envelope if the request did not include one (§ 6.3)",
    );
  }

  // § 9.1: Compute digests before constructing the envelope
  const requestDigest = computeRequestDigest(requestParams);
  const resultDigest = computeResultDigest(resultContent);

  // § 9.3 steps 1-6: Construct envelope with signature.value = null
  const envelopeForSigning: ProvenanceEnvelope = {
    version,
    tool_call_id: opts.tool_call_id,
    tool_id: opts.tool_id,
    tool_version: opts.tool_version,
    invoked_at: opts.invoked_at ?? new Date().toISOString(),
    invoked_by: opts.invoked_by,
    request_digest: requestDigest,
    result_digest: resultDigest,
    sources: opts.sources,
    ...(opts.transformations !== undefined && { transformations: opts.transformations }),
    ...(opts.confidence !== undefined && { confidence: opts.confidence }),
    signature: {
      protected_header: {
        alg: signingKey.alg,
        key_id: signingKey.keyId,
      },
      value: "", // placeholder; will be replaced after signing
    },
  };

  // § 9.2: Compute canonical bytes (signature.value excluded)
  const canonical = envelopeCanonicalBytes(envelopeForSigning);

  // § 9.3 step 8: Sign
  let signatureBytes: Uint8Array;

  if (signingKey.alg === "Ed25519") {
    // @noble/ed25519 signs the message directly (no prehash)
    signatureBytes = await ed25519.sign(canonical, signingKey.privateKey);
  } else {
    // ES256: ECDSA P-256 / SHA-256, raw R||S format (§ 11, § 6.14)
    const sig = p256.sign(canonical, signingKey.privateKey, { lowS: true, prehash: true });
    // Extract raw R||S: each component zero-padded to 32 bytes (§ 11)
    const r = sig.r;
    const s = sig.s;
    const rBytes = hexPadTo32(bytesToHex(bigIntToBytes(r)));
    const sBytes = hexPadTo32(bytesToHex(bigIntToBytes(s)));
    signatureBytes = new Uint8Array(64);
    signatureBytes.set(rBytes, 0);
    signatureBytes.set(sBytes, 32);
  }

  // § 9.3 step 9-10: base64url encode, set value
  const signatureB64 = base64urlEncode(signatureBytes);

  return {
    ...envelopeForSigning,
    signature: {
      protected_header: envelopeForSigning.signature.protected_header,
      value: signatureB64,
    },
  };
}

// ─── Utility ────────────────────────────────────────────────────────────────

function bigIntToBytes(n: bigint): Uint8Array {
  let hex = n.toString(16);
  if (hex.length % 2 !== 0) hex = "0" + hex;
  const bytes = new Uint8Array(hex.length / 2);
  for (let i = 0; i < bytes.length; i++) {
    bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return bytes;
}

function hexPadTo32(hex: string): Uint8Array {
  // Pad hex to 64 chars (32 bytes), then decode
  const padded = hex.padStart(64, "0");
  const bytes = new Uint8Array(32);
  for (let i = 0; i < 32; i++) {
    bytes[i] = parseInt(padded.slice(i * 2, i * 2 + 2), 16);
  }
  return bytes;
}
