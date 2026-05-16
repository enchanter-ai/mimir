"""Calibration probe — does the scoring rubric discriminate between known-good
and known-bad tool-call results? Run against the live scoring service.

This isn't a full calibration set — it's a smoke test that the real Claude
Sonnet 4.6 scoring path returns different sigma / verdict for results that
SHOULD score differently. If known-good gives DEPLOY and known-bad gives
HOLD/FAIL, the rubric is at least directionally correct.

Usage:
    SCORING_URL=http://localhost:9090 python probe.py
"""
from __future__ import annotations

import json
import os
import sys
import time
from typing import Any

try:
    import requests
except ImportError:
    import subprocess
    subprocess.run([sys.executable, "-m", "pip", "install", "requests"], check=True)
    import requests

SCORING_URL = os.environ.get("SCORING_URL", "http://localhost:9090")


# -------------------------------------------------------------------------
# Sample tool-call results — deliberately constructed at three quality tiers
# -------------------------------------------------------------------------

GOOD_CASE = {
    "request": {
        "tool_name": "summarize_paper",
        "tool_use_id": "tu_calib_good",
        "input": {
            "url": "https://example.com/papers/transformer-arch",
            "max_words": 200,
        },
        "model_id": "claude-sonnet-4-6",
        "prompt_version": "v1.0",
    },
    "result": {
        "tool_use_id": "tu_calib_good",
        "content": [
            {
                "type": "text",
                "text": (
                    "The paper introduces the Transformer architecture, replacing recurrent "
                    "and convolutional layers with self-attention. Section 3 defines scaled "
                    "dot-product attention: Attention(Q,K,V) = softmax(QK^T / sqrt(d_k)) V. "
                    "Multi-head attention runs h parallel attention layers with different "
                    "learned projections (Section 3.2.2). Positional encodings use sinusoids "
                    "of different frequencies (Section 3.5). On WMT 2014 English-to-German "
                    "the model achieves BLEU 28.4, a 2.0 BLEU improvement over the prior "
                    "state-of-the-art ensemble. Training cost: 3.5 days on 8 P100 GPUs. "
                    "All numbers are reported in Table 2 of the paper."
                ),
            },
        ],
    },
    "metadata": {"session_id": "calib-good", "timestamp": "2026-05-15T22:00:00Z"},
    "expected_verdict": "DEPLOY",
    "label": "good — concrete, cited, specific numbers",
}

MEDIUM_CASE = {
    "request": {
        "tool_name": "summarize_paper",
        "tool_use_id": "tu_calib_medium",
        "input": {
            "url": "https://example.com/papers/transformer-arch",
            "max_words": 200,
        },
        "model_id": "claude-sonnet-4-6",
        "prompt_version": "v1.0",
    },
    "result": {
        "tool_use_id": "tu_calib_medium",
        "content": [
            {
                "type": "text",
                "text": (
                    "The paper proposes a new neural architecture based on attention. "
                    "It performs well on translation tasks and is more parallelizable "
                    "than recurrent models. The authors report improvements over previous "
                    "methods on standard benchmarks."
                ),
            },
        ],
    },
    "metadata": {"session_id": "calib-medium", "timestamp": "2026-05-15T22:00:01Z"},
    "expected_verdict": "HOLD",
    "label": "medium — vague but not wrong",
}

BAD_CASE = {
    "request": {
        "tool_name": "summarize_paper",
        "tool_use_id": "tu_calib_bad",
        "input": {
            "url": "https://example.com/papers/transformer-arch",
            "max_words": 200,
        },
        "model_id": "claude-sonnet-4-6",
        "prompt_version": "v1.0",
    },
    "result": {
        "tool_use_id": "tu_calib_bad",
        "content": [
            {
                "type": "text",
                "text": (
                    "Sure! Here is a great summary of the paper for you. It is super "
                    "interesting and you will love it. Just trust me, the architecture "
                    "is amazing. Let me know if you want anything else! :)"
                ),
            },
        ],
    },
    "metadata": {"session_id": "calib-bad", "timestamp": "2026-05-15T22:00:02Z"},
    "expected_verdict": "HOLD",  # or FAIL — we expect overall < 7
    "label": "bad — sycophantic, content-free",
}


def banner(s: str) -> None:
    print(f"\n{'=' * 78}\n  {s}\n{'=' * 78}")


def score_case(case: dict[str, Any]) -> dict[str, Any]:
    body = {
        "request": case["request"],
        "result": case["result"],
        "metadata": case["metadata"],
    }
    t0 = time.time()
    r = requests.post(f"{SCORING_URL}/v1/score", json=body, timeout=120)
    elapsed = time.time() - t0
    r.raise_for_status()
    v = r.json()

    print(f"  case            : {case['label']}")
    print(f"  expected verdict: {case['expected_verdict']}")
    print(f"  ACTUAL verdict  : {v.get('verdict')}")
    print(f"  sigma           : {v.get('sigma'):.4f}")
    print(f"  overall         : {v.get('overall'):.2f}")
    print(f"  axes:")
    for a in v.get("axes", []):
        print(f"      {a['axis']:30s}  {a['score']:.2f}   {a['rationale'][:80]}")
    passed = [a for a in v.get("assertions", []) if a.get("passed")]
    print(f"  assertions      : {len(passed)}/8 passed")
    print(f"  latency         : {elapsed:.1f}s")
    print(f"  model           : {v.get('model_used')}")
    return v


def main() -> int:
    banner("Mimir scoring calibration probe — real Claude Sonnet 4.6")
    print(f"  scoring URL: {SCORING_URL}")

    try:
        h = requests.get(f"{SCORING_URL}/v1/healthz", timeout=5).json()
        print(f"  health     : {h}")
    except requests.RequestException as e:
        print(f"  [FAIL] scoring service not reachable: {e}")
        return 1

    results = []
    for case in (GOOD_CASE, MEDIUM_CASE, BAD_CASE):
        banner(f"Scoring: {case['label']}")
        v = score_case(case)
        results.append((case, v))

    banner("Calibration check — does the rubric discriminate?")
    good_v, medium_v, bad_v = results[0][1], results[1][1], results[2][1]
    good_overall = good_v["overall"]
    medium_overall = medium_v["overall"]
    bad_overall = bad_v["overall"]

    print(f"  good overall   : {good_overall:.2f}")
    print(f"  medium overall : {medium_overall:.2f}")
    print(f"  bad overall    : {bad_overall:.2f}")

    monotone = good_overall > medium_overall > bad_overall
    print(f"  monotone (good > medium > bad)? {monotone}")

    good_verdict = good_v["verdict"]
    bad_verdict = bad_v["verdict"]
    discriminates = good_verdict in ("DEPLOY", "PASS") and bad_verdict in ("HOLD", "FAIL")
    print(f"  verdicts discriminate?         {discriminates}  ({good_verdict} vs {bad_verdict})")

    # Save the probe transcript for inspection.
    out_path = os.path.join(os.path.dirname(__file__), "probe-transcript.json")
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(
            {
                "scoring_url": SCORING_URL,
                "results": [
                    {"label": c["label"], "expected": c["expected_verdict"], "verdict": v}
                    for c, v in results
                ],
                "monotone": monotone,
                "discriminates": discriminates,
            },
            f,
            indent=2,
        )
    print(f"\n  Transcript written to: {out_path}")

    return 0 if (monotone and discriminates) else 1


if __name__ == "__main__":
    sys.exit(main())
