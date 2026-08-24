"""deploy_gate.py — Off-chain, quality-gated receipt generation for Mimir.

This module is the **executed** receipt-generation / signer path for the POC
layer. It closes the gap the README/ROADMAP tracked: historically the issuer's
`POST /v1/attest` signed `(request, result)` *unconditionally* — the scoring
oracle's DEPLOY verdict was advisory-only, and `sources[]` was a hardcoded
placeholder (`issuer/envelope/builder.go: stubSources()`).

Here, a receipt is produced **only** when a wixie DEPLOY verdict is present and
independently passes the DEPLOY bar, and `sources[]` is populated from that
verdict's provenance (axes / assertions / scores). If the verdict is absent or
fails the bar, receipt generation REFUSES: it raises `DeployGateError`, logs the
refusal, and the CLI exits non-zero. No verdict, no receipt.

SCOPE — off-chain only. This module:
  * touches NO Solidity contract, ABI, or generated binding;
  * performs NO chain interaction (no broadcast, no `cast send`, no deploy);
  * emits `sources[]` into the OFF-CHAIN JSON receipt payload ONLY — sources are
    never placed on-chain (the on-chain anchor stores digests, not sources).

The DEPLOY bar enforced here is the strict **wixie** bar:

    DEPLOY  ⇔  σ < 0.45  ∧  overall ≥ 9.0  ∧  min-axis ≥ 7.0  ∧  8/8 assertions pass

Note: the mimir scoring engine (`scoring/src/score.ts`) currently emits its
verdict against a *relaxed* σ < 0.75 bar, empirically calibrated for its own
score distribution. This gate deliberately re-derives the decision against the
strict wixie bar (σ < 0.45) and does NOT blindly trust the verdict string — a
verdict labelled "DEPLOY" whose numbers do not clear the strict bar is refused.

────────────────────────────────────────────────────────────────────────────
Interface — the wixie verdict input (a `metadata.json` / verdict JSON object)
────────────────────────────────────────────────────────────────────────────

`load_verdict()` / `evaluate_gate()` read an object of this shape (a superset is
fine; unknown keys are ignored):

    {
      "verdict":   "DEPLOY",                 # informative; re-derived here
      "sigma":     0.12,                     # float ≥ 0
      "overall":   9.3,                      # float in [0, 10]
      "axes": [                              # exactly 5 entries
        {"axis": "clarity",                "score": 9.5, "rationale": "..."},
        {"axis": "specificity",            "score": 9.0, "rationale": "..."},
        {"axis": "faithfulness_to_source", "score": 9.2, "rationale": "..."},
        {"axis": "safety",                 "score": 9.4, "rationale": "..."},
        {"axis": "structure",              "score": 9.1, "rationale": "..."}
      ],
      "assertions": [                        # exactly 8 entries
        {"assertion": "request_addressed",       "passed": true, "rationale": "..."},
        ... 8 total ...
      ],
      "scored_at":  "2026-08-24T12:00:00Z",  # RFC 3339 UTC (optional)
      "model_used": "claude-sonnet-4-6"      # optional
    }

This is exactly the `ScoringVerdict` shape emitted by the TS scoring engine
(`scoring/src/types.ts`) and is compatible with a wixie `metadata.json` that
carries the same 5-axis / 8-assertion / σ / overall fields.
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import json
import logging
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

try:  # PyNaCl is the same Ed25519 dependency the rest of the POC layer uses.
    import nacl.signing
except ImportError as _e:  # pragma: no cover - environment guard
    raise ImportError(
        "deploy_gate requires PyNaCl (pip install pynacl) for Ed25519 signing"
    ) from _e

logger = logging.getLogger("mimir.receipt.deploy_gate")

# ─── DEPLOY bar (strict wixie bar) ───────────────────────────────────────────

#: σ across the 5 axes must be strictly below this.
SIGMA_MAX = 0.45
#: Overall (mean axis score) must be at least this.
OVERALL_MIN = 9.0
#: Every individual axis must be at least this.
AXIS_MIN = 7.0
#: Exactly this many axes must be present.
EXPECTED_AXES = 5
#: Exactly this many SAT assertions must be present, all passing.
EXPECTED_ASSERTIONS = 8

#: Envelope profile — mirrors the Go issuer / TS reference impl.
RECEIPT_VERSION = "mcp-provenance/2026-05-13-ed25519"
#: invoked_by placeholder, matching issuer/envelope/builder.go invokedByStub.
INVOKED_BY_STUB = "did:enchanter:unverified"


class DeployGateError(Exception):
    """Raised when a receipt is refused because the DEPLOY bar is not met.

    Carries the machine-readable list of failed criteria so callers can log or
    surface exactly why signing was refused.
    """

    def __init__(self, message: str, reasons: list[str] | None = None) -> None:
        super().__init__(message)
        self.reasons: list[str] = reasons or []


@dataclass(frozen=True)
class GateDecision:
    """The result of evaluating a verdict against the strict wixie DEPLOY bar."""

    passed: bool
    reasons: list[str] = field(default_factory=list)
    sigma: float | None = None
    overall: float | None = None
    min_axis: float | None = None
    assertions_passed: int | None = None

    def as_dict(self) -> dict[str, Any]:
        return {
            "passed": self.passed,
            "reasons": self.reasons,
            "bar": {
                "sigma_max": SIGMA_MAX,
                "overall_min": OVERALL_MIN,
                "axis_min": AXIS_MIN,
                "assertions_required": EXPECTED_ASSERTIONS,
            },
            "observed": {
                "sigma": self.sigma,
                "overall": self.overall,
                "min_axis": self.min_axis,
                "assertions_passed": self.assertions_passed,
            },
        }


# ─── Canonicalization + digests (mirror spec §9.1 / demo.py) ─────────────────


def canonical_bytes(obj: Any) -> bytes:
    """RFC 8785-style JCS: sorted keys, no whitespace, UTF-8.

    Matches ``demo.py: rfc8785_canonicalize`` and the reference impl.
    """
    return json.dumps(
        obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False
    ).encode("utf-8")


def _sha256_prefixed(obj: Any) -> str:
    """Return ``sha-256:{hex}`` over the canonical bytes (spec §9.1)."""
    return "sha-256:" + hashlib.sha256(canonical_bytes(obj)).hexdigest()


def _b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")


# ─── Load + validate the verdict ─────────────────────────────────────────────


def load_verdict(source: str | Path | dict[str, Any]) -> dict[str, Any]:
    """Load a wixie verdict from a JSON file path or an in-memory dict.

    Raises ``DeployGateError`` when the verdict is absent/unreadable — an absent
    verdict is a refusal condition, not a soft warning.
    """
    if isinstance(source, dict):
        return source
    path = Path(source)
    if not path.exists():
        raise DeployGateError(
            f"verdict not found: {path}",
            reasons=[f"verdict_absent: no file at {path}"],
        )
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as e:
        raise DeployGateError(
            f"verdict unreadable: {path}: {e}",
            reasons=[f"verdict_unreadable: {e}"],
        ) from e


# ─── The gate predicate ──────────────────────────────────────────────────────


def evaluate_gate(verdict: dict[str, Any] | None) -> GateDecision:
    """Independently evaluate a verdict against the strict wixie DEPLOY bar.

    Does NOT trust ``verdict["verdict"]`` — it re-derives every criterion from
    the raw numbers. The verdict string, if present, is only cross-checked: a
    label of "DEPLOY" whose numbers fail the bar is itself a failure reason.
    """
    reasons: list[str] = []

    if verdict is None:
        return GateDecision(passed=False, reasons=["verdict_absent: verdict is None"])
    if not isinstance(verdict, dict):
        return GateDecision(
            passed=False, reasons=[f"verdict_malformed: expected object, got {type(verdict).__name__}"]
        )

    # --- sigma ---
    sigma = _as_float(verdict.get("sigma"))
    if sigma is None:
        reasons.append("sigma_missing: no numeric 'sigma' field")
    elif not (sigma < SIGMA_MAX):
        reasons.append(f"sigma_too_high: {sigma} >= {SIGMA_MAX}")

    # --- overall ---
    overall = _as_float(verdict.get("overall"))
    if overall is None:
        reasons.append("overall_missing: no numeric 'overall' field")
    elif overall < OVERALL_MIN:
        reasons.append(f"overall_too_low: {overall} < {OVERALL_MIN}")

    # --- axes ---
    axes = verdict.get("axes")
    min_axis: float | None = None
    if not isinstance(axes, list) or len(axes) != EXPECTED_AXES:
        reasons.append(
            f"axes_count: expected {EXPECTED_AXES}, got "
            f"{len(axes) if isinstance(axes, list) else 'non-list'}"
        )
    else:
        scores = [_as_float(a.get("score")) if isinstance(a, dict) else None for a in axes]
        if any(s is None for s in scores):
            reasons.append("axes_malformed: one or more axes missing a numeric score")
        else:
            min_axis = min(scores)  # type: ignore[type-var]
            if min_axis < AXIS_MIN:
                low = [
                    a.get("axis", "?")
                    for a, s in zip(axes, scores)
                    if s is not None and s < AXIS_MIN
                ]
                reasons.append(f"axis_below_floor: {low} < {AXIS_MIN}")

    # --- assertions ---
    assertions = verdict.get("assertions")
    assertions_passed: int | None = None
    if not isinstance(assertions, list) or len(assertions) != EXPECTED_ASSERTIONS:
        reasons.append(
            f"assertions_count: expected {EXPECTED_ASSERTIONS}, got "
            f"{len(assertions) if isinstance(assertions, list) else 'non-list'}"
        )
    else:
        assertions_passed = sum(
            1 for a in assertions if isinstance(a, dict) and a.get("passed") is True
        )
        if assertions_passed != EXPECTED_ASSERTIONS:
            failed = [
                a.get("assertion", "?")
                for a in assertions
                if not (isinstance(a, dict) and a.get("passed") is True)
            ]
            reasons.append(f"assertions_failed: {failed}")

    # --- cross-check the label (does not relax the bar) ---
    label = verdict.get("verdict")
    if label is not None and label != "DEPLOY" and not reasons:
        reasons.append(
            f"verdict_label_mismatch: numbers pass the bar but label is {label!r}, not 'DEPLOY'"
        )

    return GateDecision(
        passed=not reasons,
        reasons=reasons,
        sigma=sigma,
        overall=overall,
        min_axis=min_axis,
        assertions_passed=assertions_passed,
    )


def _as_float(v: Any) -> float | None:
    if isinstance(v, bool):  # bool is an int subclass; reject it explicitly
        return None
    if isinstance(v, (int, float)):
        return float(v)
    return None


# ─── sources[] population from the eval provenance ───────────────────────────


def build_sources(verdict: dict[str, Any]) -> list[dict[str, Any]]:
    """Populate ``sources[]`` from the wixie eval output.

    Emits spec-conformant Source entries (§6.9) using the ``x-`` extension
    namespace so they validate as experimental source types:

      * one ``x-wixie-eval`` summary entry, hash-bound to the exact verdict, and
      * one ``x-wixie-eval-axis`` entry per scored axis, weight = score / 10.

    This replaces the hardcoded ``stub:scoring-engine-not-yet-integrated``
    placeholder with the actual evidence the receipt is being signed against.
    """
    retrieved_at = verdict.get("scored_at") or verdict.get("timestamp")
    verdict_hash = _sha256_prefixed(verdict)

    summary: dict[str, Any] = {
        "type": "x-wixie-eval",
        "retrieved_at": retrieved_at,
        "hash": verdict_hash,
        "weight": _round(_as_float(verdict.get("overall")) or 0.0, 10.0),
        "detail": {
            "model_used": verdict.get("model_used"),
            "verdict": verdict.get("verdict"),
            "sigma": verdict.get("sigma"),
            "overall": verdict.get("overall"),
            "assertions": [
                {"assertion": a.get("assertion"), "passed": a.get("passed")}
                for a in verdict.get("assertions", [])
                if isinstance(a, dict)
            ],
        },
    }

    sources: list[dict[str, Any]] = [summary]
    for a in verdict.get("axes", []):
        if not isinstance(a, dict):
            continue
        score = _as_float(a.get("score"))
        sources.append(
            {
                "type": "x-wixie-eval-axis",
                "retrieved_at": retrieved_at,
                "weight": _round(score or 0.0, 10.0),
                "detail": {
                    "axis": a.get("axis"),
                    "score": a.get("score"),
                    "rationale": a.get("rationale"),
                },
            }
        )
    return sources


def _round(value: float, denom: float) -> float:
    return round(value / denom, 4)


# ─── Signing key ─────────────────────────────────────────────────────────────


@dataclass
class SigningKey:
    """An Ed25519 signing key for the off-chain POC signer path."""

    signing_key: "nacl.signing.SigningKey"
    key_id: str

    @classmethod
    def from_seed(cls, seed: bytes, key_id: str = "poc-ed25519-key") -> "SigningKey":
        if len(seed) != 32:
            raise ValueError("Ed25519 seed must be exactly 32 bytes")
        return cls(nacl.signing.SigningKey(seed), key_id)

    @classmethod
    def ephemeral(cls, key_id: str = "poc-ephemeral") -> "SigningKey":
        return cls(nacl.signing.SigningKey.generate(), key_id)

    def public_key_b64url(self) -> str:
        return _b64url(bytes(self.signing_key.verify_key))


# ─── The gated receipt generator ─────────────────────────────────────────────


def generate_receipt(
    request: Any,
    result: Any,
    verdict: str | Path | dict[str, Any],
    signing_key: SigningKey,
    *,
    tool_id: str,
    tool_version: str,
    invoked_at: str,
    tool_call_id: str,
    invoked_by: str = INVOKED_BY_STUB,
) -> dict[str, Any]:
    """Produce a signed, quality-gated provenance receipt — or REFUSE.

    Refusal (``DeployGateError``) happens when the verdict is absent, unreadable,
    or fails the strict wixie DEPLOY bar. On refusal, nothing is signed and the
    reason is logged at WARNING. On success, the receipt's ``sources[]`` is
    populated from the eval provenance and the envelope is Ed25519-signed.

    ``invoked_at`` and ``tool_call_id`` are required inputs (deterministic,
    caller-supplied) — the receipt does not fabricate them.
    """
    loaded = load_verdict(verdict)  # raises DeployGateError if absent/unreadable
    decision = evaluate_gate(loaded)
    if not decision.passed:
        logger.warning(
            "REFUSED to generate receipt for tool_call_id=%s: DEPLOY bar not met: %s",
            tool_call_id,
            "; ".join(decision.reasons),
        )
        raise DeployGateError(
            "DEPLOY bar not met - refusing to sign receipt", reasons=decision.reasons
        )

    logger.info(
        "DEPLOY bar met (sigma=%s overall=%s min_axis=%s assertions=%s/8) — signing receipt %s",
        decision.sigma,
        decision.overall,
        decision.min_axis,
        decision.assertions_passed,
        tool_call_id,
    )

    # Result digest is over the "content" array when present (spec §9.1).
    result_target = (
        result["content"] if isinstance(result, dict) and "content" in result else result
    )

    envelope: dict[str, Any] = {
        "version": RECEIPT_VERSION,
        "tool_call_id": tool_call_id,
        "tool_id": tool_id,
        "tool_version": tool_version,
        "invoked_at": invoked_at,
        "invoked_by": invoked_by,
        "request_digest": _sha256_prefixed(request),
        "result_digest": _sha256_prefixed(result_target),
        "sources": build_sources(loaded),
        "signature": {
            "protected_header": {"alg": "Ed25519", "key_id": signing_key.key_id},
            # value added after signing over the canonical form (value excluded)
        },
    }

    canonical = canonical_bytes(envelope)
    signature = signing_key.signing_key.sign(canonical).signature
    envelope["signature"]["value"] = _b64url(signature)
    return envelope


def verify_receipt(envelope: dict[str, Any], public_key_b64url: str) -> bool:
    """Verify a receipt's Ed25519 signature over its canonical form.

    Helper for tests / external verifiers — strips ``signature.value`` before
    recomputing the canonical bytes, exactly as ``demo.py`` does.
    """
    e = json.loads(json.dumps(envelope))  # deep copy
    sig_b64 = e.get("signature", {}).pop("value", None)
    if not sig_b64:
        return False
    pad = (4 - len(public_key_b64url) % 4) % 4
    pub = base64.urlsafe_b64decode(public_key_b64url + "=" * pad)
    pad = (4 - len(sig_b64) % 4) % 4
    sig = base64.urlsafe_b64decode(sig_b64 + "=" * pad)
    try:
        nacl.signing.VerifyKey(pub).verify(canonical_bytes(e), sig)
        return True
    except Exception:
        return False


# ─── CLI — the POC entry point ───────────────────────────────────────────────


def _main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="deploy_gate",
        description="Gate a Mimir receipt on a passing wixie DEPLOY verdict (off-chain).",
    )
    parser.add_argument("--verdict", required=True, help="path to the wixie verdict JSON")
    parser.add_argument("--request", help="path to the tools/call request JSON")
    parser.add_argument("--result", help="path to the tools/call result JSON")
    parser.add_argument("--tool-id", default="did:web:example.com:tools:demo")
    parser.add_argument("--tool-version", default="1.0.0")
    parser.add_argument("--tool-call-id", default="tu_cli_demo")
    parser.add_argument(
        "--invoked-at", default="2026-08-24T00:00:00Z", help="RFC 3339 UTC timestamp"
    )
    parser.add_argument("--out", help="write the signed receipt JSON here")
    parser.add_argument("--verbose", action="store_true")
    args = parser.parse_args(argv)

    logging.basicConfig(
        level=logging.INFO if args.verbose else logging.WARNING,
        format="%(levelname)s %(name)s: %(message)s",
    )

    request = json.loads(Path(args.request).read_text("utf-8")) if args.request else {}
    result = json.loads(Path(args.result).read_text("utf-8")) if args.result else {}
    # Ephemeral key for the CLI demo — production supplies a KMS-backed key.
    key = SigningKey.ephemeral()

    try:
        receipt = generate_receipt(
            request,
            result,
            args.verdict,
            key,
            tool_id=args.tool_id,
            tool_version=args.tool_version,
            invoked_at=args.invoked_at,
            tool_call_id=args.tool_call_id,
        )
    except DeployGateError as e:
        print(f"REFUSED: {e}", file=sys.stderr)
        for r in e.reasons:
            print(f"  - {r}", file=sys.stderr)
        return 1

    out_json = json.dumps(receipt, indent=2)
    if args.out:
        Path(args.out).write_text(out_json + "\n", encoding="utf-8")
    print(out_json)
    print(
        f"\nOK: receipt signed with {len(receipt['sources'])} source entries "
        f"from the wixie eval; verifies={verify_receipt(receipt, key.public_key_b64url())}",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":  # pragma: no cover
    sys.exit(_main())
