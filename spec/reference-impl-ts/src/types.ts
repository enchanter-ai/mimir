/**
 * types.ts — TypeScript interfaces for the MCP Tool-Call Provenance Envelope v2.
 *
 * Derived from § 6 (Envelope Structure), § 12.2 (TrustAnchorRecord),
 * § 12.3 (RevocationRecord) of index-v2.mdx.
 *
 * DRAFT — for spec validation only.
 */

// ─── Profile identifier ────────────────────────────────────────────────────

/** String of the form `mcp-provenance/{date}-{algorithm-suite}` (§ 6.2, § 11). */
export type ProfileIdentifier = string;

/** Supported profile identifiers seeded by the registry (§ 11). */
export const SUPPORTED_PROFILES: ReadonlySet<string> = new Set([
  "mcp-provenance/2026-05-13-ed25519",
  "mcp-provenance/2026-05-13-ecdsa-p256",
]);

/** Signature algorithm identifiers (§ 6.12). */
export type SignatureAlgorithm = "Ed25519" | "ES256";

/** Map from profile suffix to canonical alg string. */
export const PROFILE_TO_ALG: Readonly<Record<string, SignatureAlgorithm>> = {
  ed25519: "Ed25519",
  "ecdsa-p256": "ES256",
};

// ─── Source types ──────────────────────────────────────────────────────────

/** Registered source type values (§ 6.9, Source Type registry). */
export type RegisteredSourceType =
  | "web"
  | "database"
  | "user"
  | "llm"
  | "tool"
  | "sensor";

/** Source type: registered value or experimental `x-*` extension. */
export type SourceType = RegisteredSourceType | `x-${string}`;

/**
 * One element of the `sources` array (§ 6.9).
 * `url` is REQUIRED when `type` is "web" or "database".
 */
export interface Source {
  /** One of the registered source types or an `x-` extension. */
  type: SourceType;
  /** URI conforming to RFC 3986. Required for web/database sources. */
  url?: string;
  /** UTC RFC 3339 timestamp of source retrieval. */
  retrieved_at: string;
  /** Content hash formatted as `{algorithm}:{hex}` (optional). */
  hash?: string;
  /** Producer-declared contribution weight in [0.0, 1.0] (informative). */
  weight?: number;
}

// ─── Signature ─────────────────────────────────────────────────────────────

/**
 * The `signature.protected_header` object (§ 6.12).
 * Included in canonical form — covered by the signature.
 */
export interface ProtectedHeader {
  /** Algorithm identifier matching the profile. */
  alg: SignatureAlgorithm;
  /** DID URL fragment identifying the verification key. */
  key_id: string;
}

/**
 * The `signature` object (§ 6.13).
 * Only `value` is excluded from the canonical form; `protected_header` is included.
 */
export interface SignatureBlock {
  /** Included in canonical form. */
  protected_header: ProtectedHeader;
  /** Base64url-encoded (no padding) signature over the canonical form. */
  value: string;
}

/**
 * The `signature` object as it appears *during signing*, before `value` is set.
 * Used internally by `produce()`.
 */
export interface SignatureBlockForSigning {
  protected_header: ProtectedHeader;
  value: null;
}

// ─── Provenance Envelope ───────────────────────────────────────────────────

/**
 * The provenance envelope object (§ 6).
 * This is the object carried in `tools/call` response.provenance.
 */
export interface ProvenanceEnvelope {
  /** Profile identifier (§ 6.2). */
  version: ProfileIdentifier;
  /** Identifier of the originating tool-call (§ 6.3). */
  tool_call_id: string;
  /** DID of the invoked tool (§ 6.4). */
  tool_id: string;
  /** Semver of the tool implementation (§ 6.5). */
  tool_version: string;
  /** UTC RFC 3339 timestamp of invocation (§ 6.6). */
  invoked_at: string;
  /** DID of the invoking agent (§ 6.7). */
  invoked_by: string;
  /** Digest over the canonical request params (§ 6.7, § 9.1). Format: `sha-256:{hex}`. */
  request_digest: string;
  /** Digest over the canonical result content (§ 6.8, § 9.1). Format: `sha-256:{hex}`. */
  result_digest: string;
  /** Upstream data sources (§ 6.9). Non-empty array. */
  sources: Source[];
  /** Ordered post-source transformations applied (§ 6.10). Optional. */
  transformations?: string[];
  /** Producer self-reported confidence in [0.0, 1.0] (§ 6.11). Informative only. */
  confidence?: number;
  /** Signature container (§ 6.12, § 6.13). */
  signature: SignatureBlock;
}

/**
 * Partial envelope with `signature.value` set to null — used during signing.
 * The canonical form is computed from this shape (§ 9.2).
 */
export type EnvelopeForSigning = Omit<ProvenanceEnvelope, "signature"> & {
  signature: SignatureBlockForSigning;
};

// ─── Signing key ───────────────────────────────────────────────────────────

/** Ed25519 signing key (32-byte seed). */
export interface Ed25519SigningKey {
  alg: "Ed25519";
  /** 32-byte private key seed. */
  privateKey: Uint8Array;
  /** 32-byte public key (optional — can be derived from seed). */
  publicKey?: Uint8Array;
  /** DID URL fragment identifying this key. */
  keyId: string;
}

/** ECDSA P-256 signing key. */
export interface EcdsaP256SigningKey {
  alg: "ES256";
  /** 32-byte big-endian private scalar d. */
  privateKey: Uint8Array;
  /** 64-byte uncompressed public key (x||y). */
  publicKey?: Uint8Array;
  /** DID URL fragment identifying this key. */
  keyId: string;
}

export type SigningKey = Ed25519SigningKey | EcdsaP256SigningKey;

/** Ed25519 verification key (public key bytes + keyId). */
export interface Ed25519VerificationKey {
  alg: "Ed25519";
  publicKey: Uint8Array;
  keyId: string;
}

/** ECDSA P-256 verification key. */
export interface EcdsaP256VerificationKey {
  alg: "ES256";
  publicKey: Uint8Array;
  keyId: string;
}

export type VerificationKey = Ed25519VerificationKey | EcdsaP256VerificationKey;

// ─── Produce options ───────────────────────────────────────────────────────

/** Options passed to `produce()` (§ 9.3). */
export interface ProduceOptions {
  /** Profile identifier. Defaults to `mcp-provenance/2026-05-13-ed25519`. */
  version?: ProfileIdentifier;
  /** Ordered transformations applied after source retrieval. */
  transformations?: string[];
  /** Producer self-reported confidence [0.0, 1.0]. */
  confidence?: number;
}

// ─── Validation ────────────────────────────────────────────────────────────

/** The three progressive validation levels (§ 10). */
export type ValidationLevel =
  | "well_formed"
  | "cryptographically_valid"
  | "trust_anchored"
  | "invalid";

/**
 * The result returned by `verify()`.
 * `errors` contains VE-xxx codes when level is "invalid".
 */
export interface ValidationOutcome {
  level: ValidationLevel;
  /** VE-xxx error codes (empty when valid). */
  errors: string[];
  /** Non-fatal warnings (e.g., source hash mismatch per § 10.3 step 5). */
  warnings: string[];
}

// ─── Trust Anchor (§ 12.2) ─────────────────────────────────────────────────

/**
 * JSON Web Key (minimal — only fields required by spec, § 12.2).
 * Full JWK per RFC 7517.
 */
export interface JsonWebKey {
  kty: string;
  crv?: string;
  x?: string;
  y?: string;
  [key: string]: unknown;
}

/**
 * `trust_anchor_record` schema (§ 12.2).
 */
export interface TrustAnchorRecord {
  tool_id: string;
  key_id: string;
  issuer: string;
  issued_at: string;
  expires_at: string;
  public_key_jwk: JsonWebKey;
  scope?: string[];
  issuer_signature: string;
}

// ─── Revocation Record (§ 12.3) ────────────────────────────────────────────

/** Reason values for revocation. */
export type RevocationReason =
  | "key_compromise"
  | "tool_decommissioned"
  | "policy_violation"
  | "issuer_error"
  | `x-${string}`;

/**
 * `revocation_record` schema (§ 12.3).
 */
export interface RevocationRecord {
  tool_id: string;
  key_id: string;
  revoked_at: string;
  reason: RevocationReason;
  issuer: string;
  issued_at: string;
  issuer_signature: string;
}

// ─── Registry response (§ 12.1) ────────────────────────────────────────────

/** Response from `registry/lookup` (§ 12.1). */
export interface RegistryLookupResponse {
  found: boolean;
  record?: TrustAnchorRecord;
  revocation?: RevocationRecord | null;
  feed_root: string;
  response_at: string;
  registry_id: string;
}
