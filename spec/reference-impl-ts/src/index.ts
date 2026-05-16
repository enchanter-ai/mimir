/**
 * index.ts — Barrel export for @modelcontextprotocol/provenance-envelope-reference
 *
 * DRAFT — for spec validation only.
 */

// Types
export type {
  ProvenanceEnvelope,
  EnvelopeForSigning,
  SignatureBlock,
  SignatureBlockForSigning,
  ProtectedHeader,
  Source,
  SourceType,
  RegisteredSourceType,
  SigningKey,
  Ed25519SigningKey,
  EcdsaP256SigningKey,
  VerificationKey,
  Ed25519VerificationKey,
  EcdsaP256VerificationKey,
  ProduceOptions,
  ValidationOutcome,
  ValidationLevel,
  TrustAnchorRecord,
  RevocationRecord,
  RevocationReason,
  RegistryLookupResponse,
  JsonWebKey,
  ProfileIdentifier,
  SignatureAlgorithm,
} from "./types.js";

export { SUPPORTED_PROFILES, PROFILE_TO_ALG } from "./types.js";

// Canonicalize
export { canonicalizeJson, canonicalBytes } from "./canonicalize.js";

// Digest
export {
  computeRequestDigest,
  computeResultDigest,
  isValidDigestFormat,
  digestsEqual,
} from "./digest.js";

// Produce
export { produce } from "./produce.js";
export type { FullProduceOptions } from "./produce.js";

// Verify
export { verify, verifyAll } from "./verify.js";
export type { VerifyOptions } from "./verify.js";

// Registry client
export { RegistryClient } from "./registry-client.js";
export type { RegistryClientOptions } from "./registry-client.js";

// Errors
export {
  VECode,
  PECode,
  RECode,
  PE_JSON_RPC_CODES,
  RE_JSON_RPC_CODES,
  ProvenanceError,
  ValidationError,
  ProducerError,
  RegistryError,
} from "./errors.js";
