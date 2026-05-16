"""Run the 50 labeled cases through the live scoring service, save raw results.

Scoring runs concurrently in small batches (default 3 at a time) to keep
Anthropic API load reasonable. Each case contributes one HTTP POST to the
service; the service itself fans out 5 parallel axis Claude calls + 1
assertions call internally.

Output: scoring/calibration/calibration-results.json
Cost:   ~50 × $0.05 ≈ $2.50 in real Claude credits.
"""
from __future__ import annotations

import asyncio
import json
import os
import sys
import time
from dataclasses import asdict
from pathlib import Path

try:
    import aiohttp
except ImportError:
    import subprocess
    subprocess.run([sys.executable, "-m", "pip", "install", "aiohttp"], check=True)
    import aiohttp

from cases import ALL_CASES, Case

SCORING_URL = os.environ.get("SCORING_URL", "http://localhost:9090")
CONCURRENCY = int(os.environ.get("CALIB_CONCURRENCY", "3"))
OUT_PATH = Path(__file__).parent / "calibration-results.json"


async def score_one(session: aiohttp.ClientSession, case: Case, idx: int, total: int) -> dict:
    payload = {
        "request": case.request,
        "result": case.result,
        "metadata": {"session_id": "calibration-50", "timestamp": "2026-05-16T12:00:00Z"},
    }
    t0 = time.time()
    try:
        async with session.post(f"{SCORING_URL}/v1/score", json=payload, timeout=aiohttp.ClientTimeout(total=240)) as r:
            if r.status != 200:
                txt = await r.text()
                print(f"  [{idx:02d}/{total}] {case.expected:4s} {case.label[:50]:50s}  HTTP {r.status}")
                return {
                    "idx": idx, "label": case.label, "expected": case.expected,
                    "failure_mode": case.failure_mode, "tool_name": case.request["tool_name"],
                    "error": f"HTTP {r.status}: {txt[:120]}",
                }
            v = await r.json()
    except Exception as e:
        print(f"  [{idx:02d}/{total}] {case.expected:4s} {case.label[:50]:50s}  ERR {e}")
        return {
            "idx": idx, "label": case.label, "expected": case.expected,
            "failure_mode": case.failure_mode, "tool_name": case.request["tool_name"],
            "error": str(e),
        }
    elapsed = time.time() - t0
    asserts_passed = sum(1 for a in v.get("assertions", []) if a["passed"])
    print(
        f"  [{idx:02d}/{total}] {case.expected:4s} {case.label[:50]:50s}  "
        f"v={v['verdict']:6s} sigma={v['sigma']:.2f} ov={v['overall']:.1f} "
        f"ax=[{','.join(str(int(a['score'])) for a in v['axes'])}] as={asserts_passed}/8  ({elapsed:.0f}s)"
    )
    return {
        "idx": idx, "label": case.label, "expected": case.expected,
        "failure_mode": case.failure_mode, "tool_name": case.request["tool_name"],
        "latency_s": elapsed,
        "verdict": v["verdict"],
        "sigma": v["sigma"],
        "overall": v["overall"],
        "axes": v["axes"],
        "assertions": v["assertions"],
    }


async def main() -> int:
    print(f"=== Mimir calibration run — {len(ALL_CASES)} cases ===")
    print(f"  scoring URL : {SCORING_URL}")
    print(f"  concurrency : {CONCURRENCY}")
    print(f"  output      : {OUT_PATH}")
    print()

    async with aiohttp.ClientSession() as session:
        async with session.get(f"{SCORING_URL}/v1/healthz") as h:
            if h.status != 200:
                print(f"[FAIL] scoring service not healthy")
                return 1

        results: list[dict] = []
        sem = asyncio.Semaphore(CONCURRENCY)

        async def _bounded(c: Case, i: int):
            async with sem:
                r = await score_one(session, c, i, len(ALL_CASES))
                results.append(r)

        t0 = time.time()
        await asyncio.gather(*[_bounded(c, i) for i, c in enumerate(ALL_CASES, 1)])
        elapsed_total = time.time() - t0

    results.sort(key=lambda r: r["idx"])

    OUT_PATH.write_text(json.dumps({
        "total_cases": len(ALL_CASES),
        "good_cases": sum(1 for r in results if r["expected"] == "good"),
        "bad_cases":  sum(1 for r in results if r["expected"] == "bad"),
        "errored":    sum(1 for r in results if "error" in r),
        "elapsed_total_s": elapsed_total,
        "results": results,
    }, indent=2), encoding="utf-8")

    print()
    print(f"=== complete ===  wall time {elapsed_total:.0f}s")
    print(f"  written: {OUT_PATH}")
    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
