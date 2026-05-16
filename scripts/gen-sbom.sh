#!/usr/bin/env bash
# gen-sbom.sh — emit CycloneDX SBOMs for every ecosystem in this repo.
#
# Produces:
#   sbom/issuer.cdx.json       — Go deps of issuer/
#   sbom/anchor.cdx.json       — Go deps of anchor/go/
#   sbom/bench.cdx.json        — Go deps of bench/
#   sbom/scoring.cdx.json      — npm deps of scoring/
#   sbom/anchor-build.cdx.json — npm deps of anchor/ (solc-js)
#   sbom/rust.cdx.json         — Cargo deps of spec/reference-impl-rust/
#
# Uses `cyclonedx-gomod` for Go, `cyclonedx-npm` for npm, `cargo-cyclonedx` for
# Rust. The script installs missing tools via `go install` / `npm i -g` /
# `cargo install`. SBOMs are emitted as CycloneDX 1.5 JSON.
#
# Idempotent. Safe to re-run; existing SBOM files are overwritten.

set -euo pipefail
cd "$(dirname "$0")/.."

mkdir -p sbom

echo "==== Mimir SBOM generation ===="
echo "  commit: $(git rev-parse --short HEAD)"
echo

# ─── Go ─────────────────────────────────────────────────────────────────
ensure_gomod_cyclonedx() {
  if ! command -v cyclonedx-gomod >/dev/null 2>&1; then
    echo "  installing cyclonedx-gomod (one-time)..."
    go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest
  fi
}

run_gomod() {
  local dir="$1"
  local out="$2"
  ensure_gomod_cyclonedx
  echo "  [go]    $dir -> sbom/$out"
  ( cd "$dir" && cyclonedx-gomod mod -licenses -output "../$out" >/dev/null )
}

# Issuer's go.mod lives at issuer/go.mod
run_gomod "issuer" "sbom/issuer.cdx.json"
run_gomod "anchor/go" "sbom/anchor.cdx.json"
run_gomod "bench" "sbom/bench.cdx.json"

# ─── npm ────────────────────────────────────────────────────────────────
ensure_cyclonedx_npm() {
  if ! command -v cyclonedx-npm >/dev/null 2>&1; then
    echo "  installing @cyclonedx/cyclonedx-npm (one-time)..."
    npm install -g @cyclonedx/cyclonedx-npm
  fi
}

run_npm() {
  local dir="$1"
  local out="$2"
  ensure_cyclonedx_npm
  echo "  [npm]   $dir -> sbom/$out"
  ( cd "$dir" && cyclonedx-npm --output-file "../sbom/$out" --spec-version 1.5 --output-format json --omit dev >/dev/null )
}

run_npm "scoring" "scoring.cdx.json"
# anchor/ has only solc-js as a dev-time tool; skip unless lockfile exists.
if [[ -f anchor/package-lock.json ]]; then
  run_npm "anchor" "anchor-build.cdx.json"
fi

# ─── Rust ───────────────────────────────────────────────────────────────
ensure_cargo_cyclonedx() {
  if ! cargo cyclonedx --version >/dev/null 2>&1; then
    echo "  installing cargo-cyclonedx (one-time)..."
    cargo install cargo-cyclonedx
  fi
}

run_cargo() {
  local dir="$1"
  local out="$2"
  ensure_cargo_cyclonedx
  echo "  [cargo] $dir -> sbom/$out"
  ( cd "$dir" && cargo cyclonedx --format json --override-filename "../../$out" >/dev/null )
}

run_cargo "spec/reference-impl-rust" "sbom/rust.cdx.json"

echo
echo "==== SBOMs generated ===="
ls -la sbom/*.cdx.json
