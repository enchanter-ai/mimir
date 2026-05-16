# Integrating Mimir with Claude Desktop

This recipe registers a Mimir-attested MCP server with Claude Desktop so every tool-call result your server returns ships with a cryptographic provenance receipt. The user sees the tool reply as normal; the receipt is in the structured content for downstream verifiers to consume.

Time: ~10 minutes. Requires Claude Desktop, Python 3.11+, the Mimir issuer running locally.

---

## 1. Run the issuer locally

```bash
git clone git@github.com:enchanter-ai/mimir.git
cd mimir/issuer

# Ephemeral key on :8090. Production: KMS_MODE=aws KMS_KEY_ARN=...
ISSUER_PORT=8090 go run .
```

Confirm `curl http://localhost:8090/v1/healthz` returns `{"status":"ok"}`.

---

## 2. Use the example MCP server we ship

The repo includes [`tests/mcp/mcp_server.py`](../tests/mcp/mcp_server.py) — a real MCP server using Anthropic's official Python SDK that exposes a `fetch_document` tool. Every call to that tool POSTs to the issuer's `/v1/attest-mcp` endpoint and embeds the returned envelope in the tool's content.

Confirm it works standalone with the included client:

```bash
python tests/mcp/mcp_client.py
# → [OK] ENVELOPE VERIFIED -- official MCP SDK round-trip succeeded
```

If that passes, you have a working Mimir-attested MCP server. The remaining step is just pointing Claude Desktop at it.

---

## 3. Register with Claude Desktop

Edit Claude Desktop's MCP config file. Path:

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`
- **Linux (if available):** `~/.config/Claude/claude_desktop_config.json`

Add the `mimir-attested` server block. Existing servers in the `mcpServers` object stay; you're adding one alongside:

```json
{
  "mcpServers": {
    "mimir-attested": {
      "command": "python",
      "args": ["/absolute/path/to/mimir/tests/mcp/mcp_server.py"],
      "env": {
        "MIMIR_ISSUER_URL": "http://localhost:8090",
        "PYTHONUNBUFFERED": "1"
      }
    }
  }
}
```

Replace `/absolute/path/to/mimir/` with your actual checkout path. Use forward slashes even on Windows (Claude Desktop's JSON parser handles either).

Save the file. Fully quit Claude Desktop (not just close the window — on macOS use Cmd+Q, on Windows close it from the system tray) and reopen.

---

## 4. Confirm the server registered

In a new Claude Desktop conversation, type:

> What MCP tools do you have access to right now?

Claude should list `fetch_document` from the `mimir-attested` server. If it does not, check the Claude Desktop developer console:

- **macOS:** `~/Library/Logs/Claude/mcp.log`
- **Windows:** `%APPDATA%\Claude\logs\mcp.log`

Common failures:

| Symptom in mcp.log | Fix |
|---|---|
| `connection closed: stdio` | The `python` command isn't in Claude Desktop's PATH. Use the absolute path: `/usr/bin/python3` or `C:\Users\...\python.exe`. |
| `ModuleNotFoundError: mcp` | The Python env Claude Desktop uses doesn't have the MCP SDK installed. `pip install mcp` in the env Claude Desktop is invoking. |
| `Connection refused: 8090` | The Mimir issuer is not running. `cd mimir/issuer && ISSUER_PORT=8090 go run .` |

---

## 5. Use the attested tool

Ask Claude:

> Use the fetch_document tool to fetch https://example.com/article/42 in text format.

The tool returns a JSON object that includes both the document body AND a Mimir provenance envelope. Claude will use the document content as it normally would — and the envelope is now part of the conversation context, available to any future tool / Claude Skill / Anthropic API call that wants to verify the result independently.

Sample envelope fields (from a real run):

```json
{
  "envelope": {
    "version": "mcp-provenance/2026-05-13-ed25519",
    "tool_call_id": "...",
    "tool_id": "did:web:example.com:tools:fetch-document",
    "tool_version": "1.0.0",
    "invoked_at": "2026-05-16T...Z",
    "invoked_by": "did:enchanter:unverified",
    "request_digest": "sha-256:...",
    "result_digest": "sha-256:...",
    "signature": {
      "protected_header": {"alg": "Ed25519", "key_id": "ephemeral-..."},
      "value": "<base64url>"
    }
  }
}
```

A downstream consumer can:

1. Fetch the issuer's public key: `curl http://localhost:8090/v1/keys`
2. Recompute the canonical form per spec § 9.
3. Verify the Ed25519 signature against the published key.

If the signature verifies, the consumer has a cryptographic guarantee that the result hasn't been tampered with since the tool ran.

---

## 6. Optional: add DPoP client identity

Claude Desktop doesn't currently emit DPoP proofs natively. If you want trust-anchored validation (spec § 12 level 3 instead of level 2), wrap the tool body in your MCP server to construct a DPoP JWT before calling `/v1/attest-mcp`. See `issuer/clientid/dpop.go` for the proof format; the issuer accepts DPoP either in the `DPoP` HTTP header or in the request body's `client_identity_proof` field.

If a DPoP proof is present and valid, the returned envelope's `invoked_by` becomes `did:jwk:<thumbprint>` and `validation_level` becomes `trust_anchored`.

---

## 7. Wiring against production (KMS + on-chain anchor)

For a production deployment instead of localhost:

1. **AWS KMS signing.** Start the issuer with `KMS_MODE=aws`, `KMS_KEY_ARN=...`. Envelopes are signed by KMS; `invoked_by` defaults to `did:enchanter:unverified` unless DPoP is present.
2. **On-chain anchoring.** After receiving each envelope, optionally POST the `result_digest` to `MimirValidationRegistry` via `anchor.AnchorEnvelope()`. The deployed Sepolia contract is `0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117` (see [`docs/deployments.md`](deployments.md)).
3. **JWK rotation.** Run `python scripts/rotate-key.py --issuer https://issuer.example.com` before swapping keys to keep historical envelopes verifiable.

---

## What you just demonstrated

A real Anthropic-owned product (Claude Desktop) speaking real MCP wire format (JSON-RPC 2.0 `tools/call`) to a real Mimir-attested server, producing real Ed25519-signed envelopes that an independent verifier can validate without trusting either Claude Desktop or your tool implementation.

This is the smallest end-to-end demonstration of "verifiable agent tool calls in the wild."
