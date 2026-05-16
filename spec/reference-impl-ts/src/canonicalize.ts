/**
 * canonicalize.ts — RFC 8785 JSON Canonicalization Scheme (JCS).
 *
 * Delegates to the `canonicalize` npm library (npm:canonicalize), which
 * implements RFC 8785 deterministic serialization.
 *
 * § 9.2: Compute Canonical Form — used by both produce() and verify().
 *
 * Known gap — duplicate member detection (§ 15.12, VE-011):
 *   JSON.parse() silently overwrites duplicate object keys, so calling
 *   canonicalize() on a string parsed with JSON.parse() will NOT detect
 *   duplicates. Production code MUST parse with a strict JSON parser that
 *   throws on duplicate members (e.g., a custom reviver or a library like
 *   `json-bigint` with `strict` mode). This gap is documented explicitly
 *   because it is a security-relevant omission: a duplicate-key envelope
 *   can pass syntactic validation here even though the spec (§ 10.1 step 1)
 *   requires rejection.
 *
 * DRAFT — for spec validation only.
 */

// eslint-disable-next-line @typescript-eslint/no-require-imports
const canonicalize_ = require("canonicalize") as (obj: unknown) => string;

/**
 * Produce the RFC 8785 canonical JSON string for any JSON-compatible value.
 *
 * @param value - Any JSON-serializable value.
 * @returns Canonical UTF-8 string.
 */
export function canonicalizeJson(value: unknown): string {
  const result: string | undefined = canonicalize_(value);
  if (result === undefined) {
    throw new Error("canonicalize returned undefined — value is not JSON-serializable");
  }
  return result;
}

/**
 * Produce the UTF-8 byte encoding of the canonical JSON form.
 *
 * @param value - Any JSON-serializable value.
 * @returns UTF-8 encoded bytes of the canonical JSON string.
 */
export function canonicalBytes(value: unknown): Uint8Array {
  const str = canonicalizeJson(value);
  return new TextEncoder().encode(str);
}
