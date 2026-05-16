# Mimir issuer service — multi-stage build.
#
# Build target: a static, distroless binary that runs as a non-root user with
# only the issuer + ca-certs on disk. ~25 MB final image.
#
#   docker build -f issuer/Dockerfile -t mimir-issuer:dev .
#   docker run --rm -p 8080:8080 mimir-issuer:dev
#
# For production:
#   docker run --rm -p 8080:8080 \
#     -e KMS_MODE=aws -e KMS_KEY_ARN=arn:aws:kms:... -e AWS_REGION=us-east-1 \
#     -e AWS_ACCESS_KEY_ID=... -e AWS_SECRET_ACCESS_KEY=... \
#     -e ISSUER_RATELIMIT_RPS=50 -e ISSUER_RATELIMIT_BURST=100 \
#     mimir-issuer:tag

# ─── Stage 1: build ──────────────────────────────────────────────────────
FROM golang:1.22-alpine AS build

WORKDIR /src

# Cache module downloads separately from source compile.
COPY go.mod go.sum ./
RUN go mod download

COPY . ./

# Static build, no CGO, strip symbol table for smaller image.
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/issuer ./

# ─── Stage 2: runtime ────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/issuer /app/issuer

# Distroless nonroot runs as uid 65532. The issuer needs no writable disk.
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/issuer"]

# Liveness probe via /v1/healthz (configure in your orchestrator).
LABEL org.opencontainers.image.title="Mimir Issuer"
LABEL org.opencontainers.image.description="MCP tool-call provenance envelope signer (Ed25519)"
LABEL org.opencontainers.image.source="https://github.com/enchanter-ai/mimir"
LABEL org.opencontainers.image.licenses="Apache-2.0"
