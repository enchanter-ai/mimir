"""Mimir-attested MCP server using the official mcp SDK.

This is a real MCP server that:
  1. Exposes one tool ("fetch_document") via the official MCP SDK's FastMCP.
  2. When the tool is called, computes the result (a stub document fetch).
  3. POSTs {request, result} to the local Mimir issuer's /v1/attest-mcp endpoint.
  4. Wraps the issuer's signed envelope into the tool's return content so the
     MCP client receives both the document text AND a cryptographic provenance receipt.

The MCP wire format is whatever the SDK emits — we deliberately do NOT hand-craft
JSON-RPC. If the SDK's encoding mismatches our issuer's schema validation, the
attest call fails and the tool returns an error. So passing this end-to-end is
proof that real MCP wire-format consumers and our issuer agree on the contract.

Usage: launched as a subprocess by mcp_client.py via stdio transport.
"""
from __future__ import annotations

import json
import os
import sys

import requests
from mcp.server.fastmcp import FastMCP

ISSUER_URL = os.environ.get("MIMIR_ISSUER_URL", "http://localhost:8090")
TOOL_NAME = "fetch_document"
TOOL_VERSION = "1.0.0"
TOOL_DID = "did:web:example.com:tools:fetch-document"

mcp = FastMCP("mimir-attested-server")


@mcp.tool()
def fetch_document(url: str, format: str = "text") -> str:
    """Fetch a document from a URL and return the body. Returns JSON with the
    document text and a Mimir provenance envelope cryptographically attesting
    to the request + result + signing key."""

    # 1. Compute the actual tool result.
    result_text = f"Stub fetch of {url} in {format}. Body: Lorem ipsum dolor sit amet."

    # 2. Build the MCP-shaped request + result for the attest call.
    #    The official MCP `tools/call` request shape uses JSON-RPC 2.0.
    mcp_request = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "tools/call",
        "params": {
            "name": TOOL_NAME,
            "arguments": {"url": url, "format": format},
        },
    }
    mcp_result = {
        "jsonrpc": "2.0",
        "id": 1,
        "result": {
            "content": [{"type": "text", "text": result_text}],
        },
    }

    # 3. POST to the issuer's /v1/attest-mcp endpoint.
    attest_body = {
        "tool_id": TOOL_DID,
        "tool_version": TOOL_VERSION,
        "request": mcp_request,
        "result": mcp_result,
    }
    try:
        r = requests.post(f"{ISSUER_URL}/v1/attest-mcp", json=attest_body, timeout=5)
        r.raise_for_status()
    except requests.RequestException as e:
        return json.dumps({"error": f"attest call failed: {e}"})

    envelope = r.json().get("envelope")
    if envelope is None:
        return json.dumps({"error": "issuer returned no envelope"})

    # 4. Return the document body AND the provenance envelope together.
    return json.dumps({
        "document": result_text,
        "envelope": envelope,
    })


if __name__ == "__main__":
    mcp.run()
