# Mimir scoring service — multi-stage build.
#
#   docker build -f scoring/Dockerfile -t mimir-scoring:dev scoring/
#   docker run --rm -p 9090:9090 -e ANTHROPIC_API_KEY=... mimir-scoring:dev
#
# For MOCK_MODE (no API key needed):
#   docker run --rm -p 9090:9090 -e MOCK_MODE=1 mimir-scoring:dev

# ─── Stage 1: build ──────────────────────────────────────────────────────
FROM node:22-alpine AS build

WORKDIR /src

# Cache npm install separately from source compile.
COPY package.json package-lock.json* ./
RUN npm ci

COPY . ./
RUN npx tsc --noEmit && \
    npx tsc --outDir /out --module nodenext --moduleResolution nodenext --target es2022

# ─── Stage 2: runtime ────────────────────────────────────────────────────
FROM node:22-alpine

WORKDIR /app

# Re-install production-only deps in the runtime layer.
COPY package.json package-lock.json* ./
RUN npm ci --omit=dev && npm cache clean --force

COPY --from=build /out /app/dist
COPY src /app/src

# Non-root user; node images don't ship a stable one, so create our own.
RUN addgroup -S mimir && adduser -S -G mimir mimir && \
    chown -R mimir:mimir /app
USER mimir

EXPOSE 9090
ENTRYPOINT ["npx", "tsx", "src/server.ts"]

LABEL org.opencontainers.image.title="Mimir Scoring"
LABEL org.opencontainers.image.description="MCP tool-call σ-bound quality scoring (Claude Sonnet 4.6)"
LABEL org.opencontainers.image.source="https://github.com/enchanter-ai/mimir"
LABEL org.opencontainers.image.licenses="Apache-2.0"
