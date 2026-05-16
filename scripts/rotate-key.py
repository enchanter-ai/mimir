"""rotate-key.py — safely rotate the Mimir issuer's signing key.

A2 (keystore.go) defined the file format for historical-keys.json. This
script is the operator-facing workflow that produces it correctly.

Run BEFORE you point the issuer at a new KMS key (or before restart with
a new ephemeral seed). The script:

  1. Fetches the currently-active JWK from a running issuer.
  2. Appends it to historical-keys.json with status="retired" (or
     "revoked" if --revoke is set — use this for compromise scenarios).
  3. Verifies the resulting JSON parses + has no duplicate kids.
  4. Prints the operator's next steps.

The script does NOT do the actual key swap (that's a KMS API call or env
restart, which depends on your deployment). It only manages the
historical-keys file so verifiers can continue validating envelopes
signed under the outgoing key.

Usage:

    # Retire the current key (normal scheduled rotation).
    python scripts/rotate-key.py \\
        --issuer https://issuer.example.com \\
        --historical historical-keys.json

    # Revoke the current key (key was compromised — see RUNBOOK § R1).
    python scripts/rotate-key.py \\
        --issuer https://issuer.example.com \\
        --historical historical-keys.json \\
        --revoke
"""
from __future__ import annotations

import argparse
import json
import os
import sys
from datetime import datetime, timezone
from pathlib import Path

try:
    import requests
except ImportError:
    import subprocess
    subprocess.run([sys.executable, "-m", "pip", "install", "requests"], check=True)
    import requests


def fetch_active_jwk(issuer_base: str) -> dict:
    url = issuer_base.rstrip("/") + "/v1/key"
    r = requests.get(url, timeout=10)
    r.raise_for_status()
    jwk = r.json()
    # Sanity check — required fields per RFC 7517 + Mimir's JWK shape.
    for field in ("kty", "crv", "x", "kid"):
        if not jwk.get(field):
            sys.exit(f"ERROR: issuer's active JWK missing required field {field!r}")
    return jwk


def load_historical(path: Path) -> list[dict]:
    if not path.exists():
        return []
    raw = path.read_text(encoding="utf-8").strip()
    if not raw:
        return []
    keys = json.loads(raw)
    if not isinstance(keys, list):
        sys.exit(f"ERROR: {path} must contain a JSON array of JWK objects")
    return keys


def save_historical(path: Path, keys: list[dict]) -> None:
    # Atomic write — write tmp then rename.
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(keys, indent=2), encoding="utf-8")
    os.replace(tmp, path)


def append_jwk(historical: list[dict], jwk: dict, status: str) -> list[dict]:
    # Reject duplicate kid — that would be a no-op or worse, silent state confusion.
    for existing in historical:
        if existing.get("kid") == jwk.get("kid"):
            sys.exit(
                f"ERROR: kid {jwk['kid']!r} already in historical file as "
                f"status={existing.get('status', 'retired')!r}. Aborting."
            )

    retired_jwk = {
        "kty":    jwk["kty"],
        "crv":    jwk["crv"],
        "x":      jwk["x"],
        "kid":    jwk["kid"],
        "use":    jwk.get("use", "sig"),
        "alg":    jwk.get("alg", "EdDSA"),
        "status": status,
    }
    return historical + [retired_jwk]


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--issuer", required=True, help="Base URL of the running issuer (e.g. http://localhost:8080)")
    p.add_argument("--historical", default="historical-keys.json", help="Path to historical-keys.json")
    p.add_argument("--revoke", action="store_true",
                   help="Mark the current key as REVOKED (use ONLY for compromise — verifiers will reject envelopes signed under this key)")
    args = p.parse_args()

    status = "revoked" if args.revoke else "retired"
    print(f"==== Mimir key-rotation ({status}) ====")
    print(f"  issuer        : {args.issuer}")
    print(f"  historical    : {args.historical}")
    print(f"  status to set : {status}")
    print(f"  rotated at    : {datetime.now(timezone.utc).isoformat()}")
    print()

    # 1. Fetch the current active JWK.
    print("[1/3] Fetch active JWK from issuer")
    jwk = fetch_active_jwk(args.issuer)
    print(f"      kid : {jwk['kid']}")
    print(f"      kty : {jwk['kty']} / crv : {jwk['crv']}")

    # 2. Load + extend historical file.
    print("\n[2/3] Append outgoing key to historical-keys.json")
    path = Path(args.historical)
    historical = load_historical(path)
    print(f"      existing entries : {len(historical)}")
    updated = append_jwk(historical, jwk, status)
    save_historical(path, updated)
    print(f"      wrote            : {path}")
    print(f"      new entries      : {len(updated)} (added 1, status={status!r})")

    # 3. Operator next steps.
    print("\n[3/3] Operator next steps")
    if args.revoke:
        print("      ! Key was REVOKED — all envelopes signed under it are now suspect.")
        print("      ! Disclose the rotation publicly (see RUNBOOK § R1).")
    print(f"      1. Set ISSUER_HISTORICAL_KEYS_FILE={path.resolve()} on the new issuer instance.")
    print(f"      2. Generate the NEW signing key (KMS or restart-with-new-seed).")
    print(f"      3. Restart the issuer pointing at the new key.")
    print(f"      4. Verify: curl {args.issuer.rstrip('/')}/v1/keys should now show 2+ keys,")
    print(f"         with the new one as status='active' and {jwk['kid']!r} as status='{status}'.")
    print()
    print("==== ROTATION FILE OK ====")
    return 0


if __name__ == "__main__":
    sys.exit(main())
