// Package main is the entry point for the MCP Provenance Issuer Service (MVP).
//
// Key custody backends (controlled by KMS_MODE env var):
//   - ephemeral (default) — in-process Ed25519 keypair regenerated on restart. Dev only.
//   - mock                — fixed-seed deterministic keypair. Tests only.
//   - aws                 — AWS KMS-backed Ed25519. Production. Requires KMS_KEY_ARN
//                           and standard AWS_REGION / AWS credential env vars.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/enchanter-ai/mimir/issuer/envelope"
	"github.com/enchanter-ai/mimir/issuer/kms"
	"github.com/enchanter-ai/mimir/issuer/schema"
	"github.com/enchanter-ai/mimir/issuer/types"
	"github.com/gorilla/mux"
)

// server holds the active Signer (key custody backend).
type server struct {
	signer kms.Signer
}

func main() {
	signer, err := selectSigner()
	if err != nil {
		log.Fatalf("FATAL: signer init: %v", err)
	}

	srv := &server{signer: signer}

	log.Printf("INFO: issuer started — key_id=%s", signer.KeyID())
	log.Printf("INFO: public key (base64url): %s", base64.RawURLEncoding.EncodeToString(signer.PublicKey()))

	addr := listenAddr()
	log.Printf("INFO: listening on %s", addr)

	r := mux.NewRouter()
	r.HandleFunc("/v1/attest", srv.handleAttest).Methods(http.MethodPost)
	r.HandleFunc("/v1/attest-mcp", srv.handleAttestMCP).Methods(http.MethodPost)
	r.HandleFunc("/v1/healthz", handleHealthz).Methods(http.MethodGet)
	r.HandleFunc("/v1/key", srv.handleKey).Methods(http.MethodGet)

	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("FATAL: server error: %v", err)
	}
}

// selectSigner reads KMS_MODE and constructs the appropriate Signer.
func selectSigner() (kms.Signer, error) {
	mode := os.Getenv("KMS_MODE")
	if mode == "" {
		mode = "ephemeral"
	}
	switch mode {
	case "ephemeral":
		return kms.NewEphemeralSigner("")
	case "mock":
		return kms.NewMockKMS(), nil
	case "aws":
		arn := os.Getenv("KMS_KEY_ARN")
		region := os.Getenv("AWS_REGION")
		if region == "" {
			region = "us-east-1"
		}
		return kms.NewAWSSigner(context.Background(), arn, region)
	default:
		return nil, &configError{Mode: mode}
	}
}

type configError struct{ Mode string }

func (e *configError) Error() string { return "unknown KMS_MODE: " + e.Mode }

// listenAddr returns the bind address, defaulting to :8080.
func listenAddr() string {
	if port := os.Getenv("ISSUER_PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}

// handleAttest handles POST /v1/attest.
func (s *server) handleAttest(w http.ResponseWriter, r *http.Request) {
	var req types.AttestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	if req.ToolID == "" {
		httpError(w, http.StatusBadRequest, "tool_id is required")
		return
	}
	if req.ToolVersion == "" {
		httpError(w, http.StatusBadRequest, "tool_version is required")
		return
	}
	if req.Request == nil {
		httpError(w, http.StatusBadRequest, "request is required")
		return
	}
	if req.Result == nil {
		httpError(w, http.StatusBadRequest, "result is required")
		return
	}

	env, err := envelope.BuildEnvelopeCtx(
		r.Context(),
		req.Request,
		req.Result,
		req.ToolID,
		req.ToolVersion,
		s.signer,
	)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "envelope build failed: "+err.Error())
		return
	}

	resp := types.AttestResponse{
		Envelope:        env,
		ValidationLevel: "cryptographically_valid",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("ERROR: response encode: %v", err)
	}
}

// handleAttestMCP handles POST /v1/attest-mcp — accepts the MCP wire format.
func (s *server) handleAttestMCP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpError(w, http.StatusBadRequest, "cannot read body: "+err.Error())
		return
	}

	var outer struct {
		Request     json.RawMessage `json:"request"`
		Result      json.RawMessage `json:"result"`
		ToolID      string          `json:"tool_id"`
		ToolVersion string          `json:"tool_version"`
	}
	if err := json.Unmarshal(body, &outer); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if outer.ToolID == "" {
		httpError(w, http.StatusBadRequest, "tool_id is required")
		return
	}
	if outer.ToolVersion == "" {
		httpError(w, http.StatusBadRequest, "tool_version is required")
		return
	}

	mcpReq, err := schema.ValidateRequest(outer.Request)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid MCP request: "+err.Error())
		return
	}
	mcpRes, err := schema.ValidateResult(outer.Result)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid MCP result: "+err.Error())
		return
	}

	internal, err := schema.ToInternal(mcpReq, mcpRes, outer.ToolID, outer.ToolVersion)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "adapter error: "+err.Error())
		return
	}

	env, err := envelope.BuildEnvelopeCtx(
		r.Context(),
		internal.Request, internal.Result,
		internal.ToolID, internal.ToolVersion,
		s.signer,
	)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "envelope build failed: "+err.Error())
		return
	}

	resp := types.AttestResponse{
		Envelope:        env,
		ValidationLevel: "cryptographically_valid",
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("ERROR: attest-mcp response encode: %v", err)
	}
}

// handleHealthz handles GET /v1/healthz.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleKey handles GET /v1/key — returns the active public key as a JWK.
func (s *server) handleKey(w http.ResponseWriter, _ *http.Request) {
	jwk := types.JWKPublicKey{
		Kty: "OKP",
		Crv: "Ed25519",
		X:   base64.RawURLEncoding.EncodeToString(s.signer.PublicKey()),
		Kid: s.signer.KeyID(),
		Use: "sig",
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(jwk); err != nil {
		log.Printf("ERROR: key response encode: %v", err)
	}
}

// httpError writes a JSON error body with the given status code.
func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	body := map[string]string{"error": msg}
	_ = json.NewEncoder(w).Encode(body)
}
