package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/enchanter-ai/mimir/issuer/schema"
)

// -------------------------------------------------------------------
// ValidateRequest tests
// -------------------------------------------------------------------

// TestValidateRequest_HappyPath verifies a well-formed tools/call request is accepted.
func TestValidateRequest_HappyPath(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/call",
		"params": {
			"name": "fetch_document",
			"arguments": {"url": "https://example.com/doc.txt"}
		}
	}`)

	req, err := schema.ValidateRequest(raw)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if req.Params.Name != "fetch_document" {
		t.Errorf("params.name: got %q, want %q", req.Params.Name, "fetch_document")
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc: got %q, want %q", req.JSONRPC, "2.0")
	}
}

// TestValidateRequest_MissingJSONRPC verifies that a request without jsonrpc is rejected.
func TestValidateRequest_MissingJSONRPC(t *testing.T) {
	raw := json.RawMessage(`{
		"id": 1,
		"method": "tools/call",
		"params": {"name": "fetch_document", "arguments": {}}
	}`)

	_, err := schema.ValidateRequest(raw)
	if err == nil {
		t.Fatal("expected error for missing jsonrpc, got nil")
	}
}

// TestValidateRequest_WrongJSONRPCVersion verifies that jsonrpc "1.0" is rejected.
func TestValidateRequest_WrongJSONRPCVersion(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "1.0",
		"id": 1,
		"method": "tools/call",
		"params": {"name": "fetch_document", "arguments": {}}
	}`)

	_, err := schema.ValidateRequest(raw)
	if err == nil {
		t.Fatal("expected error for jsonrpc=1.0, got nil")
	}
}

// TestValidateRequest_WrongMethod verifies that a non-tools/call method is rejected.
func TestValidateRequest_WrongMethod(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/list",
		"params": {"name": "fetch_document", "arguments": {}}
	}`)

	_, err := schema.ValidateRequest(raw)
	if err == nil {
		t.Fatal("expected error for wrong method, got nil")
	}
}

// TestValidateRequest_MissingParams verifies that a missing params object is rejected.
func TestValidateRequest_MissingParams(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/call"
	}`)

	_, err := schema.ValidateRequest(raw)
	if err == nil {
		t.Fatal("expected error for missing params, got nil")
	}
}

// TestValidateRequest_MissingParamsName verifies that params.name="" is rejected.
func TestValidateRequest_MissingParamsName(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/call",
		"params": {"name": "", "arguments": {"q": "test"}}
	}`)

	_, err := schema.ValidateRequest(raw)
	if err == nil {
		t.Fatal("expected error for empty params.name, got nil")
	}
}

// TestValidateRequest_ArgumentsNull verifies that params.arguments=null is rejected.
func TestValidateRequest_ArgumentsNull(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/call",
		"params": {"name": "fetch_document", "arguments": null}
	}`)

	_, err := schema.ValidateRequest(raw)
	if err == nil {
		t.Fatal("expected error for arguments=null, got nil")
	}
}

// TestValidateRequest_ArgumentsArray verifies that params.arguments as array is rejected.
func TestValidateRequest_ArgumentsArray(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/call",
		"params": {"name": "fetch_document", "arguments": ["url"]}
	}`)

	_, err := schema.ValidateRequest(raw)
	if err == nil {
		t.Fatal("expected error for arguments=array, got nil")
	}
}

// TestValidateRequest_StringID verifies that string IDs (valid per JSON-RPC 2.0) are accepted.
func TestValidateRequest_StringID(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": "req-abc-123",
		"method": "tools/call",
		"params": {"name": "query_db", "arguments": {"sql": "SELECT 1"}}
	}`)

	req, err := schema.ValidateRequest(raw)
	if err != nil {
		t.Fatalf("expected no error for string id, got: %v", err)
	}
	if req.Params.Name != "query_db" {
		t.Errorf("name: got %q", req.Params.Name)
	}
}

// TestValidateRequest_MetaPreserved verifies that optional _meta is preserved without error.
func TestValidateRequest_MetaPreserved(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 42,
		"method": "tools/call",
		"params": {
			"name": "search_web",
			"arguments": {"query": "go testing"},
			"_meta": {"progressToken": "tok-001"}
		}
	}`)

	req, err := schema.ValidateRequest(raw)
	if err != nil {
		t.Fatalf("expected no error with _meta, got: %v", err)
	}
	if len(req.Params.Meta) == 0 {
		t.Error("expected _meta to be preserved, got empty")
	}
}

// -------------------------------------------------------------------
// ValidateResult tests
// -------------------------------------------------------------------

// TestValidateResult_HappyPath verifies a well-formed success result is accepted.
func TestValidateResult_HappyPath(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"result": {
			"content": [{"type": "text", "text": "hello"}]
		}
	}`)

	resp, err := schema.ValidateResult(raw)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.Result == nil {
		t.Fatal("expected Result to be set")
	}
	if len(resp.Result.Result.Content) != 1 {
		t.Errorf("content length: got %d, want 1", len(resp.Result.Result.Content))
	}
}

// TestValidateResult_ErrorVariant verifies that an error-shaped result is accepted as MCPError.
func TestValidateResult_ErrorVariant(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"error": {
			"code": -32601,
			"message": "Method not found"
		}
	}`)

	resp, err := schema.ValidateResult(raw)
	if err != nil {
		t.Fatalf("expected no error for error-variant result, got: %v", err)
	}
	if resp.Err == nil {
		t.Fatal("expected Err to be set")
	}
	if resp.Err.Error.Code != -32601 {
		t.Errorf("error.code: got %d, want -32601", resp.Err.Error.Code)
	}
}

// TestValidateResult_BothResultAndError verifies that having both fields is rejected.
func TestValidateResult_BothResultAndError(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"result": {"content": []},
		"error": {"code": -1, "message": "also broken"}
	}`)

	_, err := schema.ValidateResult(raw)
	if err == nil {
		t.Fatal("expected error when both result and error are present, got nil")
	}
}

// TestValidateResult_NeitherResultNorError verifies that omitting both fields is rejected.
func TestValidateResult_NeitherResultNorError(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1
	}`)

	_, err := schema.ValidateResult(raw)
	if err == nil {
		t.Fatal("expected error when neither result nor error present, got nil")
	}
}

// TestValidateResult_EmptyContentArray verifies that an empty content array is accepted.
func TestValidateResult_EmptyContentArray(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"result": {"content": []}
	}`)

	resp, err := schema.ValidateResult(raw)
	if err != nil {
		t.Fatalf("expected no error for empty content array, got: %v", err)
	}
	if resp.Result == nil {
		t.Fatal("expected Result to be set")
	}
}

// TestValidateResult_IsErrorTrue verifies that isError=true in result is accepted (tool-level error).
func TestValidateResult_IsErrorTrue(t *testing.T) {
	raw := json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"result": {
			"content": [{"type": "text", "text": "tool failed: rate limit exceeded"}],
			"isError": true
		}
	}`)

	resp, err := schema.ValidateResult(raw)
	if err != nil {
		t.Fatalf("expected no error for isError=true result, got: %v", err)
	}
	if !resp.Result.Result.IsError {
		t.Error("expected IsError to be true")
	}
}
