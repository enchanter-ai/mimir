"""
generate-vectors.py — Adversarial test-vector generator for Mimir provenance envelopes.

For each of the 12 attacks this script:
  1. Starts with a freshly-signed baseline envelope (captured from a live issuer run
     or from the embedded baseline below).
  2. Applies the specified mutation.
  3. Writes attack-NN-<slug>/envelope.json, jwk.json, description.md, expected.json.

Run:
    python generate-vectors.py
Output directory: same directory as this script (test-vectors-adversarial/).
"""
from __future__ import annotations

import base64
import copy
import hashlib
import json
import os
import subprocess
import sys
import time
from pathlib import Path
from datetime import datetime, timezone, timedelta

HERE = Path(__file__).resolve().parent

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def b64url_decode(s: str) -> bytes:
    pad = (4 - len(s) % 4) % 4
    return base64.urlsafe_b64decode(s + "=" * pad)

def b64url_encode(b: bytes) -> str:
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode()

def rfc8785_canonicalize(obj) -> bytes:
    """Minimal RFC 8785 JCS — sorted keys, no whitespace, UTF-8."""
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")

def canonical_for_verification(envelope: dict) -> bytes:
    """Strip signature.value, then canonicalize — matches demo.py logic."""
    e = copy.deepcopy(envelope)
    if "signature" in e and isinstance(e["signature"], dict):
        e["signature"].pop("value", None)
    return rfc8785_canonicalize(e)

def write_attack(slug: str, envelope: dict, jwk: dict, description: str, expected: dict) -> None:
    d = HERE / slug
    d.mkdir(parents=True, exist_ok=True)
    (d / "envelope.json").write_text(json.dumps(envelope, indent=2), encoding="utf-8")
    (d / "jwk.json").write_text(json.dumps(jwk, indent=2), encoding="utf-8")
    (d / "description.md").write_text(description, encoding="utf-8")
    (d / "expected.json").write_text(json.dumps(expected, indent=2), encoding="utf-8")
    print(f"  [+] {slug}/")

# ---------------------------------------------------------------------------
# Obtain a fresh baseline envelope from the running issuer (or use fallback).
# ---------------------------------------------------------------------------

def fetch_baseline() -> tuple[dict, dict]:
    """POST to issuer and GET /v1/key; return (envelope, jwk)."""
    try:
        import requests  # type: ignore
        attest_body = {
            "request": {
                "name": "fetch_document",
                "arguments": {"url": "https://example.com/doc.txt", "format": "text"},
            },
            "result": {
                "tool_use_id": "tu_adversarial_001",
                "content": [{"type": "text", "text": "Article title: A Brief History of Provenance."}],
            },
            "tool_id": "did:web:demo.enchanter-labs.io:tools:fetch-document",
            "tool_version": "1.0.0",
        }
        r = requests.post("http://localhost:8080/v1/attest", json=attest_body, timeout=5)
        r.raise_for_status()
        envelope = r.json()["envelope"]

        rk = requests.get("http://localhost:8080/v1/key", timeout=5)
        rk.raise_for_status()
        jwk = rk.json()

        print(f"  Fetched live baseline — tool_call_id={envelope['tool_call_id']}")
        return envelope, jwk
    except Exception as exc:
        print(f"  WARNING: issuer not reachable ({exc}). Using embedded baseline.")
        return _embedded_baseline()

def _embedded_baseline() -> tuple[dict, dict]:
    """Hard-coded baseline from the demo run (2026-05-13)."""
    envelope = {
        "version": "mcp-provenance/2026-05-13-ed25519",
        "tool_call_id": "3c7f99cc-1700-460a-9475-9cf4d09c907e",
        "tool_id": "did:web:demo.enchanter-labs.io:tools:fetch-document",
        "tool_version": "1.0.0",
        "invoked_at": "2026-05-13T18:23:41Z",
        "invoked_by": "did:enchanter:unverified",
        "request_digest": "sha-256:32e332cf1042b1ebdd559cac6a352ab2a70b4d6b62d4f83e89a277dc8bd8d677",
        "result_digest": "sha-256:9adaf6a4174851569b41b1284a424b4ef296a2392238f8e354bf3a39b2a9f200",
        "sources": [
            {
                "uri": "stub:scoring-engine-not-yet-integrated",
                "confidence": 0,
                "label": "MVP stub — replace with real scoring engine output",
            }
        ],
        "signature": {
            "protected_header": {
                "alg": "Ed25519",
                "key_id": "ephemeral-31edacbc-55b5-415c-b5c7-51fe6fe77dc5",
            },
            "value": "oVPEVHP9apohKYjgZdMxnZSd2WLbV1IADSohgxnviOn__qgFHiowY4HtXTD8kRMWo-QBtvtwqi9KC8l30GcBDw",
        },
    }
    jwk = {
        "kty": "OKP",
        "crv": "Ed25519",
        "x": "l-Z61egxiGQ-UxvY38JqE9yf4ByoM_WVWCC4Tnz4nvg",
        "kid": "ephemeral-31edacbc-55b5-415c-b5c7-51fe6fe77dc5",
        "use": "sig",
    }
    return envelope, jwk

# ---------------------------------------------------------------------------
# Generate a SECOND independent keypair + envelope for cross-envelope attacks.
# ---------------------------------------------------------------------------

def generate_second_keypair_envelope() -> tuple[dict, dict]:
    """
    Generate a second Ed25519 keypair (using nacl) and sign a second envelope.
    Used for attack-06 (tool_call_id swap).
    """
    try:
        import nacl.signing  # type: ignore
        sk = nacl.signing.SigningKey.generate()
        vk = sk.verify_key
        pub_bytes = bytes(vk)
        kid2 = "ephemeral-second-0000-0000-000000000000"

        env2: dict = {
            "version": "mcp-provenance/2026-05-13-ed25519",
            "tool_call_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
            "tool_id": "did:web:other-tool.example.io:tools:other",
            "tool_version": "2.0.0",
            "invoked_at": "2026-05-13T10:00:00Z",
            "invoked_by": "did:enchanter:unverified",
            "request_digest": "sha-256:" + "ab" * 32,
            "result_digest": "sha-256:" + "cd" * 32,
            "sources": [{"uri": "stub:other", "confidence": 0.0, "label": "other stub"}],
            "signature": {
                "protected_header": {"alg": "Ed25519", "key_id": kid2},
            },
        }
        canonical = canonical_for_verification(env2)
        sig_bytes = bytes(sk.sign(canonical).signature)
        env2["signature"]["value"] = b64url_encode(sig_bytes)

        jwk2 = {
            "kty": "OKP",
            "crv": "Ed25519",
            "x": b64url_encode(pub_bytes),
            "kid": kid2,
            "use": "sig",
        }
        return env2, jwk2
    except ImportError:
        # Fallback: reuse the primary envelope with a different tool_call_id
        env2 = {
            "version": "mcp-provenance/2026-05-13-ed25519",
            "tool_call_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
            "tool_id": "did:web:other-tool.example.io:tools:other",
            "tool_version": "2.0.0",
            "invoked_at": "2026-05-13T10:00:00Z",
            "invoked_by": "did:enchanter:unverified",
            "request_digest": "sha-256:" + "ab" * 32,
            "result_digest": "sha-256:" + "cd" * 32,
            "sources": [{"uri": "stub:other", "confidence": 0.0, "label": "other stub"}],
            "signature": {
                "protected_header": {"alg": "Ed25519", "key_id": "ephemeral-second-0000-0000-000000000000"},
                "value": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
            },
        }
        jwk2 = {
            "kty": "OKP",
            "crv": "Ed25519",
            "x": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
            "kid": "ephemeral-second-0000-0000-000000000000",
            "use": "sig",
        }
        return env2, jwk2

# ---------------------------------------------------------------------------
# Main generation
# ---------------------------------------------------------------------------

def main() -> None:
    print("=" * 70)
    print("  Mimir adversarial test-vector generator")
    print("=" * 70)

    baseline, jwk = fetch_baseline()
    env2, jwk2 = generate_second_keypair_envelope()

    # -----------------------------------------------------------------------
    # Attack 01 — signature.value truncated (drop last 4 bytes)
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    sig_bytes = b64url_decode(e["signature"]["value"])
    truncated = sig_bytes[:-4]
    e["signature"]["value"] = b64url_encode(truncated)
    write_attack(
        "attack-01-sig-truncated",
        e, jwk,
        "## Attack 01 — Signature truncated (last 4 bytes dropped)\n\n"
        "The `signature.value` field has been shortened by removing the last 4 bytes "
        "of the 64-byte Ed25519 signature before re-encoding to base64url. "
        "The resulting value is 60 bytes, not the required 64. "
        "A correct verifier **MUST** reject this because the decoded signature "
        "does not match the expected 64-byte Ed25519 length, and cryptographic "
        "verification will fail (`VE-008`). "
        "Spec §11 (Profile Identifiers) mandates the Ed25519 profile produces "
        "a 64-byte signature; §9.3 step 10 requires cryptographic verification "
        "to succeed; §10.2 step 11 maps verification failure to `VE-008`.\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§9.3, §10.2, §11"},
    )

    # -----------------------------------------------------------------------
    # Attack 02 — one byte flipped mid-signature
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    sig_bytes = bytearray(b64url_decode(e["signature"]["value"]))
    flip_pos = len(sig_bytes) // 2
    sig_bytes[flip_pos] ^= 0xFF  # flip all bits of the middle byte
    e["signature"]["value"] = b64url_encode(bytes(sig_bytes))
    write_attack(
        "attack-02-sig-bit-flip",
        e, jwk,
        "## Attack 02 — One byte flipped mid-signature\n\n"
        "Byte at position `len/2` of the 64-byte Ed25519 signature has been XOR-flipped "
        "(all 8 bits inverted). The base64url-encoded length is unchanged (still 86 chars), "
        "so the envelope passes structural checks. "
        "A correct verifier **MUST** reject this because the signature no longer "
        "verifies over the canonical form (`VE-008`). "
        "Spec §10.2 step 10–11 requires the Ed25519 signature to verify; "
        "§15.4 (Timing Side-Channels) demands constant-time rejection to prevent "
        "byte-by-byte oracle attacks — this vector exercises that path.\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§9.3, §10.2, §15.4"},
    )

    # -----------------------------------------------------------------------
    # Attack 03 — signature.value all-zeros (64 zero bytes)
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    e["signature"]["value"] = b64url_encode(bytes(64))
    write_attack(
        "attack-03-sig-all-zeros",
        e, jwk,
        "## Attack 03 — Signature replaced with 64 zero bytes\n\n"
        "The `signature.value` field has been replaced with the base64url encoding "
        "of 64 zero bytes (`AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA`). "
        "This is the trivially forgeable 'null signature'. "
        "A correct verifier **MUST** reject this via `VE-008` (signature invalid). "
        "Spec §9.3 step 10 verifies the signature bytes over the canonical form; "
        "§10.2 step 11 maps failure to `VE-008`. "
        "A verifier that accepts all-zero signatures is trivially bypassed.\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§9.3, §10.2"},
    )

    # -----------------------------------------------------------------------
    # Attack 04 — request_digest altered after signing
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    # Flip one hex char in the digest
    prefix, hex_part = e["request_digest"].split(":", 1)
    flipped = hex_part[:8] + ("a" if hex_part[8] != "a" else "b") + hex_part[9:]
    e["request_digest"] = prefix + ":" + flipped
    write_attack(
        "attack-04-request-digest-altered",
        e, jwk,
        "## Attack 04 — `request_digest` altered after signing\n\n"
        "One hex character in `request_digest` has been changed after the envelope "
        "was signed. The signature was computed over the original (correct) digest; "
        "the altered digest causes the canonical form to differ from what was signed. "
        "A correct verifier **MUST** reject this on two independent grounds: "
        "(1) cryptographic signature verification fails because the canonical bytes differ "
        "(`VE-008`), and (2) the request digest does not match the Consumer's "
        "independently computed digest (`VE-009`). "
        "Spec §9.2 includes `request_digest` in the signed canonical form; "
        "§10.2 step 12 cross-checks it against the originating request; "
        "§15.11 (Unbound Result Substitution) describes the threat this field defends against.\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§9.2, §10.2, §15.11"},
    )

    # -----------------------------------------------------------------------
    # Attack 05 — result_digest altered after signing
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    prefix, hex_part = e["result_digest"].split(":", 1)
    flipped = hex_part[:12] + ("f" if hex_part[12] != "f" else "0") + hex_part[13:]
    e["result_digest"] = prefix + ":" + flipped
    write_attack(
        "attack-05-result-digest-altered",
        e, jwk,
        "## Attack 05 — `result_digest` altered after signing\n\n"
        "One hex character in `result_digest` has been changed after the envelope "
        "was signed. The result digest is included in the signed canonical form; "
        "altering it post-signing invalidates the signature. "
        "A correct verifier **MUST** reject this: signature verification fails (`VE-008`) "
        "because the canonical bytes now differ from what the Producer signed; "
        "additionally, the digest does not match the Consumer's observed result (`VE-010`). "
        "Spec §9.2 mandates that `result_digest` is part of the signed canonical form; "
        "§10.2 step 13 cross-checks it against the `content` field; "
        "§6.9 defines the digest computation; §15.11 is the threat model.\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§6.9, §9.2, §10.2, §15.11"},
    )

    # -----------------------------------------------------------------------
    # Attack 06 — tool_call_id swapped with another envelope's
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    # Swap in the tool_call_id from the second envelope
    e["tool_call_id"] = env2["tool_call_id"]
    # Signature is now invalid because tool_call_id is in the signed canonical form
    write_attack(
        "attack-06-tool-call-id-swap",
        e, jwk,
        "## Attack 06 — `tool_call_id` swapped with another envelope's\n\n"
        "The `tool_call_id` has been replaced with the value from an unrelated envelope "
        f"(`{env2['tool_call_id']}`). "
        "The signature was computed over the original `tool_call_id`; swapping it "
        "changes the canonical bytes and invalidates the signature (`VE-008`). "
        "Beyond the signature failure, the Consumer **MUST** also reject this via `VE-002` "
        "because the `tool_call_id` no longer matches any pending tool-call in the "
        "Consumer's session. "
        "Spec §6.3 requires the Consumer to check `tool_call_id` against the originating "
        "session call (`VE-002`); §9.2 includes `tool_call_id` in the signed canonical form; "
        "§15.2 (Replay Attacks) and §15.19 (Cross-Session Replay) describe the threat.\n",
        {"verdict": "REJECT", "reason_code": "VE-002", "spec_section": "§6.3, §9.2, §15.2, §15.19"},
    )

    # -----------------------------------------------------------------------
    # Attack 07 — invoked_at moved 1 hour into the future (replay-window violation)
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    # Parse original time and add 1 hour
    try:
        orig_ts = datetime.fromisoformat(e["invoked_at"].replace("Z", "+00:00"))
    except Exception:
        orig_ts = datetime(2026, 5, 13, 18, 23, 41, tzinfo=timezone.utc)
    future_ts = orig_ts + timedelta(hours=1)
    e["invoked_at"] = future_ts.strftime("%Y-%m-%dT%H:%M:%SZ")
    # Signature is invalid because invoked_at changed
    write_attack(
        "attack-07-invoked-at-future",
        e, jwk,
        "## Attack 07 — `invoked_at` moved 1 hour into the future\n\n"
        f"`invoked_at` has been changed from the original value to `{e['invoked_at']}`, "
        "which is 1 hour in the future relative to the original signing time. "
        "The signature was computed over the original `invoked_at`; altering it invalidates "
        "the signature (`VE-008`). "
        "Even if the signature were valid, a Consumer **SHOULD** reject envelopes whose "
        "`invoked_at` exceeds the Consumer's clock-skew tolerance (`VE-004`). "
        "The non-normative default tolerance is 300 seconds (§6.6); 1 hour far exceeds it. "
        "Spec §6.6 defines the timestamp skew check; §9.2 includes `invoked_at` in the "
        "signed canonical form; §15.2 discusses `invoked_at` as a replay-window bound.\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§6.6, §9.2, §15.2"},
    )

    # -----------------------------------------------------------------------
    # Attack 08 — invoked_at moved 1 hour into the past (stale)
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    try:
        orig_ts = datetime.fromisoformat(baseline["invoked_at"].replace("Z", "+00:00"))
    except Exception:
        orig_ts = datetime(2026, 5, 13, 18, 23, 41, tzinfo=timezone.utc)
    past_ts = orig_ts - timedelta(hours=1)
    e["invoked_at"] = past_ts.strftime("%Y-%m-%dT%H:%M:%SZ")
    write_attack(
        "attack-08-invoked-at-past",
        e, jwk,
        "## Attack 08 — `invoked_at` moved 1 hour into the past (stale)\n\n"
        f"`invoked_at` has been changed to `{e['invoked_at']}`, which is 1 hour earlier "
        "than the original signing time. "
        "The signature was computed over the original `invoked_at`; altering it invalidates "
        "the signature (`VE-008`). "
        "Even if the signature were valid, a Consumer **MAY** reject the envelope via "
        "`VE-004` if the timestamp exceeds the Consumer's clock-skew tolerance (§6.6, "
        "non-normative default 300 seconds). "
        "This vector tests the stale-envelope variant of the replay-window check. "
        "Spec §6.6 defines `VE-004`; §15.2 names `invoked_at` as a replay bound.\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§6.6, §9.2, §15.2"},
    )

    # -----------------------------------------------------------------------
    # Attack 09 — signature.protected_header.alg changed from Ed25519 to "none"
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    e["signature"]["protected_header"]["alg"] = "none"
    # Note: alg is inside protected_header which IS part of the signed canonical form.
    # Changing alg changes the canonical bytes, so the existing signature is invalid.
    write_attack(
        "attack-09-alg-downgrade-none",
        e, jwk,
        "## Attack 09 — `signature.protected_header.alg` changed from `Ed25519` to `\"none\"`\n\n"
        "The algorithm identifier in `signature.protected_header.alg` has been changed "
        "from `Ed25519` to `none`. This is a signature algorithm downgrade attack (§15.3). "
        "A correct verifier **MUST** reject this via `VE-006` (algorithm mismatch) because "
        "`none` does not match the algorithm declared in `version` (`mcp-provenance/2026-05-13-ed25519`). "
        "In v2, `protected_header` is included in the signed canonical form (§9.2), so "
        "changing `alg` also invalidates the signature (`VE-008`). "
        "The spec requires the `VE-006` check to run **before** signature verification "
        "(§10.2 step 3) as defence-in-depth. "
        "Spec §6.13, §10.2 step 3, §15.3 (Signature Algorithm Downgrade).\n",
        {"verdict": "REJECT", "reason_code": "VE-006", "spec_section": "§6.13, §10.2, §15.3"},
    )

    # -----------------------------------------------------------------------
    # Attack 10 — signature.protected_header.key_id swapped to a different key
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    original_kid = e["signature"]["protected_header"]["key_id"]
    e["signature"]["protected_header"]["key_id"] = "ephemeral-attacker-00000000-0000-0000-0000-000000000000"
    # key_id is in protected_header which IS signed; changing it invalidates the signature.
    write_attack(
        "attack-10-key-id-swap",
        e, jwk,
        "## Attack 10 — `signature.protected_header.key_id` swapped to a different key\n\n"
        f"The `key_id` in `signature.protected_header` has been replaced with "
        f"`ephemeral-attacker-00000000-0000-0000-0000-000000000000` "
        f"(original: `{original_kid}`). "
        "In v2, `protected_header` (including `key_id`) is part of the signed canonical "
        "form (§9.2), so this substitution invalidates the signature (`VE-008`). "
        "A Consumer **MUST** fail DID resolution for the substituted key (`VE-003`) "
        "because the attacker-controlled `key_id` does not exist in the legitimate "
        "tool's DID document. "
        "This vector exercises the structural fix for the v1 SSRF vulnerability: "
        "in v2, any `key_id` modification is self-defeating because it breaks the "
        "signature before DID resolution can be attempted. "
        "Spec §6.13, §9.2, §10.2 step 6–7, §15.18 (Replay Across Key-Rotation Forks), "
        "§15.20 (Pre-Validation SSRF via key_id Malleability).\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§6.13, §9.2, §10.2, §15.18, §15.20"},
    )

    # -----------------------------------------------------------------------
    # Attack 11 — JSON whitespace added (should PASS — canonicalizer is correct)
    # -----------------------------------------------------------------------
    # Produce a pretty-printed JSON string of the baseline envelope, then parse it
    # back. A correct verifier MUST verify this because:
    #   - The verification path parses JSON -> strips signature.value -> canonicalize
    #   - Whitespace is irrelevant after parsing; canonical form is identical.
    e = copy.deepcopy(baseline)
    # Write the envelope.json with extra indentation / whitespace
    # We represent it as a pretty-printed JSON with extra blank lines (via a raw
    # string that parses to the same object).
    pretty_json_str = json.dumps(e, indent=4, sort_keys=False)
    # Parse it back to confirm round-trip equality
    reparsed = json.loads(pretty_json_str)
    assert reparsed == e, "Whitespace round-trip failed"

    # For attack-11 we write the pretty-printed string directly to envelope.json
    # rather than using json.dumps(e, indent=2) so it's clearly different bytes.
    d = HERE / "attack-11-whitespace-added"
    d.mkdir(parents=True, exist_ok=True)
    (d / "envelope.json").write_text(pretty_json_str, encoding="utf-8")
    (d / "jwk.json").write_text(json.dumps(jwk, indent=2), encoding="utf-8")
    (d / "description.md").write_text(
        "## Attack 11 — Extra JSON whitespace added (should VERIFY, not reject)\n\n"
        "The envelope has been pretty-printed with 4-space indentation instead of the "
        "compact form. No field values have been changed; only serialization whitespace "
        "differs. "
        "A correct verifier **MUST** accept this and return VERIFY, because the verification "
        "path parses the JSON first, then strips `signature.value`, then applies "
        "RFC 8785 JCS (which produces a unique canonical form regardless of input whitespace). "
        "The canonical bytes produced by the verifier are identical whether the input was "
        "compact or pretty-printed. "
        "If the verifier rejects this it has a bug: it is comparing raw bytes instead "
        "of canonical form. "
        "Spec §9.2 (Compute Canonical Form) mandates JCS canonicalization per RFC 8785; "
        "§15.16 (Forward-Compatibility) requires unknown-field and whitespace tolerance.\n",
        encoding="utf-8",
    )
    (d / "expected.json").write_text(
        json.dumps({"verdict": "VERIFY", "reason_code": "OK", "spec_section": "§9.2, §15.16"}, indent=2),
        encoding="utf-8",
    )
    print(f"  [+] attack-11-whitespace-added/")

    # -----------------------------------------------------------------------
    # Attack 12 — extra unknown field added outside signature scope
    # Spec §15.16: Consumer MUST compute canonical form over the FULL received object,
    # including unknown fields. An unknown field changes the canonical bytes, so the
    # existing signature (signed without that field) becomes invalid -> REJECT VE-008.
    # The spec does NOT say additional fields are transparent; it says the verifier
    # must include them in the canonical form. A field added post-signing therefore
    # breaks the signature.
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    e["x_extra_field"] = "attacker-controlled-data"
    write_attack(
        "attack-12-extra-unknown-field",
        e, jwk,
        "## Attack 12 — Extra unknown field added outside signature scope\n\n"
        "A new top-level field `x_extra_field` with value `attacker-controlled-data` "
        "has been injected into the envelope after signing. "
        "Per §15.16 (Forward-Compatibility and Unknown-Field Handling), a Consumer "
        "**MUST** compute the canonical form over the **full** received envelope, "
        "including unknown fields. "
        "Because the Producer signed a canonical form that did not contain `x_extra_field`, "
        "the canonical form the Consumer computes (with the extra field) differs from "
        "what was signed, and signature verification **MUST** fail (`VE-008`). "
        "Verdict: **REJECT**. "
        "Note: §15.16 also says a Consumer **MAY** conservatively reject unknown fields "
        "with `VE-012` (unknown_field_stripped/present) — both REJECT paths are valid. "
        "The primary rejection reason here is `VE-008` because the signature no longer "
        "covers the canonical form of the received object. "
        "Spec §9.2, §10.2 step 8–11, §15.16.\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§9.2, §10.2, §15.16"},
    )

    # -----------------------------------------------------------------------
    # Attack 13 — tool_id altered to claim a different tool produced this envelope.
    # Tests that envelope provenance binds the tool's identity, not just the request.
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    e["tool_id"] = "did:web:attacker.example:tools:impersonator"
    write_attack(
        "attack-13-tool-id-altered",
        e, jwk,
        "## Attack 13 — tool_id altered post-signing\n\n"
        "The envelope's `tool_id` has been changed from the original signed value to "
        "`did:web:attacker.example:tools:impersonator`. An attacker who intercepts an "
        "honest tool's envelope cannot re-attribute the call to a different tool "
        "without invalidating the signature. Since `tool_id` is in the canonical bytes "
        "the Producer signed over (§6.4, §9.2), changing it post-signing causes the "
        "Consumer's recomputed canonical form to diverge from what was signed, and "
        "signature verification **MUST** fail (`VE-008`). Verdict: **REJECT**. "
        "Spec §6.4, §9.2, §10.2 step 8–11.\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§6.4, §9.2, §10.2"},
    )

    # -----------------------------------------------------------------------
    # Attack 14 — invoked_by altered to claim a different client identity.
    # Tests that client-identity attestation (relevant for DPoP-extension envelopes
    # at validation level 3) is bound under the same signature.
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    e["invoked_by"] = "did:jwk:fakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFake"
    write_attack(
        "attack-14-invoked-by-altered",
        e, jwk,
        "## Attack 14 — invoked_by altered post-signing\n\n"
        "The envelope's `invoked_by` has been changed from the original signed value "
        "(`did:enchanter:unverified` in the baseline) to a fabricated `did:jwk:fake…`. "
        "An attacker who captures an honest envelope cannot re-attribute the request "
        "to a different client without breaking the signature. `invoked_by` is part of "
        "the canonical bytes the Producer signed (§6.4); any post-signing modification "
        "fails verification with `VE-008`. Verdict: **REJECT**. "
        "Spec §6.4, §6.11 (ClientIdentityProof extension binding), §10.2.\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§6.4, §6.11, §10.2"},
    )

    # -----------------------------------------------------------------------
    # Attack 15 — sources array tampering (injected fabricated source entry).
    # Tests that the source-binding property promised by §6.5 holds.
    # -----------------------------------------------------------------------
    e = copy.deepcopy(baseline)
    e["sources"] = e.get("sources", []) + [
        {
            "uri": "https://attacker.example/fake-authority",
            "confidence": 1.0,
            "label": "Falsely claimed peer-review verification",
        }
    ]
    write_attack(
        "attack-15-sources-injection",
        e, jwk,
        "## Attack 15 — `sources` array tampering (injection)\n\n"
        "An additional fabricated entry has been appended to the envelope's `sources` "
        "array claiming a high-confidence external authority. Mimir's source-binding "
        "guarantee (spec §1.2 novelty claim, §6.5) is that the upstream sources a "
        "Producer claims it consulted are signed under the same envelope as the request "
        "and result. An attacker who injects a fake source post-signing changes the "
        "canonical bytes the Consumer recomputes, and the Producer's signature no longer "
        "matches. Verdict: **REJECT** with `VE-008`. "
        "Spec §1.2, §6.5, §9.2, §10.2 step 8–11.\n",
        {"verdict": "REJECT", "reason_code": "VE-008", "spec_section": "§1.2, §6.5, §9.2, §10.2"},
    )

    print()
    print(f"Generated 15 attack vectors in {HERE}/")
    print("Run verify-all.py to assert expected verdicts.")

if __name__ == "__main__":
    main()
