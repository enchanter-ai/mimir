"""Starter MCP server with Mimir attestation — fork this file.

This is the minimum a third-party integrator needs to publish a Mimir-attested
MCP tool. Replace the `expensive_lookup` tool body with your real tool logic;
keep the attestation wrapper.

What you get for free:
  - Tool invocations get a cryptographic provenance receipt
  - The receipt is in the tool's response content as JSON, alongside your result
  - Any MCP client (Claude Desktop / Cursor / Cline / API agents) sees the
    receipt and can verify it independently against the issuer's public key

Run standalone:
  pip install mcp requests
  ISSUER_URL=http://localhost:8080 python server.py

Register with Claude Desktop:
  Edit ~/Library/Application Support/Claude/claude_desktop_config.json
  (macOS) or %APPDATA%\\Claude\\claude_desktop_config.json (Windows):

  {
    "mcpServers": {
      "my-attested-tool": {
        "command": "python",
        "args": ["/absolute/path/to/server.py"],
        "env": {
          "ISSUER_URL": "http://localhost:8080",
          "TOOL_DID": "did:web:mycompany.com:tools:expensive-lookup",
          "TOOL_VERSION": "1.0.0"
        }
      }
    }
  }
"""
from __future__ import annotations

import json
import os
import sys

import requests
from mcp.server.fastmcp import FastMCP


# ─── Config (read from env so deployment doesn't need code changes) ────

ISSUER_URL   = os.environ.get("ISSUER_URL", "http://localhost:8080")
TOOL_DID     = os.environ.get("TOOL_DID", "did:web:example.com:tools:expensive-lookup")
TOOL_VERSION = os.environ.get("TOOL_VERSION", "1.0.0")
SERVER_NAME  = os.environ.get("SERVER_NAME", "mimir-attested-starter")


mcp = FastMCP(SERVER_NAME)


def attest(tool_name: str, arguments: dict, result_content: str) -> dict | None:
    """POST {request, result} to the Mimir issuer; return the signed envelope.

    Returns None on attestation failure — the caller decides whether to surface
    the result without the envelope, or fail the tool call. The starter pattern
    here SURFACES the result either way (degrade gracefully when the issuer is
    momentarily unavailable), but flags the absence so the client knows."""
    mcp_request = {
        "jsonrpc": "2.0", "id": 1, "method": "tools/call",
        "params": {"name": tool_name, "arguments": arguments},
    }
    mcp_result = {
        "jsonrpc": "2.0", "id": 1,
        "result": {"content": [{"type": "text", "text": result_content}]},
    }
    body = {
        "tool_id":      TOOL_DID,
        "tool_version": TOOL_VERSION,
        "request":      mcp_request,
        "result":       mcp_result,
    }
    try:
        r = requests.post(f"{ISSUER_URL}/v1/attest-mcp", json=body, timeout=10)
        r.raise_for_status()
        return r.json().get("envelope")
    except requests.RequestException as e:
        # Production deploys should structured-log this; for the starter
        # we just print to stderr so the operator notices.
        print(f"WARN: attestation failed: {e}", file=sys.stderr)
        return None


# ─── Your tool goes here. ──────────────────────────────────────────────
#
# Replace the stub with whatever your tool actually does. Keep the call to
# attest() so every result ships with a Mimir envelope.

@mcp.tool()
def expensive_lookup(query: str, max_results: int = 5) -> str:
    """A toy tool — replace with your real implementation.

    Returns a JSON-encoded object: {result: <text>, envelope?: <Mimir envelope>}.
    Clients that don't know about envelopes get the result; clients that do
    can verify the envelope and act on it.
    """
    # 1. Run the real tool work.
    result_text = f"Expensive lookup result for query={query!r}, max_results={max_results}: 42, 43, 44."

    # 2. Attest the (request, result) pair via the Mimir issuer.
    envelope = attest("expensive_lookup",
                      {"query": query, "max_results": max_results},
                      result_text)

    # 3. Return both to the MCP client.
    if envelope is None:
        return json.dumps({"result": result_text, "attestation_error": "issuer unreachable"})
    return json.dumps({"result": result_text, "envelope": envelope})


if __name__ == "__main__":
    mcp.run()
