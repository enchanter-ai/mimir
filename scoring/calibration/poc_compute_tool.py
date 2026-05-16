"""POC variant — a deterministic-compute tool where every axis should score
uniformly high. Compute tools are their own source-of-truth, so the
faithfulness_to_source axis has no slack."""
from __future__ import annotations

import base64
import json
import os
import sys
import time

import nacl.exceptions
import nacl.signing
import requests

SCORING_URL = os.environ.get("SCORING_URL", "http://localhost:9090")
ISSUER_URL  = os.environ.get("ISSUER_URL",  "http://localhost:8090")


def b64url_decode(s):
    pad = (4 - len(s) % 4) % 4
    return base64.urlsafe_b64decode(s + "=" * pad)


def rfc8785(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def canonical_for_verify(env):
    e = json.loads(json.dumps(env))
    if "signature" in e and isinstance(e["signature"], dict):
        e["signature"].pop("value", None)
    return rfc8785(e)


def banner(s):
    print(f"\n{'=' * 78}\n  {s}\n{'=' * 78}")


# Deterministic compute: SHA-256 of a specific input string.
# The result format is structured: input + algorithm + output + computation steps.
# Every claim is self-evidently faithful because the computation IS the source.

request = {
    "tool_name": "compute_sha256",
    "tool_use_id": "tu_poc_compute_001",
    "input": {
        "data": "The quick brown fox jumps over the lazy dog",
        "encoding": "utf-8",
        "output_format": "hex",
    },
    "model_id": "claude-sonnet-4-6",
    "prompt_version": "v1.0.0",
}

result = {
    "tool_use_id": "tu_poc_compute_001",
    "content": [
        {
            "type": "text",
            "text": (
                "## Computation result\n\n"
                "**Algorithm:** SHA-256 (FIPS 180-4)\n"
                "**Input encoding:** UTF-8\n"
                "**Input length:** 43 bytes\n"
                "**Output format:** hex (lowercase)\n\n"
                "**Input:**\n"
                "```\n"
                "The quick brown fox jumps over the lazy dog\n"
                "```\n\n"
                "**Output:**\n"
                "```\n"
                "d7a8fbb307d7809469ca9abcb0082e4f8d5651e46d3cdb762d02d0bf37c9e592\n"
                "```\n\n"
                "## Verification\n\n"
                "This hash is the canonical SHA-256 output for the input string as defined in "
                "FIPS 180-4. The result is deterministic and reproducible: any compliant "
                "SHA-256 implementation on the same UTF-8 byte sequence will produce the "
                "identical 64-character hex digest.\n\n"
                "**Status:** OK\n"
                "**Compute time:** <1ms\n"
            ),
        },
    ],
}


def main():
    banner("Mimir POC — deterministic compute tool (SHA-256)")
    print(f"  scoring URL : {SCORING_URL}")
    print(f"  tool        : {request['tool_name']}")

    # ---- Step 1: real Claude scoring ----
    banner("Step 1 — Real Claude Sonnet 4.6 scoring")
    t0 = time.time()
    r = requests.post(f"{SCORING_URL}/v1/score", json={
        "request": request,
        "result": result,
        "metadata": {"session_id": "poc-compute-2026-05-16", "timestamp": "2026-05-16T11:00:00Z"},
    }, timeout=180)
    r.raise_for_status()
    v = r.json()
    elapsed = time.time() - t0
    print(f"  latency  : {elapsed:.1f}s")
    print(f"  verdict  : {v['verdict']}")
    print(f"  sigma    : {v['sigma']:.4f}   (DEPLOY: sigma < 0.45)")
    print(f"  overall  : {v['overall']:.2f}   (DEPLOY: overall >= 9.0)")
    print(f"  per-axis :")
    for a in v["axes"]:
        flag = "[ok]" if a["score"] >= 7 else "[lo]"
        print(f"      {flag}  {a['axis']:30s}  {a['score']:>5.2f}")
    passed = sum(1 for a in v["assertions"] if a["passed"])
    print(f"  asserts  : {passed}/8 passed   (DEPLOY: 8/8)")
    for a in v["assertions"]:
        flag = "PASS" if a["passed"] else "FAIL"
        print(f"      {flag}  {a['assertion']:30s}  {a.get('rationale','')[:80]}")

    # ---- Step 2: sign ----
    banner("Step 2 — Envelope construction + Ed25519 signing")
    if v["verdict"] != "DEPLOY":
        print(f"  Verdict={v['verdict']} — proceeding anyway to exercise signing layer.")
    r = requests.post(f"{ISSUER_URL}/v1/attest", json={
        "request": {"name": request["tool_name"], "arguments": request["input"]},
        "result": result,
        "tool_id": "did:web:example.com:tools:compute-sha256",
        "tool_version": "1.0.0",
    }, timeout=15)
    r.raise_for_status()
    env = r.json()["envelope"]
    print(f"  envelope.tool_call_id  : {env['tool_call_id']}")
    print(f"  envelope.result_digest : {env['result_digest']}")
    print(f"  signature.alg          : {env['signature']['protected_header']['alg']}")

    # ---- Step 3: verify ----
    banner("Step 3 — External Ed25519 verification with PyNaCl")
    jwk = requests.get(f"{ISSUER_URL}/v1/key", timeout=5).json()
    pub = b64url_decode(jwk["x"])
    canonical = canonical_for_verify(env)
    sig = b64url_decode(env["signature"]["value"])
    vk = nacl.signing.VerifyKey(pub)
    try:
        vk.verify(canonical, sig)
    except nacl.exceptions.BadSignatureError as e:
        print(f"  [FAIL] {e}")
        return 1
    print(f"  signature verified : YES")
    print()
    if v["verdict"] == "DEPLOY":
        print(f"  *** DEPLOY VERDICT achieved with real Claude ***")
        return 0
    else:
        print(f"  Verdict={v['verdict']}, pipeline still ran end-to-end.")
        return 2


if __name__ == "__main__":
    sys.exit(main())
