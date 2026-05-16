#!/usr/bin/env bash
# verify-build.sh — reproducibility check for the on-chain anchor bytecode.
#
# This is the script auditors / external verifiers run to confirm that the
# bytecode deployed on-chain matches the Solidity source in this repo at the
# current commit. Failing this check means the deployed contract is NOT
# auditable against `main`.
#
# Inputs (env or args):
#   CONTRACT_ADDRESS   on-chain address to compare against
#   RPC_URL            chain RPC (e.g. https://ethereum-sepolia.publicnode.com)
#   (optional) EXPECTED_BYTECODE_HASH — sha256 hex of the .bin file; if set,
#       we additionally assert the local compile matches this hash, which
#       lets you pin the compile output across machines independently of
#       on-chain comparison.
#
# Usage:
#   ./scripts/verify-build.sh \
#       CONTRACT_ADDRESS=0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117 \
#       RPC_URL=https://ethereum-sepolia.publicnode.com
#
# Exit codes:
#   0   bytecode matches (deployment is genuine, source is auditable)
#   1   bytecode mismatch (DEPLOYMENT DOES NOT CORRESPOND TO THIS SOURCE)
#   2   prereq missing (node / solc / curl)
#   3   bad usage

set -euo pipefail

cd "$(dirname "$0")/.."

# ─── Parse args / env ───────────────────────────────────────────────────
for arg in "$@"; do
  case "$arg" in
    CONTRACT_ADDRESS=*) export CONTRACT_ADDRESS="${arg#*=}" ;;
    RPC_URL=*)          export RPC_URL="${arg#*=}" ;;
    EXPECTED_BYTECODE_HASH=*) export EXPECTED_BYTECODE_HASH="${arg#*=}" ;;
    --skip-onchain)     SKIP_ONCHAIN=1 ;;
    *) echo "unknown arg: $arg"; exit 3 ;;
  esac
done

echo "==== Mimir bytecode reproducibility verifier ===="
echo "  commit       : $(git rev-parse --short HEAD)"
echo "  branch       : $(git rev-parse --abbrev-ref HEAD)"
echo "  workdir      : $(pwd)"

# ─── Prereqs ────────────────────────────────────────────────────────────
need() {
  command -v "$1" >/dev/null 2>&1 || { echo "ERROR: $1 not in PATH"; exit 2; }
}
need node
need npm
need sha256sum 2>/dev/null || need shasum
HASH_CMD="sha256sum"
if ! command -v sha256sum >/dev/null 2>&1; then
  HASH_CMD="shasum -a 256"
fi

# ─── Recompile bytecode from source ─────────────────────────────────────
echo
echo "[1/3] Recompile contract source -> bytecode"
cd anchor
if [[ ! -d node_modules/solc ]]; then
  echo "  installing solc-js 0.8.20 (one-time)..."
  npm install --no-save --silent solc@0.8.20
fi
node compile.js >/dev/null
LOCAL_CREATION_BIN="go/abi/MimirValidationRegistry.bin"
LOCAL_RUNTIME_BIN="go/abi/MimirValidationRegistry.runtime.bin"
if [[ ! -s "$LOCAL_CREATION_BIN" || ! -s "$LOCAL_RUNTIME_BIN" ]]; then
  echo "ERROR: compile produced no bytecode at $LOCAL_CREATION_BIN or $LOCAL_RUNTIME_BIN"; exit 1
fi
LOCAL_HASH=$($HASH_CMD "$LOCAL_RUNTIME_BIN" | awk '{print $1}')
echo "  source recompiled: $(wc -c < "$LOCAL_CREATION_BIN" | awk '{print $1}') chars creation, $(wc -c < "$LOCAL_RUNTIME_BIN" | awk '{print $1}') chars runtime"
echo "  sha256(local runtime) : $LOCAL_HASH"
cd ..

# ─── Optional: pinned-hash check ────────────────────────────────────────
if [[ -n "${EXPECTED_BYTECODE_HASH:-}" ]]; then
  echo
  echo "[2/3] Pinned-hash check"
  if [[ "$LOCAL_HASH" != "$EXPECTED_BYTECODE_HASH" ]]; then
    echo "  MISMATCH:"
    echo "    local hash  : $LOCAL_HASH"
    echo "    expected    : $EXPECTED_BYTECODE_HASH"
    exit 1
  fi
  echo "  local hash matches pinned expected hash."
else
  echo
  echo "[2/3] Pinned-hash check: skipped (EXPECTED_BYTECODE_HASH not set)"
fi

# ─── On-chain comparison ────────────────────────────────────────────────
if [[ "${SKIP_ONCHAIN:-0}" == "1" || -z "${CONTRACT_ADDRESS:-}" || -z "${RPC_URL:-}" ]]; then
  echo
  echo "[3/3] On-chain comparison: skipped (CONTRACT_ADDRESS or RPC_URL not set)"
  echo
  echo "==== VERIFY OK (local compile only) ===="
  exit 0
fi

echo
echo "[3/3] Compare against on-chain bytecode"
echo "  contract : $CONTRACT_ADDRESS"
echo "  rpc      : $RPC_URL"

ONCHAIN=$(curl -fsSL -X POST "$RPC_URL" \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getCode\",\"params\":[\"$CONTRACT_ADDRESS\",\"latest\"],\"id\":1}" \
  | node -e 'let s=""; process.stdin.on("data",d=>s+=d); process.stdin.on("end",()=>process.stdout.write(JSON.parse(s).result))')

if [[ -z "$ONCHAIN" || "$ONCHAIN" == "0x" ]]; then
  echo "ERROR: no code at $CONTRACT_ADDRESS on this chain"
  exit 1
fi

LOCAL_RUNTIME_HEX=$(cat anchor/go/abi/MimirValidationRegistry.runtime.bin)
LOCAL_RUNTIME_HEX="${LOCAL_RUNTIME_HEX#0x}"
ONCHAIN_HEX="${ONCHAIN#0x}"

echo "  onchain bytecode    : ${#ONCHAIN_HEX} hex chars"
echo "  local runtime       : ${#LOCAL_RUNTIME_HEX} hex chars"

# ─── Strict bytecode equality ───────────────────────────────────────────
# The trailing 43 bytes (86 hex chars) of Solidity-compiled runtime bytecode
# is the CBOR-encoded metadata (IPFS hash of the metadata.json + solc version
# + experimental flag). It changes when:
#   - solc version differs
#   - metadata content differs (e.g. different absolute source paths)
#   - the metadata IPFS hash differs (recomputed every compile)
# We compare both the FULL bytecode (strict) and the metadata-stripped
# bytecode (semantic) and report each separately.

if [[ "$LOCAL_RUNTIME_HEX" == "$ONCHAIN_HEX" ]]; then
  echo
  echo "  byte-for-byte match : YES (strict — including metadata suffix)"
  STRICT_OK=1
else
  echo
  echo "  byte-for-byte match : NO (metadata suffix likely differs — checking semantic equality)"
  STRICT_OK=0
fi

# Strip the metadata suffix and re-compare.
STRIP=86 # 43 bytes * 2 hex chars
if [[ ${#LOCAL_RUNTIME_HEX} -gt $STRIP && ${#ONCHAIN_HEX} -gt $STRIP ]]; then
  LOCAL_NOMETA="${LOCAL_RUNTIME_HEX:0:$((${#LOCAL_RUNTIME_HEX} - STRIP))}"
  ONCHAIN_NOMETA="${ONCHAIN_HEX:0:$((${#ONCHAIN_HEX} - STRIP))}"
  if [[ "$LOCAL_NOMETA" == "$ONCHAIN_NOMETA" ]]; then
    echo "  semantic match      : YES (metadata-stripped bytecode is identical — code is auditable)"
    if [[ "$STRICT_OK" -eq 0 ]]; then
      echo
      echo "==== VERIFY OK (semantic) ===="
      echo
      echo "The deployed contract is the SAME CODE as this source — only the"
      echo "CBOR metadata suffix differs (most commonly because the .json"
      echo "source paths embedded by solc don't match between local and CI"
      echo "compile environments). For source verification on Etherscan this"
      echo "is sufficient."
      exit 0
    fi
    echo
    echo "==== VERIFY OK (strict) ===="
    exit 0
  fi
  echo
  echo "  semantic match      : NO"
  echo "  ERROR: deployed bytecode does not correspond to this source."
  echo "  Showing first 64 hex chars of each for diagnosis:"
  echo "    local:   ${LOCAL_RUNTIME_HEX:0:64}"
  echo "    onchain: ${ONCHAIN_HEX:0:64}"
  exit 1
fi

echo
echo "ERROR: bytecode too short to strip metadata suffix"
exit 1
