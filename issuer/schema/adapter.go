package schema

import (
	"encoding/json"
	"errors"

	"github.com/enchanter-ai/mimir/issuer/types"
)

// ToInternal converts a validated MCP wire-format pair into the issuer's internal
// AttestRequest shape that BuildEnvelope expects.
//
// Mapping:
//
//	MCPRequest.Params  → AttestRequest.Request  (as {"name":…,"arguments":{…}})
//	MCPResponse.Result → AttestRequest.Result   (the full result payload)
//	MCPResponse.Err    → AttestRequest.Result   (the error object, so it gets digested)
//
// toolID and toolVersion are caller-supplied because the MCP wire format does not
// carry them — they are issuer-side metadata injected at attestation time.
func ToInternal(req *MCPRequest, res *MCPResponse, toolID, toolVersion string) (*types.AttestRequest, error) {
	if req == nil {
		return nil, errors.New("schema.ToInternal: req is nil")
	}
	if res == nil {
		return nil, errors.New("schema.ToInternal: res is nil")
	}
	if toolID == "" {
		return nil, errors.New("schema.ToInternal: toolID is required")
	}
	if toolVersion == "" {
		return nil, errors.New("schema.ToInternal: toolVersion is required")
	}

	// Build a plain map from the MCP params so the digest covers name + arguments.
	// This matches the shape the existing BuildEnvelope tests use and keeps the
	// digest stable even if new optional fields are added to MCPParams later.
	requestObj := map[string]interface{}{
		"name":      req.Params.Name,
		"arguments": json.RawMessage(req.Params.Arguments),
	}
	// Preserve _meta in the digest when present — tools that use progress tokens
	// produce different digests depending on whether the token was included.
	if len(req.Params.Meta) > 0 && string(req.Params.Meta) != "null" {
		requestObj["_meta"] = json.RawMessage(req.Params.Meta)
	}

	// Build the result value that will be digested.
	var resultObj interface{}
	switch {
	case res.Result != nil:
		resultObj = res.Result.Result
	case res.Err != nil:
		// Error responses are still digested so the attestation covers what
		// actually happened, not only successful tool calls.
		resultObj = map[string]interface{}{
			"error": res.Err.Error,
		}
	default:
		return nil, errors.New("schema.ToInternal: MCPResponse has neither Result nor Err")
	}

	return &types.AttestRequest{
		Request:     requestObj,
		Result:      resultObj,
		ToolID:      toolID,
		ToolVersion: toolVersion,
	}, nil
}
