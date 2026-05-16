"""Mimir MCP interop test — official MCP client → MCP server → issuer.

The flow:
  1. Spawn mcp_server.py over stdio using the official MCP SDK's stdio_client.
  2. Initialize an MCP session (handshake, capability negotiation — all real SDK).
  3. List tools (must return fetch_document).
  4. Call the tool with arguments.
  5. Parse the returned content: expect {document, envelope}.
  6. Fetch the issuer's /v1/key, then externally verify the envelope's Ed25519
     signature with PyNaCl.

If this passes, then a REAL MCP-SDK-emitted JSON-RPC envelope was successfully
validated by our issuer's schema layer, signed, and round-tripped back through
the SDK's tool-response channel to a third-party verifier. That closes the
"MCP wire-format conformance" gap — we now have proof through the official SDK,
not just our handcrafted curl.

Usage:
    # The issuer must already be running on http://localhost:8090
    python mcp_client.py
"""
from __future__ import annotations

import asyncio
import base64
import json
import os
import sys
from pathlib import Path

import nacl.exceptions
import nacl.signing
import requests
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client


ISSUER_URL = os.environ.get("MIMIR_ISSUER_URL", "http://localhost:8090")
SERVER_SCRIPT = Path(__file__).parent / "mcp_server.py"


def b64url_decode(s: str) -> bytes:
    pad = (4 - len(s) % 4) % 4
    return base64.urlsafe_b64decode(s + ("=" * pad))


def rfc8785_canonicalize(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def canonical_form_for_verification(envelope: dict) -> bytes:
    e = json.loads(json.dumps(envelope))  # deep copy
    if "signature" in e and isinstance(e["signature"], dict):
        e["signature"].pop("value", None)
    return rfc8785_canonicalize(e)


async def run() -> int:
    print("=" * 72)
    print("  Mimir interop test — official MCP SDK end-to-end")
    print("=" * 72)
    print(f"  Issuer URL : {ISSUER_URL}")
    print(f"  Server     : {SERVER_SCRIPT}")
    print()

    # Sanity: issuer must be live.
    try:
        h = requests.get(f"{ISSUER_URL}/v1/healthz", timeout=2)
        h.raise_for_status()
    except requests.RequestException as e:
        print(f"  [FAIL] issuer not reachable at {ISSUER_URL}: {e}")
        print(f"  hint: cd issuer && ISSUER_PORT=8090 go run .")
        return 1

    server_params = StdioServerParameters(
        command=sys.executable,
        args=[str(SERVER_SCRIPT)],
        env={**os.environ, "MIMIR_ISSUER_URL": ISSUER_URL, "PYTHONUNBUFFERED": "1"},
    )

    async with stdio_client(server_params) as (read, write):
        async with ClientSession(read, write) as session:
            print("  [step 1] initialize MCP session via official SDK")
            init = await session.initialize()
            print(f"           server: {init.serverInfo.name} v{init.serverInfo.version}")

            print("  [step 2] list tools")
            tools = await session.list_tools()
            tool_names = [t.name for t in tools.tools]
            print(f"           tools: {tool_names}")
            if "fetch_document" not in tool_names:
                print("           [FAIL] fetch_document not exposed")
                return 1

            print("  [step 3] call fetch_document via JSON-RPC tools/call")
            result = await session.call_tool(
                "fetch_document",
                {"url": "https://example.com/article/42", "format": "text"},
            )
            if result.isError:
                print(f"           [FAIL] tool reported error: {result.content}")
                return 1

            # Parse the text content — the server returned JSON-encoded {document, envelope}.
            payload_text = result.content[0].text
            payload = json.loads(payload_text)
            if "error" in payload:
                print(f"           [FAIL] server returned error: {payload['error']}")
                return 1
            envelope = payload["envelope"]
            print(f"           envelope.tool_call_id : {envelope['tool_call_id']}")
            print(f"           envelope.request_digest: {envelope['request_digest'][:32]}...")
            print(f"           envelope.signature alg : {envelope['signature']['protected_header']['alg']}")

    # ---- step 4: external verification ----
    print()
    print("  [step 4] verify the envelope using the issuer's published JWK")
    jwk_resp = requests.get(f"{ISSUER_URL}/v1/key", timeout=5).json()
    pub_bytes = b64url_decode(jwk_resp["x"])
    print(f"           jwk.kid       : {jwk_resp['kid']}")
    print(f"           pub key hex   : {pub_bytes[:8].hex()}...{pub_bytes[-8:].hex()}")

    canonical = canonical_form_for_verification(envelope)
    sig_bytes = b64url_decode(envelope["signature"]["value"])
    print(f"           canonical len : {len(canonical)} bytes")
    print(f"           signature len : {len(sig_bytes)} bytes")

    vk = nacl.signing.VerifyKey(pub_bytes)
    try:
        vk.verify(canonical, sig_bytes)
        print()
        print("  [OK] ENVELOPE VERIFIED -- official MCP SDK round-trip succeeded")
        print("       Real JSON-RPC tools/call -> issuer schema validation ->")
        print("       envelope signing -> external Ed25519 verification all pass.")
        return 0
    except nacl.exceptions.BadSignatureError as e:
        print(f"\n  [FAIL] signature does not verify: {e}")
        return 1


if __name__ == "__main__":
    sys.exit(asyncio.run(run()))
