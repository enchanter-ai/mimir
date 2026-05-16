# Mimir-attested MCP server — starter template

A 60-line MCP server that wraps any tool call in a Mimir provenance envelope. **Fork this** as the starting point for shipping your first Mimir-attested tool.

## Quick start

```bash
# 1. Get the Mimir issuer running locally (uses an ephemeral dev key).
git clone git@github.com:enchanter-ai/mimir.git ../mimir
cd ../mimir/issuer && go run . &

# 2. In this directory:
pip install mcp requests
ISSUER_URL=http://localhost:8080 python server.py
```

The MCP server is now running on stdio, exposing one tool (`expensive_lookup`). Test it with the official MCP SDK client:

```bash
python ../../mimir/tests/mcp/mcp_client.py
# (after pointing SERVER_SCRIPT at this server.py)
```

## What to change

There's exactly one function to replace: [`expensive_lookup`](server.py). Swap the body with your actual tool logic, keep the call to `attest()`. Everything else — issuer URL, tool DID, version — is configured via env vars so deployment never edits the source.

## What the client sees

Every tool call returns a JSON object:

```json
{
  "result": "<your tool's actual output>",
  "envelope": {
    "version": "mcp-provenance/2026-05-13-ed25519",
    "tool_call_id": "<uuid>",
    "tool_id": "did:web:mycompany.com:tools:expensive-lookup",
    "tool_version": "1.0.0",
    "request_digest": "sha-256:...",
    "result_digest": "sha-256:...",
    "signature": {
      "protected_header": {"alg": "Ed25519", "key_id": "..."},
      "value": "<base64url Ed25519 signature>"
    }
  }
}
```

Clients that don't know about envelopes just see `result`. Clients that do can verify the envelope against the issuer's published JWK at `/v1/keys`.

## Production checklist

Before going public with your attested tool:

- [ ] Replace the toy tool body with your real implementation
- [ ] Set a real `TOOL_DID` (we recommend a `did:web:` pointing at your domain so anyone can resolve it to your org)
- [ ] Point `ISSUER_URL` at a production Mimir issuer (yours, or a shared one) — production issuers should run with `KMS_MODE=aws` per [`deploy/aws-kms/README.md`](../../deploy/aws-kms/README.md)
- [ ] Decide on failure-mode policy: if the issuer is unreachable, do you serve the result anyway with `attestation_error`, or fail the tool call entirely? The starter degrades gracefully; production tools concerned with provable correctness should fail closed.
- [ ] Optionally: emit a DPoP `ClientIdentityProof` in the `client_identity_proof` body field so the envelope's `invoked_by` reflects the actual client identity. See [`docs/integrate-claude-desktop.md`](../../docs/integrate-claude-desktop.md) § 6.

## Why MCP clients keep working

The `result` field is a normal Markdown / JSON string Claude Desktop / Cursor / Cline already render. The `envelope` field is extra structured content they ignore by default — until they're updated to verify envelopes, at which point your tool starts benefiting from cryptographic trust signals without any breaking API change.

## License

The starter file is licensed under MIT — fork freely, no attribution needed.
