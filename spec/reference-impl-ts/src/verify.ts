/**
 * verify.ts — Envelope validation (§ 10.1, § 10.2, § 10.4).
 *
 * verify(envelope, observedRequest, observedResult, options) → ValidationOutcome
 *
 * Level 3 (Trust-Anchored, § 10.3) is policy-driven and opt-in via options.
 *
 * DRAFT — for spec validation only.
 */

import * as ed25519 from "@noble/ed25519";
import { p256 } from "@noble/curves/p256";
import { canonicalBytes } from "./canonicalize.js";
import { computeRequestDigest, computeResultDigest, digestsEqual } from "./digest.js";
import {
  type ProvenanceEnvelope,
  type ValidationOutcome,
  type VerificationKey,
  type EnvelopeForSigning,
  SUPPORTED_PROFILES,
  PROFILE_TO_ALG,
} from "./types.js";
import { VECode } from "./errors.js";
import type { RegistryLookupResponse } from "./types.js";

// ─── Helpers ────────────────────────────────────────────────────────────────

/** Base64url decode without padding. */
function base64urlDecode(s: string): Uint8Array {
  const padded = s + "=".repeat((4 - (s.length % 4)) % 4);
  const b64 = padded.replace(/-/g, "+").replace(/_/g, "/");
  return Buffer.from(b64, "base64");
}

/** Canonical bytes for verify: envelope with signature.value removed. */
function envelopeCanonicalBytesForVerify(envelope: ProvenanceEnvelope): Uint8Array {
  const forSigning: EnvelopeForSigning = {
    ...envelope,
    signature: {
      protected_header: envelope.signature.protected_header,
      value: null,
    },
  };
  return canonicalBytes(forSigning);
}

/** Extract alg suffix from profile identifier. */
function algSuffixFromVersion(version: string): string | null {
  const match = /^mcp-provenance\/\d{4}-\d{2}-\d{2}-(.+)$/.exec(version);
  return match?.[1] ?? null;
}

/** Check `{algorithm}:{hex}` format. */
function isDigestFormat(s: string): boolean {
  return /^[a-z0-9-]+:[0-9a-f]+$/i.test(s);
}

// ─── Options ────────────────────────────────────────────────────────────────

export interface VerifyOptions {
  /**
   * The tool_call_id the Consumer generated for the originating request.
   * When provided, the envelope's tool_call_id is checked against this value (§ 6.3 / VE-002).
   */
  expectedToolCallId?: string;

  /**
   * Consumer clock-skew tolerance in seconds (§ 6.6). Default: 300.
   */
  clockSkewToleranceSeconds?: number;

  /**
   * Resolver function for obtaining the verification key by tool_id + key_id.
   * When absent, Level 2 signature verification is SKIPPED (only Level 1 runs).
   * In production, this would perform DID resolution per [DID-CORE].
   */
  resolveKey?: (toolId: string, keyId: string) => Promise<VerificationKey | null>;

  /**
   * Optional registry lookup result for Trust-Anchored validation (§ 10.3).
   * When provided alongside resolveKey, Level 3 is attempted.
   */
  trustAnchorRecord?: RegistryLookupResponse;
}

// ─── verify() ───────────────────────────────────────────────────────────────

/**
 * Validate a provenance envelope at up to three progressive levels (§ 10).
 *
 * @param envelope        - The ProvenanceEnvelope to validate.
 * @param observedRequest - The `params` object from the originating `tools/call` request.
 * @param observedResult  - The `content` field from the `tools/call` result.
 * @param options         - Verification options including key resolver.
 * @returns ValidationOutcome with the highest level reached and any errors/warnings.
 */
export async function verify(
  envelope: unknown,
  observedRequest: unknown,
  observedResult: unknown,
  options: VerifyOptions = {},
): Promise<ValidationOutcome> {
  const errors: string[] = [];
  const warnings: string[] = [];

  // ─── Level 1: Syntactically Well-Formed (§ 10.1) ─────────────────────────

  // Step 1: Must be a non-null object.
  if (typeof envelope !== "object" || envelope === null || Array.isArray(envelope)) {
    errors.push(VECode.ProfileUnsupported);
    return { level: "invalid", errors, warnings };
  }

  const env = envelope as Record<string, unknown>;

  // Helper: check required string field
  function requireString(field: string): string | null {
    const v = env[field];
    if (typeof v !== "string" || v.length === 0) {
      return null;
    }
    return v;
  }

  const version = requireString("version");
  if (!version) { errors.push(VECode.ProfileUnsupported); }
  else if (!SUPPORTED_PROFILES.has(version)) { errors.push(VECode.ProfileUnsupported); }

  const toolCallId = requireString("tool_call_id");
  if (!toolCallId) errors.push(VECode.ToolCallIdMismatch);

  const toolId = requireString("tool_id");
  if (!toolId) errors.push(VECode.DidResolutionFailed);

  if (!requireString("tool_version")) errors.push("VE-field:tool_version");
  if (!requireString("invoked_at")) errors.push("VE-field:invoked_at");
  if (!requireString("invoked_by")) errors.push("VE-field:invoked_by");

  const requestDigest = requireString("request_digest");
  if (!requestDigest || !isDigestFormat(requestDigest)) {
    errors.push(VECode.RequestDigestMismatch);
  }

  const resultDigest = requireString("result_digest");
  if (!resultDigest || !isDigestFormat(resultDigest)) {
    errors.push(VECode.ResultDigestMismatch);
  }

  // sources must be non-empty array
  const sources = env["sources"];
  if (!Array.isArray(sources) || sources.length === 0) {
    errors.push("VE-field:sources");
  }

  // signature object
  const sig = env["signature"];
  if (typeof sig !== "object" || sig === null || Array.isArray(sig)) {
    errors.push(VECode.SignatureMissing);
  } else {
    const sigObj = sig as Record<string, unknown>;
    const ph = sigObj["protected_header"];
    if (typeof ph !== "object" || ph === null) {
      errors.push(VECode.SignatureMissing);
    } else {
      const phObj = ph as Record<string, unknown>;
      if (typeof phObj["alg"] !== "string") errors.push(VECode.AlgorithmMismatch);
      if (typeof phObj["key_id"] !== "string") errors.push(VECode.DidResolutionFailed);
    }
    if (typeof sigObj["value"] !== "string" || (sigObj["value"] as string).length === 0) {
      errors.push(VECode.SignatureMissing);
    }
  }

  // Clock skew check (§ 6.6, VE-004)
  const invokedAt = requireString("invoked_at");
  if (invokedAt) {
    const tol = (options.clockSkewToleranceSeconds ?? 300) * 1000;
    const invokedMs = Date.parse(invokedAt);
    if (!isNaN(invokedMs) && Math.abs(Date.now() - invokedMs) > tol) {
      errors.push(VECode.TimestampSkew);
    }
  }

  // tool_call_id match (§ 6.3, VE-002)
  if (options.expectedToolCallId && toolCallId && toolCallId !== options.expectedToolCallId) {
    errors.push(VECode.ToolCallIdMismatch);
  }

  if (errors.length > 0) {
    return { level: "invalid", errors, warnings };
  }

  // Cast — we've validated shape above
  const typedEnvelope = envelope as ProvenanceEnvelope;

  // alg vs version consistency (§ 6.12 / VE-006)
  const algSuffix = algSuffixFromVersion(typedEnvelope.version);
  const expectedAlg = algSuffix ? PROFILE_TO_ALG[algSuffix] : undefined;
  if (!expectedAlg || typedEnvelope.signature.protected_header.alg !== expectedAlg) {
    errors.push(VECode.AlgorithmMismatch);
    return { level: "invalid", errors, warnings };
  }

  // Level 1 passed
  if (!options.resolveKey) {
    // No key resolver — stop at Level 1
    return { level: "well_formed", errors, warnings };
  }

  // ─── Level 2: Cryptographically Valid (§ 10.2) ───────────────────────────

  // Step 4-7: DID resolution for verification key
  const keyId = typedEnvelope.signature.protected_header.key_id;
  let verificationKey: VerificationKey | null;
  try {
    verificationKey = await options.resolveKey(typedEnvelope.tool_id, keyId);
  } catch {
    errors.push(VECode.DidResolutionFailed);
    return { level: "invalid", errors, warnings };
  }

  if (!verificationKey) {
    errors.push(VECode.DidResolutionFailed);
    return { level: "invalid", errors, warnings };
  }

  // Step 8: canonical bytes
  const canonical = envelopeCanonicalBytesForVerify(typedEnvelope);

  // Step 9: decode signature
  let signatureBytes: Uint8Array;
  try {
    signatureBytes = base64urlDecode(typedEnvelope.signature.value);
  } catch {
    errors.push(VECode.SignatureInvalid);
    return { level: "invalid", errors, warnings };
  }

  // Step 10: verify (constant-time per § 15.4 — noble libraries use constant-time ops)
  let sigValid = false;
  try {
    if (verificationKey.alg === "Ed25519") {
      sigValid = await ed25519.verify(signatureBytes, canonical, verificationKey.publicKey);
    } else {
      // ES256: raw R||S, 64 bytes (§ 11)
      if (signatureBytes.length !== 64) {
        errors.push(VECode.SignatureInvalid);
        return { level: "invalid", errors, warnings };
      }
      const r = BigInt("0x" + Buffer.from(signatureBytes.slice(0, 32)).toString("hex"));
      const s = BigInt("0x" + Buffer.from(signatureBytes.slice(32, 64)).toString("hex"));
      sigValid = p256.verify({ r, s }, canonical, verificationKey.publicKey, { prehash: true });
    }
  } catch {
    errors.push(VECode.SignatureInvalid);
    return { level: "invalid", errors, warnings };
  }

  if (!sigValid) {
    errors.push(VECode.SignatureInvalid);
    return { level: "invalid", errors, warnings };
  }

  // Step 12: verify request_digest (§ 10.2 step 12)
  const expectedRequestDigest = computeRequestDigest(observedRequest);
  if (!digestsEqual(typedEnvelope.request_digest, expectedRequestDigest)) {
    errors.push(VECode.RequestDigestMismatch);
    return { level: "invalid", errors, warnings };
  }

  // Step 13: verify result_digest (§ 10.2 step 13)
  const expectedResultDigest = computeResultDigest(observedResult);
  if (!digestsEqual(typedEnvelope.result_digest, expectedResultDigest)) {
    errors.push(VECode.ResultDigestMismatch);
    return { level: "invalid", errors, warnings };
  }

  // Level 2 passed
  if (!options.trustAnchorRecord) {
    return { level: "cryptographically_valid", errors, warnings };
  }

  // ─── Level 3: Trust-Anchored (§ 10.3) ────────────────────────────────────
  // This is a policy-driven check. The caller must provide the registry response.

  const tar = options.trustAnchorRecord;

  if (!tar.found || !tar.record) {
    warnings.push("Trust anchor not found in registry");
    return { level: "cryptographically_valid", errors, warnings };
  }

  // Check for revocation (§ 10.3 step 3)
  if (tar.revocation) {
    const revokedAt = new Date(tar.revocation.revoked_at).getTime();
    const invokedMs = new Date(typedEnvelope.invoked_at).getTime();
    if (invokedMs >= revokedAt) {
      warnings.push("Key is revoked — invoked_at is at or after revocation time");
      return { level: "cryptographically_valid", errors, warnings };
    }
  }

  // Check trust anchor validity window (§ 12.2)
  const anchorExpires = new Date(tar.record.expires_at).getTime();
  const anchorIssued = new Date(tar.record.issued_at).getTime();
  const invokedMs2 = new Date(typedEnvelope.invoked_at).getTime();
  if (invokedMs2 > anchorExpires) {
    warnings.push("Trust anchor has expired relative to invoked_at");
    return { level: "cryptographically_valid", errors, warnings };
  }
  if (invokedMs2 < anchorIssued) {
    warnings.push("Trust anchor issued_at is after invoked_at");
    return { level: "cryptographically_valid", errors, warnings };
  }

  // Freshness of registry response (§ 12.5 — 24 hours)
  const responseAt = new Date(tar.response_at).getTime();
  if (Date.now() - responseAt > 24 * 60 * 60 * 1000) {
    warnings.push("Registry lookup response is older than 24 hours — re-fetch required");
    return { level: "cryptographically_valid", errors, warnings };
  }

  // Source hash warnings (§ 10.3 step 5) — non-fatal
  for (const src of typedEnvelope.sources) {
    if (src.hash) {
      // In a real implementation, fetch src.url and verify hash.
      // Here we emit a warning noting the caller must verify.
      warnings.push(`Source hash present for ${src.url ?? src.type} — caller must verify content hash`);
    }
  }

  return { level: "trust_anchored", errors, warnings };
}

// ─── Multi-envelope independence (§ 10.4) ──────────────────────────────────

/**
 * Validate multiple envelopes independently per § 10.4.
 * Returns an array of outcomes in the same order as the input envelopes.
 * Each envelope is validated independently; one invalid result does NOT affect others.
 */
export async function verifyAll(
  envelopes: unknown[],
  observedRequest: unknown,
  observedResult: unknown,
  options: VerifyOptions = {},
): Promise<ValidationOutcome[]> {
  return Promise.all(
    envelopes.map((env) => verify(env, observedRequest, observedResult, options)),
  );
}
