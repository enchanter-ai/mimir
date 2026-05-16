/**
 * errors.ts — Error codes for the MCP Provenance Envelope spec.
 *
 * VE-001 through VE-013 — Validation errors (§ 13)
 * PE-001 through PE-004 — Protocol errors (§ 13)
 * RE-001 through RE-008 — Registry errors (§ 12.7)
 *
 * DRAFT — for spec validation only.
 */

// ─── Validation error codes ────────────────────────────────────────────────

export const enum VECode {
  ProfileUnsupported = "VE-001",
  ToolCallIdMismatch = "VE-002",
  DidResolutionFailed = "VE-003",
  TimestampSkew = "VE-004",
  InvokedByMismatch = "VE-005",
  AlgorithmMismatch = "VE-006",
  SignatureMissing = "VE-007",
  SignatureInvalid = "VE-008",
  RequestDigestMismatch = "VE-009",
  ResultDigestMismatch = "VE-010",
  DuplicateMember = "VE-011",
  UnknownFieldStripped = "VE-012",
  SessionIdMismatch = "VE-013",
}

// ─── Protocol error codes ──────────────────────────────────────────────────

export const enum PECode {
  NotSupported = "PE-001",
  SessionBoundary = "PE-002a",
  NotYetAvailable = "PE-002b",
  NotAvailable = "PE-003",
  InvalidCursor = "PE-004",
}

/** JSON-RPC integer codes for PE-xxx errors (§ 13). */
export const PE_JSON_RPC_CODES: Readonly<Record<string, number>> = {
  [PECode.NotSupported]: -32000,
  [PECode.SessionBoundary]: -32001,
  [PECode.NotYetAvailable]: -32002,
  [PECode.NotAvailable]: -32003,
  [PECode.InvalidCursor]: -32004,
};

// ─── Registry error codes ──────────────────────────────────────────────────

export const enum RECode {
  ToolNotFound = "RE-001",
  KeyNotFound = "RE-002",
  RecordExpired = "RE-003",
  InvalidDidSyntax = "RE-004",
  FeedUnavailable = "RE-005",
  RateLimited = "RE-006",
  AtTimeOutOfRange = "RE-007",
  MethodNotSupported = "RE-008",
}

/** JSON-RPC integer codes for RE-xxx errors (§ 12.7). */
export const RE_JSON_RPC_CODES: Readonly<Record<string, number>> = {
  [RECode.ToolNotFound]: -32800,
  [RECode.KeyNotFound]: -32801,
  [RECode.RecordExpired]: -32802,
  [RECode.InvalidDidSyntax]: -32803,
  [RECode.FeedUnavailable]: -32804,
  [RECode.RateLimited]: -32805,
  [RECode.AtTimeOutOfRange]: -32806,
  [RECode.MethodNotSupported]: -32807,
};

// ─── Error classes ─────────────────────────────────────────────────────────

/** Base class for all provenance-envelope errors. */
export class ProvenanceError extends Error {
  constructor(
    message: string,
    public readonly code: string,
  ) {
    super(message);
    this.name = "ProvenanceError";
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

/** Thrown when envelope validation fails (VE-xxx). */
export class ValidationError extends ProvenanceError {
  constructor(
    public readonly veCode: VECode,
    detail?: string,
  ) {
    super(
      detail ? `${veCode}: ${detail}` : veCode,
      veCode,
    );
    this.name = "ValidationError";
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

/** Thrown by producers when a signing precondition fails (PE-xxx). */
export class ProducerError extends ProvenanceError {
  constructor(
    public readonly peCode: PECode,
    detail?: string,
  ) {
    super(
      detail ? `${peCode}: ${detail}` : peCode,
      peCode,
    );
    this.name = "ProducerError";
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

/** Thrown by registry client on RE-xxx responses. */
export class RegistryError extends ProvenanceError {
  constructor(
    public readonly reCode: RECode,
    public readonly jsonRpcCode: number,
    detail?: string,
  ) {
    super(
      detail ? `${reCode}: ${detail}` : reCode,
      reCode,
    );
    this.name = "RegistryError";
    Object.setPrototypeOf(this, new.target.prototype);
  }
}
