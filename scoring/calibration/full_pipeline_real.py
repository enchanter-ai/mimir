"""End-to-end pipeline using REAL Claude Sonnet 4.6 scoring (no MOCK_MODE).

Connects to a scoring service on :9090 (started with ANTHROPIC_API_KEY set)
and an issuer on :8090. Submits a tool-call result, runs full pipeline, and
externally verifies the resulting envelope with PyNaCl.

Differs from demo.py: that one hardcodes MOCK_MODE=1 and spawns both services.
This one assumes both are running with real credentials.
"""
from __future__ import annotations

import base64
import json
import os
import sys

try:
    import nacl.signing, nacl.exceptions
    import requests
except ImportError:
    import subprocess
    subprocess.run([sys.executable, "-m", "pip", "install", "pynacl", "requests"], check=True)
    import nacl.signing, nacl.exceptions
    import requests

SCORING_URL = os.environ.get("SCORING_URL", "http://localhost:9090")
ISSUER_URL  = os.environ.get("ISSUER_URL",  "http://localhost:8090")


def b64url_decode(s: str) -> bytes:
    pad = (4 - len(s) % 4) % 4
    return base64.urlsafe_b64decode(s + ("=" * pad))


def rfc8785(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def canonical_for_verify(env):
    e = json.loads(json.dumps(env))
    if "signature" in e and isinstance(e["signature"], dict):
        e["signature"].pop("value", None)
    return rfc8785(e)


def banner(s):
    print(f"\n{'=' * 78}\n  {s}\n{'=' * 78}")


def main():
    banner("Mimir end-to-end with REAL Claude Sonnet 4.6 (no MOCK_MODE)")
    print(f"  scoring : {SCORING_URL}")
    print(f"  issuer  : {ISSUER_URL}")

    # ---- A high-quality tool-call result. ----
    request = {
        "tool_name": "fetch_document",
        "tool_use_id": "tu_real_001",
        "input": {"url": "https://example.com/article/transformer-arch", "format": "text"},
        "model_id": "claude-sonnet-4-6",
        "prompt_version": "v1.0.0",
    }
    result = {
        "tool_use_id": "tu_real_001",
        "content": [
            {
                "type": "text",
                "text": (
                    "Fetched https://example.com/article/transformer-arch (200 OK, "
                    "Content-Type: text/html, 23,418 bytes). Extracted body text "
                    "(8,914 chars after boilerplate removal). Title: 'Attention Is "
                    "All You Need'. Authors: Vaswani et al., 2017. Abstract retrieved "
                    "verbatim. Full text available at the source URL."
                ),
            },
        ],
    }

    # ---- Step 1: real-Claude scoring ----
    banner("Step 1 — Scoring (real claude-sonnet-4-6)")
    score_req = {
        "request": request,
        "result": result,
        "metadata": {"session_id": "real-e2e", "timestamp": "2026-05-15T22:30:00Z"},
    }
    r = requests.post(f"{SCORING_URL}/v1/score", json=score_req, timeout=120)
    r.raise_for_status()
    v = r.json()
    print(f"  verdict  : {v['verdict']}")
    print(f"  sigma    : {v['sigma']:.4f}")
    print(f"  overall  : {v['overall']:.2f}")
    print(f"  axes     : {[(a['axis'], a['score']) for a in v['axes']]}")
    print(f"  asserts  : {sum(1 for a in v['assertions'] if a['passed'])}/8 passed")
    print(f"  model    : {v['model_used']}")

    if v["verdict"] != "DEPLOY":
        print(f"\n  Note: verdict is {v['verdict']}, would NOT proceed to signing in production.")
        print(f"  Proceeding anyway for end-to-end demo (with the real numbers above attached).")

    # ---- Step 2: issuer signs envelope ----
    banner("Step 2 — Envelope construction + Ed25519 signing")
    attest_req = {
        "request": {"name": request["tool_name"], "arguments": request["input"]},
        "result": result,
        "tool_id": "did:web:example.com:tools:fetch-document",
        "tool_version": "1.0.0",
    }
    r = requests.post(f"{ISSUER_URL}/v1/attest", json=attest_req, timeout=10)
    r.raise_for_status()
    env = r.json()["envelope"]
    print(f"  envelope.tool_call_id   : {env['tool_call_id']}")
    print(f"  envelope.request_digest : {env['request_digest']}")
    print(f"  envelope.result_digest  : {env['result_digest']}")
    print(f"  signature.alg           : {env['signature']['protected_header']['alg']}")
    print(f"  signature.value (head)  : {env['signature']['value'][:60]}...")

    # ---- Step 3: external verification ----
    banner("Step 3 — External Ed25519 verification with PyNaCl")
    jwk = requests.get(f"{ISSUER_URL}/v1/key", timeout=5).json()
    pub = b64url_decode(jwk["x"])
    canonical = canonical_for_verify(env)
    sig = b64url_decode(env["signature"]["value"])
    print(f"  jwk.kid       : {jwk['kid']}")
    print(f"  canonical len : {len(canonical)} bytes")
    print(f"  signature len : {len(sig)} bytes")
    vk = nacl.signing.VerifyKey(pub)
    try:
        vk.verify(canonical, sig)
        print(f"\n  [OK] FULL PIPELINE PASS")
        print(f"       Real Claude scored sigma={v['sigma']:.2f} overall={v['overall']:.2f},")
        print(f"       envelope signed with Ed25519, externally verified.")
        return 0
    except nacl.exceptions.BadSignatureError as e:
        print(f"\n  [FAIL] signature did not verify: {e}")
        return 1


if __name__ == "__main__":
    sys.exit(main())
