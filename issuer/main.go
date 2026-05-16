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

	"log/slog"
	"time"

	"github.com/enchanter-ai/mimir/issuer/clientid"
	"github.com/enchanter-ai/mimir/issuer/envelope"
	"github.com/enchanter-ai/mimir/issuer/keystore"
	"github.com/enchanter-ai/mimir/issuer/kms"
	"github.com/enchanter-ai/mimir/issuer/ratelimit"
	"github.com/enchanter-ai/mimir/issuer/schema"
	"github.com/enchanter-ai/mimir/issuer/telemetry"
	"github.com/enchanter-ai/mimir/issuer/types"
	"github.com/gorilla/mux"
)

// server holds the active Signer (key custody backend), the JWK Set keystore,
// and the telemetry tracer that spans the hot paths.
type server struct {
	signer   kms.Signer
	keystore *keystore.Store
	tracer   telemetry.Tracer
}

func main() {
	logger := telemetry.Setup()
	tracer := telemetry.NewTracer(logger)

	signer, err := selectSigner()
	if err != nil {
		log.Fatalf("FATAL: signer init: %v", err)
	}

	ks, err := keystore.New(signer.KeyID(), signer.PublicKey())
	if err != nil {
		log.Fatalf("FATAL: keystore init: %v", err)
	}
	srv := &server{signer: signer, keystore: ks, tracer: tracer}

	log.Printf("INFO: issuer started — key_id=%s", signer.KeyID())
	log.Printf("INFO: public key (base64url): %s", base64.RawURLEncoding.EncodeToString(signer.PublicKey()))
	if n := ks.HistoricalCount(); n > 0 {
		log.Printf("INFO: keystore loaded %d historical key(s) from ISSUER_HISTORICAL_KEYS_FILE", n)
	}

	addr := listenAddr()
	log.Printf("INFO: listening on %s", addr)

	r := mux.NewRouter()
	r.HandleFunc("/v1/attest", srv.handleAttest).Methods(http.MethodPost)
	r.HandleFunc("/v1/attest-mcp", srv.handleAttestMCP).Methods(http.MethodPost)
	r.HandleFunc("/v1/healthz", handleHealthz).Methods(http.MethodGet)
	r.HandleFunc("/v1/key", srv.handleKey).Methods(http.MethodGet)
	r.HandleFunc("/v1/keys", srv.handleKeys).Methods(http.MethodGet)
	r.HandleFunc("/.well-known/jwks.json", srv.handleKeys).Methods(http.MethodGet)

	// Per-IP token-bucket rate limit (configurable via ISSUER_RATELIMIT_RPS / _BURST).
	// Healthz is exempt so liveness probes never get throttled.
	limiter := ratelimit.New()
	if limiter != nil {
		log.Printf("INFO: rate limit enabled (defaults: %d rps, burst %d per IP)", 10, 20)
	} else {
		log.Printf("WARN: rate limit DISABLED (ISSUER_RATELIMIT_RPS=0) — not recommended in production")
	}
	handler := limiter.Middleware(map[string]bool{"/v1/healthz": true})(r)

	if err := http.ListenAndServe(addr, handler); err != nil {
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
	ctx, span := s.tracer.StartSpan(r.Context(), "handle_attest",
		slog.String("method", r.Method), slog.String("path", r.URL.Path))
	defer span.End()

	var req types.AttestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		span.SetError(err)
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

	invokedBy, validationLevel, err := s.resolveClientIdentity(r, req.ClientIdentityProof)
	if err != nil {
		span.SetError(err)
		httpError(w, http.StatusBadRequest, "client_identity_proof rejected: "+err.Error())
		return
	}

	_, signSpan := s.tracer.StartSpan(ctx, "build_envelope",
		slog.String("tool_id", req.ToolID), slog.String("invoked_by", invokedBy))
	env, err := envelope.BuildEnvelopeWithIdentityCtx(
		ctx,
		req.Request, req.Result,
		req.ToolID, req.ToolVersion, invokedBy,
		s.signer,
	)
	if err != nil {
		signSpan.SetError(err)
		signSpan.End()
		span.SetError(err)
		httpError(w, http.StatusInternalServerError, "envelope build failed: "+err.Error())
		return
	}
	signSpan.SetAttr("tool_call_id", env.ToolCallID)
	signSpan.End()

	span.SetAttr("validation_level", validationLevel)
	span.SetAttr("tool_call_id", env.ToolCallID)

	resp := types.AttestResponse{Envelope: env, ValidationLevel: validationLevel}

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
		Request             json.RawMessage `json:"request"`
		Result              json.RawMessage `json:"result"`
		ToolID              string          `json:"tool_id"`
		ToolVersion         string          `json:"tool_version"`
		ClientIdentityProof string          `json:"client_identity_proof,omitempty"`
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

	invokedBy, validationLevel, err := s.resolveClientIdentity(r, outer.ClientIdentityProof)
	if err != nil {
		httpError(w, http.StatusBadRequest, "client_identity_proof rejected: "+err.Error())
		return
	}

	env, err := envelope.BuildEnvelopeWithIdentityCtx(
		r.Context(),
		internal.Request, internal.Result,
		internal.ToolID, internal.ToolVersion, invokedBy,
		s.signer,
	)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "envelope build failed: "+err.Error())
		return
	}

	resp := types.AttestResponse{Envelope: env, ValidationLevel: validationLevel}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("ERROR: attest-mcp response encode: %v", err)
	}
}

// resolveClientIdentity checks for a DPoP proof in (a) the DPoP HTTP header
// or (b) the request body's client_identity_proof field. If present and
// valid, returns ("did:jwk:<thumbprint>", "trust_anchored", nil). If absent,
// returns ("", "cryptographically_valid", nil) — the envelope still signs,
// just at validation level 2 instead of 3. If present but malformed/invalid,
// returns ("", "", err) so the handler can fail closed.
func (s *server) resolveClientIdentity(r *http.Request, bodyProof string) (string, string, error) {
	proof := r.Header.Get("DPoP")
	if proof == "" {
		proof = bodyProof
	}
	if proof == "" {
		return "", "cryptographically_valid", nil
	}
	uri := reconstructPublicURL(r)
	res, err := clientid.Verify(clientid.VerifyParams{
		Proof:        proof,
		HTTPMethod:   r.Method,
		HTTPURI:      uri,
		MaxClockSkew: 60 * time.Second,
		Now:          time.Now(),
	})
	if err != nil {
		return "", "", err
	}
	return res.DID, "trust_anchored", nil
}

// reconstructPublicURL builds the URL the client should have committed to in
// the DPoP `htu` claim. Honors X-Forwarded-Proto / X-Forwarded-Host for the
// reverse-proxy deployment.
func reconstructPublicURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		return scheme + "://" + host + path + "?" + r.URL.RawQuery
	}
	return scheme + "://" + host + path
}

// handleHealthz handles GET /v1/healthz.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleKey handles GET /v1/key — returns ONLY the active public key.
// Retained for back-compat with the v0 verifier that fetches a single JWK.
// New consumers should call /v1/keys to receive the full set, so they can
// verify envelopes signed under retired (pre-rotation) keys.
func (s *server) handleKey(w http.ResponseWriter, _ *http.Request) {
	jwk := s.keystore.Active()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(jwk); err != nil {
		log.Printf("ERROR: key response encode: %v", err)
	}
}

// handleKeys handles GET /v1/keys (also served at /.well-known/jwks.json):
// returns the JWK Set — active + historical keys — for verifying envelopes
// signed under any non-revoked key the issuer has ever published.
func (s *server) handleKeys(w http.ResponseWriter, _ *http.Request) {
	set := s.keystore.JWKSet()
	w.Header().Set("Content-Type", "application/jwk-set+json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(set); err != nil {
		log.Printf("ERROR: jwks response encode: %v", err)
	}
}

// Keep type-name reachable so the import isn't pruned if the only other
// reference is removed by a future refactor.
var _ = types.JWKPublicKey{}

// httpError writes a JSON error body with the given status code.
func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	body := map[string]string{"error": msg}
	_ = json.NewEncoder(w).Encode(body)
}
