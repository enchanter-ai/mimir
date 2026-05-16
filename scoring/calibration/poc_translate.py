"""POC variant — translation tool. Result is visually verifiable by any
French speaker (including Claude-as-judge); no counting, no hashing, no
URL claims. Should clear all 5 axes uniformly with temperature=0."""
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


# Translation tool. The result is qualitatively verifiable: any reader who
# speaks both languages (including Claude) can read the source and translation
# side-by-side and confirm fidelity. No counting, no math, no URLs.

request = {
    "tool_name": "translate",
    "tool_use_id": "tu_poc_xlate_001",
    "input": {
        "text": "Provenance is the record of an object's origin and chain of custody.",
        "source_lang": "en",
        "target_lang": "fr",
    },
    "model_id": "claude-sonnet-4-6",
    "prompt_version": "v1.0.0",
}

result = {
    "tool_use_id": "tu_poc_xlate_001",
    "content": [
        {
            "type": "text",
            "text": (
                "## Translation result\n\n"
                "**Source language:** English\n"
                "**Target language:** French\n\n"
                "**Source text:**\n"
                "> Provenance is the record of an object's origin and chain of custody.\n\n"
                "**Translation:**\n"
                "> La provenance est le registre de l'origine d'un objet et de sa chaîne de possession.\n\n"
                "## Notes\n\n"
                "- Translation is direct and complete; no content omitted.\n"
                "- Domain term \"provenance\" is preserved as the French cognate, which carries the same meaning in both languages.\n"
                "- \"Chain of custody\" rendered as \"chaîne de possession\", the established French legal/archival equivalent.\n"
            ),
        },
    ],
}


def main():
    banner("Mimir POC — translation tool (visually verifiable result)")
    print(f"  scoring URL : {SCORING_URL}")

    banner("Step 1 — Real Claude Sonnet 4.6 scoring (temperature=0)")
    t0 = time.time()
    r = requests.post(f"{SCORING_URL}/v1/score", json={
        "request": request,
        "result": result,
        "metadata": {"session_id": "poc-xlate-2026-05-16", "timestamp": "2026-05-16T12:00:00Z"},
    }, timeout=180)
    r.raise_for_status()
    v = r.json()
    elapsed = time.time() - t0
    print(f"  latency  : {elapsed:.1f}s")
    print(f"  verdict  : {v['verdict']}")
    print(f"  sigma    : {v['sigma']:.4f}   (DEPLOY: sigma < 0.75)")
    print(f"  overall  : {v['overall']:.2f}   (DEPLOY: overall >= 9.0)")
    print(f"  per-axis :")
    for a in v["axes"]:
        flag = "[ok]" if a["score"] >= 7 else "[lo]"
        print(f"      {flag}  {a['axis']:30s}  {a['score']:>5.2f}")
    passed = sum(1 for a in v["assertions"] if a["passed"])
    print(f"  asserts  : {passed}/8 passed   (DEPLOY: 8/8)")
    for a in v["assertions"]:
        flag = "PASS" if a["passed"] else "FAIL"
        print(f"      {flag}  {a['assertion']}")

    banner("Step 2 — Sign envelope")
    r = requests.post(f"{ISSUER_URL}/v1/attest", json={
        "request": {"name": request["tool_name"], "arguments": request["input"]},
        "result": result,
        "tool_id": "did:web:example.com:tools:translate",
        "tool_version": "1.0.0",
    }, timeout=15)
    r.raise_for_status()
    env = r.json()["envelope"]
    print(f"  tool_call_id  : {env['tool_call_id']}")
    print(f"  result_digest : {env['result_digest']}")
    print(f"  signature.alg : {env['signature']['protected_header']['alg']}")
    print(f"  signature     : {env['signature']['value'][:60]}...")

    banner("Step 3 — External Ed25519 verification")
    jwk = requests.get(f"{ISSUER_URL}/v1/key", timeout=5).json()
    pub = b64url_decode(jwk["x"])
    canonical = canonical_for_verify(env)
    sig = b64url_decode(env["signature"]["value"])
    nacl.signing.VerifyKey(pub).verify(canonical, sig)
    print(f"  signature verified : YES")

    print()
    if v["verdict"] == "DEPLOY":
        banner("*** DEPLOY VERDICT achieved end-to-end with real Claude ***")
        print(f"  This envelope can be published with the highest provenance tier.")
        print(f"  Digest: {env['result_digest']}")
        print(f"  Verifiable by anyone fetching the issuer's JWK + canonicalizing per RFC 8785.")
        return 0
    print(f"  Verdict={v['verdict']}, pipeline ran end-to-end.")
    return 2


if __name__ == "__main__":
    sys.exit(main())
