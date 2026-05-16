"""
verify-all.py — Adversarial test-vector harness for Mimir provenance envelopes.

Runs each attack-NN-<slug>/ vector through the same verification logic as demo.py
and asserts the expected verdict (REJECT or VERIFY).

Exit code: 0 if all assertions pass, 1 if any fail.

Usage:
    python verify-all.py
"""
from __future__ import annotations

import base64
import copy
import json
import sys
from pathlib import Path
from typing import Any

HERE = Path(__file__).resolve().parent

# ---------------------------------------------------------------------------
# Verification logic (mirrors demo.py canonical_form_for_verification + PyNaCl)
# ---------------------------------------------------------------------------

try:
    import nacl.signing
    import nacl.exceptions
except ImportError:
    import subprocess
    subprocess.run([sys.executable, "-m", "pip", "install", "pynacl", "--quiet"], check=True)
    import nacl.signing
    import nacl.exceptions


def b64url_decode(s: str) -> bytes:
    pad = (4 - len(s) % 4) % 4
    return base64.urlsafe_b64decode(s + "=" * pad)


def rfc8785_canonicalize(obj: Any) -> bytes:
    """Minimal RFC 8785 JCS — sorted keys, no whitespace, UTF-8."""
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def canonical_form_for_verification(envelope: dict) -> bytes:
    """
    Compute canonical form for verification per spec §9.2:
    Strip signature.value, keep everything else (including protected_header),
    then apply JCS.
    """
    import copy
    e = copy.deepcopy(envelope)
    if "signature" in e and isinstance(e["signature"], dict):
        e["signature"].pop("value", None)
    return rfc8785_canonicalize(e)


def verify_envelope(envelope: dict, jwk: dict) -> tuple[str, str]:
    """
    Returns ("VERIFY", "OK") or ("REJECT", "<reason_code>").

    Implements spec §10.1 (well-formed) + §10.2 (cryptographically valid):
      Step 1: signature field present
      Step 2: alg matches version
      Step 3: decode public key from JWK
      Step 4: compute canonical form (over full envelope including unknown fields)
      Step 5: decode signature bytes
      Step 6: verify Ed25519 signature
    Note: VE-002 (tool_call_id session check), VE-004 (clock skew), VE-009/010
    (digest cross-checks against original request/result) require Consumer-side
    session context that this offline harness does not have. Those checks are
    noted where applicable, but the harness can only assert VE-008 / VE-006 / VE-007.
    """
    sig_obj = envelope.get("signature")
    if not sig_obj or not isinstance(sig_obj, dict):
        return ("REJECT", "VE-007")

    sig_value = sig_obj.get("value")
    if not sig_value:
        return ("REJECT", "VE-007")

    protected = sig_obj.get("protected_header", {})
    alg = protected.get("alg", "")
    version = envelope.get("version", "")

    # §10.2 step 3 — alg must match version
    if "ed25519" in version.lower() and alg.lower() != "ed25519":
        return ("REJECT", "VE-006")
    if alg.lower() == "none":
        return ("REJECT", "VE-006")

    # Decode public key from JWK (OKP Ed25519)
    pub_b64 = jwk.get("x", "")
    try:
        pub_bytes = b64url_decode(pub_b64)
        vk = nacl.signing.VerifyKey(pub_bytes)
    except Exception as exc:
        return ("REJECT", f"VE-003 (key decode failed: {exc})")

    # §9.2 — canonical form over full envelope (including unknown fields per §15.16)
    canonical = canonical_form_for_verification(envelope)

    # Decode signature bytes
    try:
        sig_bytes = b64url_decode(sig_value)
    except Exception as exc:
        return ("REJECT", f"VE-008 (base64 decode failed: {exc})")

    # Ed25519 signature must be exactly 64 bytes
    if len(sig_bytes) != 64:
        return ("REJECT", f"VE-008 (signature length {len(sig_bytes)} != 64)")

    # §10.2 steps 10–11 — cryptographic verification
    try:
        vk.verify(canonical, sig_bytes)
    except nacl.exceptions.BadSignatureError:
        return ("REJECT", "VE-008")

    return ("VERIFY", "OK")


# ---------------------------------------------------------------------------
# Load and run all attack directories
# ---------------------------------------------------------------------------

def run_all() -> int:
    attack_dirs = sorted(d for d in HERE.iterdir() if d.is_dir() and d.name.startswith("attack-"))
    if not attack_dirs:
        print("No attack-NN-* directories found. Run generate-vectors.py first.")
        return 1

    results: list[tuple[str, str, str, bool]] = []  # (name, expected_verdict, actual_verdict, passed)

    print(f"\n{'=' * 72}")
    print(f"  Mimir adversarial vector harness — {len(attack_dirs)} vectors")
    print(f"{'=' * 72}")

    for d in attack_dirs:
        env_path = d / "envelope.json"
        jwk_path = d / "jwk.json"
        exp_path = d / "expected.json"

        if not env_path.exists() or not jwk_path.exists() or not exp_path.exists():
            print(f"  [SKIP] {d.name} — missing envelope.json, jwk.json, or expected.json")
            continue

        envelope = json.loads(env_path.read_text(encoding="utf-8"))
        jwk = json.loads(jwk_path.read_text(encoding="utf-8"))
        expected = json.loads(exp_path.read_text(encoding="utf-8"))

        expected_verdict = expected.get("verdict", "REJECT")
        expected_reason = expected.get("reason_code", "")
        spec_section = expected.get("spec_section", "")

        actual_verdict, actual_reason = verify_envelope(envelope, jwk)

        passed = actual_verdict == expected_verdict
        status = "PASS" if passed else "FAIL"
        results.append((d.name, expected_verdict, actual_verdict, passed))

        mark = "OK" if passed else "!!"
        print(
            f"  [{mark}] {d.name:<45} "
            f"expected={expected_verdict:<8} got={actual_verdict:<8} "
            f"reason={actual_reason:<30} spec={spec_section}"
        )
        if not passed:
            print(f"       ^^^ UNEXPECTED: wanted {expected_verdict}, got {actual_verdict} ({actual_reason})")

    print(f"\n{'=' * 72}")
    n_pass = sum(1 for _, _, _, p in results if p)
    n_fail = sum(1 for _, _, _, p in results if not p)
    print(f"  TOTAL: {len(results)} vectors | PASS: {n_pass} | FAIL: {n_fail}")
    print(f"{'=' * 72}\n")

    # Print latent-bug summary for any failures
    if n_fail > 0:
        print("LATENT BUGS / UNEXPECTED BEHAVIOUR:")
        for name, exp, act, passed in results:
            if not passed:
                print(f"  {name}: expected {exp}, got {act}")
                print(f"    -> This indicates a bug in the verifier or a spec gap.")
        print()

    return 0 if n_fail == 0 else 1


if __name__ == "__main__":
    sys.exit(run_all())
