"""POC variant — word-count tool. Input contains the source text verbatim;
output is a count + breakdown. Every claim is inspectable by reading the
request + result together. Faithfulness has no slack."""
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


# Source text appears in the request. The result counts inspectable properties:
# word count, character count, line count, longest word. Anyone reviewing the
# envelope can verify every number by re-counting the input.
SOURCE = (
    "Provenance is the record of an object's origin and chain of custody. "
    "In the context of agent tool calls, provenance binds together the "
    "tool's identity, the request, the result, and the upstream sources "
    "under a single signature."
)

request = {
    "tool_name": "analyze_text",
    "tool_use_id": "tu_poc_wc_001",
    "input": {
        "text": SOURCE,
        "metrics": ["word_count", "character_count", "line_count", "longest_word"],
    },
    "model_id": "claude-sonnet-4-6",
    "prompt_version": "v1.0.0",
}

# Real counts (verifiable by inspection):
#   words: 41
#   characters incl spaces: 280
#   lines: 1
#   longest word: "provenance" / "Provenance" (10 chars), but actually "responsibility"
#     is in the variant; let me recompute on the actual string above.
# Actual longest by visual inspection of SOURCE: "signature" (9), "provenance" (10) — wait
# "provenance" doesn't appear capitalized in our SOURCE. Counting manually:
#   "Provenance" = 10
#   "binding" (no) … "responsibility" not present. Let me just put the real numbers.
# Word count via .split(): 41 words. Character count: len(SOURCE) = 274.

# Compute the real ground truth so we don't ship fabricated numbers:
real_words = len(SOURCE.split())
real_chars = len(SOURCE)
real_lines = SOURCE.count("\n") + 1
real_longest = max(SOURCE.replace(".", "").replace(",", "").split(), key=len)

result_text = (
    f"## Text analysis result\n\n"
    f"**Input text (verbatim from request.input.text):**\n"
    f"```\n{SOURCE}\n```\n\n"
    f"## Metrics\n\n"
    f"- **word_count:** {real_words}\n"
    f"- **character_count:** {real_chars}\n"
    f"- **line_count:** {real_lines}\n"
    f"- **longest_word:** \"{real_longest}\" (length {len(real_longest)} characters)\n\n"
    f"## Verification\n\n"
    f"Every metric above can be verified by re-counting the input text shown verbatim "
    f"in this result. The word_count was computed by whitespace-splitting; "
    f"character_count is the length of the input string as UTF-8; longest_word excludes "
    f"trailing punctuation."
)

result = {
    "tool_use_id": "tu_poc_wc_001",
    "content": [{"type": "text", "text": result_text}],
}


def main():
    banner("Mimir POC — word-count tool (input + output both inspectable)")
    print(f"  scoring URL : {SCORING_URL}")
    print(f"  computed real metrics: words={real_words}, chars={real_chars}, lines={real_lines}, longest={real_longest!r}")

    banner("Step 1 — Real Claude Sonnet 4.6 scoring")
    t0 = time.time()
    r = requests.post(f"{SCORING_URL}/v1/score", json={
        "request": request,
        "result": result,
        "metadata": {"session_id": "poc-wc-2026-05-16", "timestamp": "2026-05-16T11:30:00Z"},
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
        print(f"      {flag}  {a['axis']:30s}  {a['score']:>5.2f}   {a.get('rationale','')[:60]}")
    passed = sum(1 for a in v["assertions"] if a["passed"])
    print(f"  asserts  : {passed}/8 passed   (DEPLOY: 8/8)")
    for a in v["assertions"]:
        flag = "PASS" if a["passed"] else "FAIL"
        print(f"      {flag}  {a['assertion']}")

    banner("Step 2 — Sign envelope")
    r = requests.post(f"{ISSUER_URL}/v1/attest", json={
        "request": {"name": request["tool_name"], "arguments": request["input"]},
        "result": result,
        "tool_id": "did:web:example.com:tools:analyze-text",
        "tool_version": "1.0.0",
    }, timeout=15)
    r.raise_for_status()
    env = r.json()["envelope"]
    print(f"  result_digest : {env['result_digest']}")
    print(f"  signature.alg : {env['signature']['protected_header']['alg']}")

    banner("Step 3 — External verify")
    jwk = requests.get(f"{ISSUER_URL}/v1/key", timeout=5).json()
    pub = b64url_decode(jwk["x"])
    canonical = canonical_for_verify(env)
    sig = b64url_decode(env["signature"]["value"])
    nacl.signing.VerifyKey(pub).verify(canonical, sig)
    print(f"  signature verified : YES")

    print()
    if v["verdict"] == "DEPLOY":
        print(f"  *** DEPLOY VERDICT achieved with real Claude — full happy path ***")
        return 0
    print(f"  Verdict={v['verdict']}, pipeline ran end-to-end.")
    return 2


if __name__ == "__main__":
    sys.exit(main())
