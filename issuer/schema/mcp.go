// Package schema defines Go structs for the real MCP wire format (JSON-RPC 2.0)
// and validation functions that enforce the tools/call contract from the MCP spec.
//
// Wire shapes covered:
//
//	MCPRequest  — {"jsonrpc":"2.0","id":…,"method":"tools/call","params":{"name":…,"arguments":{…}}}
//	MCPResult   — {"jsonrpc":"2.0","id":…,"result":{"content":[…]}} or error variant
//	MCPError    — {"jsonrpc":"2.0","id":…,"error":{"code":…,"message":…}}
//
// The MCP spec also defines optional _meta fields (progress tokens, resource
// references, partial-result tokens).  Those fields are preserved in RawMeta so
// the digest layer can include them, but ValidateRequest does not require them.
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	jsonRPCVersion    = "2.0"
	methodToolsCall   = "tools/call"
)

// MCPParams holds the params object of a tools/call request.
type MCPParams struct {
	// Name is the tool name being invoked (required, non-empty).
	Name string `json:"name"`

	// Arguments MUST be a JSON object (map), not null or an array.
	// Stored as raw bytes so callers can forward it verbatim to the tool.
	Arguments json.RawMessage `json:"arguments"`

	// Meta carries the optional _meta field from the MCP spec (progress tokens,
	// partial-result tokens).  Preserved for digest completeness; not validated.
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// MCPRequest is the JSON-RPC 2.0 envelope for a tools/call invocation.
type MCPRequest struct {
	JSONRPC string     `json:"jsonrpc"`
	ID      any        `json:"id"` // string | number | null per JSON-RPC 2.0
	Method  string     `json:"method"`
	Params  *MCPParams `json:"params"`
}

// MCPContent is a single content item in a tools/call result.
// MCP spec defines type "text", "image", "resource", and "audio".
type MCPContent struct {
	Type string `json:"type"`
	// Text is set when Type == "text".
	Text string `json:"text,omitempty"`
	// Data is set for binary types (image, audio) — base64-encoded.
	Data     string          `json:"data,omitempty"`
	MimeType string          `json:"mimeType,omitempty"`
	// Raw preserves unknown / future content fields.
	Raw json.RawMessage `json:"-"`
}

// MCPResultPayload is the result object inside a successful tools/call response.
type MCPResultPayload struct {
	// Content is the primary output array (required by spec).
	Content []MCPContent `json:"content"`
	// IsError signals that the tool itself reported an error (not the transport).
	// This is distinct from a JSON-RPC error response.
	IsError bool `json:"isError,omitempty"`
	// Meta carries optional _meta (e.g. nextCursor for pagination).
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// MCPResult is the JSON-RPC 2.0 envelope for a successful tools/call response.
type MCPResult struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      any               `json:"id"`
	Result  *MCPResultPayload `json:"result"`
}

// MCPErrorObject is the error detail in a JSON-RPC error response.
type MCPErrorObject struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// MCPError is the JSON-RPC 2.0 envelope for a failed tools/call response.
type MCPError struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Error   *MCPErrorObject `json:"error"`
}

// MCPResponse wraps either a successful MCPResult or an MCPError.
// Exactly one of Result and Err will be non-nil after a successful parse.
type MCPResponse struct {
	Result *MCPResult
	Err    *MCPError
}

// -------------------------------------------------------------------
// ValidateRequest
// -------------------------------------------------------------------

// ValidateRequest parses raw bytes and enforces the tools/call request contract:
//
//   - JSON is valid
//   - jsonrpc == "2.0"
//   - method == "tools/call"
//   - params is present
//   - params.name is non-empty
//   - params.arguments is a JSON object (not null, not array, not scalar)
func ValidateRequest(raw json.RawMessage) (*MCPRequest, error) {
	if len(raw) == 0 {
		return nil, errors.New("mcp: empty request body")
	}

	var req MCPRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("mcp: request parse error: %w", err)
	}

	if req.JSONRPC != jsonRPCVersion {
		return nil, fmt.Errorf("mcp: jsonrpc must be %q, got %q", jsonRPCVersion, req.JSONRPC)
	}
	if req.Method != methodToolsCall {
		return nil, fmt.Errorf("mcp: method must be %q, got %q", methodToolsCall, req.Method)
	}
	if req.Params == nil {
		return nil, errors.New("mcp: params is required")
	}
	if req.Params.Name == "" {
		return nil, errors.New("mcp: params.name is required and must be non-empty")
	}
	if err := requireObject(req.Params.Arguments, "params.arguments"); err != nil {
		return nil, err
	}

	return &req, nil
}

// -------------------------------------------------------------------
// ValidateResult
// -------------------------------------------------------------------

// ValidateResult parses raw bytes and accepts either a successful result or an
// error response.  Both are valid terminal states for a tools/call invocation.
//
// Rules:
//   - JSON is valid
//   - jsonrpc == "2.0"
//   - exactly one of "result" or "error" is present (not both, not neither)
//   - if "result": result.content must be an array (may be empty)
//   - if "error": error.code must be an integer and error.message non-empty
func ValidateResult(raw json.RawMessage) (*MCPResponse, error) {
	if len(raw) == 0 {
		return nil, errors.New("mcp: empty result body")
	}

	// Probe for which variant this is.
	var probe struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("mcp: result parse error: %w", err)
	}
	if probe.JSONRPC != jsonRPCVersion {
		return nil, fmt.Errorf("mcp: jsonrpc must be %q, got %q", jsonRPCVersion, probe.JSONRPC)
	}

	hasResult := len(probe.Result) > 0 && string(probe.Result) != "null"
	hasError := len(probe.Error) > 0 && string(probe.Error) != "null"

	switch {
	case hasResult && hasError:
		return nil, errors.New("mcp: result contains both 'result' and 'error' — invalid JSON-RPC 2.0")
	case !hasResult && !hasError:
		return nil, errors.New("mcp: result must contain either 'result' or 'error'")

	case hasError:
		var errResp MCPError
		if err := json.Unmarshal(raw, &errResp); err != nil {
			return nil, fmt.Errorf("mcp: error response parse: %w", err)
		}
		if errResp.Error == nil {
			return nil, errors.New("mcp: 'error' field is null")
		}
		if errResp.Error.Message == "" {
			return nil, errors.New("mcp: error.message is required and must be non-empty")
		}
		return &MCPResponse{Err: &errResp}, nil

	default: // hasResult
		var okResp MCPResult
		if err := json.Unmarshal(raw, &okResp); err != nil {
			return nil, fmt.Errorf("mcp: success response parse: %w", err)
		}
		if okResp.Result == nil {
			return nil, errors.New("mcp: 'result' field is null")
		}
		// content must be an array; it may be empty but must not be missing.
		if okResp.Result.Content == nil {
			return nil, errors.New("mcp: result.content is required (may be empty array but must be present)")
		}
		return &MCPResponse{Result: &okResp}, nil
	}
}

// -------------------------------------------------------------------
// helpers
// -------------------------------------------------------------------

// requireObject returns an error if raw is not a JSON object.
// Null, arrays, strings, numbers, and booleans are all rejected.
func requireObject(raw json.RawMessage, field string) error {
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("mcp: %s must be a JSON object, got null/missing", field)
	}
	// A JSON object starts with '{'.
	for _, b := range raw {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		if b != '{' {
			return fmt.Errorf("mcp: %s must be a JSON object, got %q", field, string(raw))
		}
		return nil
	}
	return fmt.Errorf("mcp: %s is empty", field)
}
