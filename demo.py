"""End-to-end demo: tools/call result -> P2 scoring -> P1 envelope signing -> external verify.

Pipeline:
  1. Start P2 (TS scoring engine) on :9090 with MOCK_MODE=1
  2. Start P1 (Go issuer) on :8080
  3. Poll /v1/healthz on both until ready
  4. Send a sample tool-call to P2 /v1/score -> get ScoringVerdict
  5. If verdict.verdict == DEPLOY, send to P1 /v1/attest -> get signed envelope
  6. Fetch P1 /v1/key for the Ed25519 public key (JWK)
  7. Verify the envelope signature externally using PyNaCl (Ed25519)
  8. Print results, clean up subprocesses

Requirements (auto-installed via `pip install pynacl requests`):
  - pynacl (Ed25519 verification)
  - requests
"""
from __future__ import annotations

import base64
import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

try:
    import nacl.signing
    import nacl.exceptions
    import requests
except ImportError:
    print("Installing dependencies: pynacl + requests...")
    subprocess.run([sys.executable, "-m", "pip", "install", "pynacl", "requests"], check=True, capture_output=True)
    import nacl.signing
    import nacl.exceptions
    import requests


ROOT = Path(__file__).resolve().parent
P1_DIR = ROOT / "issuer"
P2_DIR = ROOT / "scoring"
DEMO_OUT = ROOT / "demo-transcript.md"

P1_PORT = 8080
P2_PORT = 9090
TIMEOUT_S = 30


def wait_for(url: str, timeout: int = TIMEOUT_S) -> bool:
    """Poll url until 200 or timeout."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            r = requests.get(url, timeout=2)
            if r.status_code == 200:
                return True
        except requests.RequestException:
            pass
        time.sleep(0.5)
    return False


def banner(s: str) -> None:
    print(f"\n{'=' * 78}\n  {s}\n{'=' * 78}")


def b64url_decode(s: str) -> bytes:
    """Decode base64url without padding."""
    pad = (4 - len(s) % 4) % 4
    return base64.urlsafe_b64decode(s + ("=" * pad))


def rfc8785_canonicalize(obj: Any) -> bytes:
    """Minimal RFC 8785 JCS — sorted keys, no whitespace, UTF-8."""
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def canonical_form_for_verification(envelope: dict) -> bytes:
    """Strip signature.value from envelope, then canonicalize."""
    e = json.loads(json.dumps(envelope))  # deep copy
    if "signature" in e and isinstance(e["signature"], dict):
        e["signature"].pop("value", None)
    return rfc8785_canonicalize(e)


def run_demo() -> dict:
    transcript: dict[str, Any] = {"steps": []}

    # Sample tools/call payload that flows through the whole pipeline
    sample_request = {
        "tool_name": "fetch_document",
        "tool_use_id": "tu_demo_e2e_001",
        "input": {"url": "https://example.com/article/42", "format": "text"},
        "model_id": "claude-sonnet-4-6",
        "prompt_version": "v1.2.0",
    }
    sample_result = {
        "tool_use_id": "tu_demo_e2e_001",
        "content": [
            {"type": "text", "text": "Article title: A Brief History of Provenance.\n\nProvenance — the record of an object's origin and chain of custody — has roots in art-world authentication."}
        ],
    }

    # ============================================
    # STEP 1: Call P2 /v1/score
    # ============================================
    banner("STEP 1 — Scoring (P2 TypeScript service, MOCK_MODE=1)")
    score_req = {
        "request": sample_request,
        "result": sample_result,
        "metadata": {"session_id": "sess_demo_e2e", "timestamp": "2026-05-13T18:00:00Z"},
    }
    r = requests.post(f"http://localhost:{P2_PORT}/v1/score", json=score_req, timeout=10)
    r.raise_for_status()
    verdict = r.json()
    print(f"  HTTP {r.status_code} from /v1/score")
    print(f"  verdict: {verdict.get('verdict')}")
    print(f"  sigma:   {verdict.get('sigma'):.4f}")
    print(f"  overall: {verdict.get('overall'):.2f}")
    print(f"  axes:    {[a['axis'] + '=' + str(a['score']) for a in verdict.get('axes', [])]}")
    print(f"  8/8 assertions passed: {all(a['passed'] for a in verdict.get('assertions', []))}")
    transcript["steps"].append({"step": "score", "verdict": verdict})

    if verdict.get("verdict") != "DEPLOY":
        print(f"  -> verdict is {verdict.get('verdict')}, would NOT proceed to signing in production")
        return transcript

    # ============================================
    # STEP 2: Call P1 /v1/attest
    # ============================================
    banner("STEP 2 — Envelope construction + signing (P1 Go service)")
    # P1 expects a flat tools/call shape. Adapt P2's request to P1's expected shape.
    attest_req = {
        "request": {"name": sample_request["tool_name"], "arguments": sample_request["input"]},
        "result": sample_result,
        "tool_id": "did:web:demo.enchanter-labs.io:tools:fetch-document",
        "tool_version": "1.0.0",
    }
    r = requests.post(f"http://localhost:{P1_PORT}/v1/attest", json=attest_req, timeout=10)
    r.raise_for_status()
    envelope = r.json().get("envelope")
    print(f"  HTTP {r.status_code} from /v1/attest")
    print(f"  envelope.version:       {envelope.get('version')}")
    print(f"  envelope.tool_call_id:  {envelope.get('tool_call_id')}")
    print(f"  envelope.request_digest: {envelope.get('request_digest')}")
    print(f"  envelope.result_digest:  {envelope.get('result_digest')}")
    print(f"  envelope.signature.alg:  {envelope.get('signature', {}).get('protected_header', {}).get('alg')}")
    print(f"  envelope.signature.value: {envelope.get('signature', {}).get('value', '')[:60]}...")
    transcript["steps"].append({"step": "attest", "envelope": envelope})

    # ============================================
    # STEP 3: Fetch public key + verify signature
    # ============================================
    banner("STEP 3 — External signature verification using PyNaCl Ed25519")
    r = requests.get(f"http://localhost:{P1_PORT}/v1/key", timeout=5)
    r.raise_for_status()
    jwk = r.json()
    print(f"  key_id: {jwk.get('kid')}")
    pubkey_b64 = jwk.get("x", "")
    pubkey_bytes = b64url_decode(pubkey_b64)
    print(f"  pub key bytes (hex first 16): {pubkey_bytes[:8].hex()}...{pubkey_bytes[-8:].hex()}")

    canonical = canonical_form_for_verification(envelope)
    print(f"  canonical form length: {len(canonical)} bytes")
    print(f"  canonical preview:     {canonical[:120].decode('utf-8')}...")

    sig_bytes = b64url_decode(envelope["signature"]["value"])
    print(f"  signature length:      {len(sig_bytes)} bytes (expect 64 for Ed25519)")

    vk = nacl.signing.VerifyKey(pubkey_bytes)
    try:
        vk.verify(canonical, sig_bytes)
        print(f"\n  [OK] SIGNATURE VERIFIED -- envelope is cryptographically valid")
        transcript["steps"].append({"step": "verify", "result": "PASS"})
    except nacl.exceptions.BadSignatureError as e:
        print(f"\n  [FAIL] SIGNATURE INVALID -- {e}")
        transcript["steps"].append({"step": "verify", "result": "FAIL", "error": str(e)})

    return transcript


def main() -> int:
    banner("Enchanter Labs Quality Oracle — End-to-End MVP Demo")
    print(f"  P1 issuer:        {P1_DIR}")
    print(f"  P2 scoring:       {P2_DIR}")
    print(f"  Workflow: tools/call -> P2 score (MOCK) -> P1 sign -> external verify")

    # Start P2 (TS scoring) in MOCK_MODE
    banner("Starting P2 (TypeScript scoring engine, MOCK_MODE=1)")
    p2_env = os.environ.copy()
    p2_env["MOCK_MODE"] = "1"
    # ANTHROPIC_API_KEY is not required in MOCK_MODE but the server may still check;
    # set a dummy to satisfy any startup checks.
    p2_env.setdefault("ANTHROPIC_API_KEY", "sk-mock-not-a-real-key")
    p2_proc = subprocess.Popen(
        ["npx", "tsx", "src/server.ts"],
        cwd=str(P2_DIR),
        env=p2_env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        shell=True,
    )
    print(f"  PID {p2_proc.pid} starting...")
    if not wait_for(f"http://localhost:{P2_PORT}/v1/healthz", timeout=30):
        print(f"  P2 failed to become healthy in 30s")
        p2_proc.terminate()
        return 1
    print(f"  P2 healthy on :{P2_PORT}")

    # Start P1 (Go issuer)
    banner("Starting P1 (Go issuer service)")
    p1_proc = subprocess.Popen(
        ["go", "run", "."],
        cwd=str(P1_DIR),
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        shell=True,
    )
    print(f"  PID {p1_proc.pid} starting...")
    if not wait_for(f"http://localhost:{P1_PORT}/v1/healthz", timeout=30):
        print(f"  P1 failed to become healthy in 30s")
        p2_proc.terminate()
        p1_proc.terminate()
        return 1
    print(f"  P1 healthy on :{P1_PORT}")

    # Run the demo pipeline
    transcript = {}
    try:
        transcript = run_demo()
    except Exception as e:
        print(f"\n  ERROR during demo: {e}")
        transcript = {"error": str(e)}
    finally:
        banner("Cleaning up subprocesses")
        for proc, name in [(p1_proc, "P1"), (p2_proc, "P2")]:
            try:
                if os.name == "nt":
                    proc.send_signal(signal.CTRL_BREAK_EVENT)
                else:
                    proc.terminate()
                proc.wait(timeout=5)
                print(f"  {name} (PID {proc.pid}) stopped")
            except (subprocess.TimeoutExpired, ProcessLookupError, OSError):
                proc.kill()
                print(f"  {name} (PID {proc.pid}) killed")

    # Write transcript
    DEMO_OUT.write_text(
        f"# Demo transcript — {time.strftime('%Y-%m-%dT%H:%M:%SZ')}\n\n```json\n{json.dumps(transcript, indent=2)}\n```\n",
        encoding="utf-8",
    )
    print(f"\n  Transcript written to: {DEMO_OUT}")

    return 0 if all(s.get("result", "PASS") == "PASS" for s in transcript.get("steps", []) if s["step"] == "verify") else 1


if __name__ == "__main__":
    sys.exit(main())
