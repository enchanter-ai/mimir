/**
 * digest.ts — Request and Result Digest computation (§ 9.1).
 *
 * Implements:
 *   - computeRequestDigest(requestParams) → "sha-256:{hex}"
 *   - computeResultDigest(resultContent)  → "sha-256:{hex}"
 *
 * DRAFT — for spec validation only.
 */

import { sha256 } from "@noble/hashes/sha256";
import { bytesToHex } from "@noble/hashes/utils";
import { canonicalBytes } from "./canonicalize.js";

/**
 * Compute the `request_digest` field value (§ 9.1 step 1–6).
 *
 * @param requestParams - The `params` object of the originating `tools/call`
 *   JSON-RPC request (not the whole request, just params).
 * @returns Digest string in the form `sha-256:{lowercase-hex}`.
 */
export function computeRequestDigest(requestParams: unknown): string {
  const bytes = canonicalBytes(requestParams);
  const hash = sha256(bytes);
  return `sha-256:${bytesToHex(hash)}`;
}

/**
 * Compute the `result_digest` field value (§ 9.1, Result Digest sub-algorithm).
 *
 * @param resultContent - The `content` field of the `tools/call` result object
 *   (the array/object produced by the tool, NOT including the `provenance` field).
 * @returns Digest string in the form `sha-256:{lowercase-hex}`.
 */
export function computeResultDigest(resultContent: unknown): string {
  const bytes = canonicalBytes(resultContent);
  const hash = sha256(bytes);
  return `sha-256:${bytesToHex(hash)}`;
}

/**
 * Validate a digest string matches the `{algorithm}:{hex}` format (§ 6.7, § 6.8).
 *
 * @param digest - The string to check.
 * @returns true when format is valid.
 */
export function isValidDigestFormat(digest: string): boolean {
  return /^[a-z0-9-]+:[0-9a-f]+$/i.test(digest);
}

/**
 * Case-insensitive hex comparison of two digest strings (§ 10.2 step 12b, 13b).
 * Both digests must include the algorithm prefix.
 *
 * @returns true when the digests are equal.
 */
export function digestsEqual(a: string, b: string): boolean {
  return a.toLowerCase() === b.toLowerCase();
}
