"""verify_build.py — strict on-chain bytecode equality with immutable masking.

This is the verifier that auditors / third parties run to confirm the
deployed contract IS the source at this commit. The check is:

  1. Recompile contracts/ via solc-js (handled by compile.js — caller invokes it).
  2. Read evm.deployedBytecode.object → expected runtime bytecode.
  3. Read evm.deployedBytecode.immutableReferences → positions of constructor
     immutables (e.g. slashWad, serviceManager, slasher addresses), which the
     compiler emits as zero in the local bytecode but the deployer inlines at
     construction time.
  4. eth_getCode(contractAddress) → actual runtime bytecode on-chain.
  5. Mask out the immutable byte ranges in BOTH bytecodes (replace with 0x00).
  6. Strip the trailing CBOR metadata (last 43 bytes — may differ between
     compile environments without changing semantic behaviour).
  7. Compare masked + stripped versions for byte-equality.

If equal → the on-chain code matches the source. If not → the deployment was
either compiled from a different source or tampered with.

Usage:
    python scripts/verify_build.py \\
        --contract 0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117 \\
        --rpc     https://ethereum-sepolia.publicnode.com

Exit codes:
    0    bytecode matches (including immutable masking + metadata strip)
    1    bytecode mismatch — DO NOT TRUST THIS DEPLOYMENT
    2    bad input / RPC error
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

try:
    import requests
except ImportError:
    import subprocess
    subprocess.run([sys.executable, "-m", "pip", "install", "requests"], check=True)
    import requests


METADATA_SUFFIX_BYTES = 43  # CBOR-encoded metadata Solidity appends to deployed bytecode


def load_artifact(name: str) -> tuple[bytes, dict[str, list[dict[str, int]]]]:
    root = Path(__file__).resolve().parent.parent
    bin_path = root / "anchor" / "go" / "abi" / f"{name}.runtime.bin"
    imm_path = root / "anchor" / "go" / "abi" / f"{name}.immutables.json"

    if not bin_path.exists():
        sys.exit(f"ERROR: missing {bin_path} — run `cd anchor && node compile.js` first.")

    hex_str = bin_path.read_text().strip()
    if hex_str.startswith("0x"):
        hex_str = hex_str[2:]
    code = bytes.fromhex(hex_str)

    immutables: dict[str, list[dict[str, int]]] = {}
    if imm_path.exists():
        immutables = json.loads(imm_path.read_text())
    return code, immutables


def fetch_onchain_code(rpc: str, address: str) -> bytes:
    body = {"jsonrpc": "2.0", "method": "eth_getCode", "params": [address, "latest"], "id": 1}
    r = requests.post(rpc, json=body, timeout=15)
    r.raise_for_status()
    data = r.json()
    if "result" not in data or data["result"] in ("0x", None):
        sys.exit(f"ERROR: no code at {address}")
    hex_str = data["result"]
    if hex_str.startswith("0x"):
        hex_str = hex_str[2:]
    return bytes.fromhex(hex_str)


def mask_immutables(code: bytes, immutables: dict[str, list[dict[str, int]]]) -> tuple[bytes, int]:
    """Return code with every immutable byte range zeroed. Also returns the
    total number of bytes masked (for diagnostics)."""
    out = bytearray(code)
    total_masked = 0
    for ranges in immutables.values():
        for r in ranges:
            start = r["start"]
            length = r["length"]
            if start + length > len(out):
                # Sanity check — should never happen if compile + on-chain
                # lengths agree.
                continue
            for i in range(start, start + length):
                out[i] = 0
            total_masked += length
    return bytes(out), total_masked


def strip_metadata(code: bytes, n: int = METADATA_SUFFIX_BYTES) -> bytes:
    if len(code) <= n:
        return code
    return code[: len(code) - n]


def diagnose_diff(a: bytes, b: bytes, label_a: str, label_b: str) -> None:
    n = min(len(a), len(b))
    for i in range(n):
        if a[i] != b[i]:
            ctx = 16
            lo = max(0, i - ctx)
            hi = min(n, i + ctx)
            print(f"  first diff at byte {i}:")
            print(f"    {label_a} [{lo}:{hi}]: {a[lo:hi].hex()}")
            print(f"    {label_b} [{lo}:{hi}]: {b[lo:hi].hex()}")
            return
    print(f"  prefix bytes match; length diff: {label_a}={len(a)} vs {label_b}={len(b)}")


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--contract", required=True, help="Deployed contract address")
    p.add_argument("--rpc", required=True, help="EVM JSON-RPC endpoint")
    p.add_argument("--name", default="MimirValidationRegistry",
                   help="Contract name (must match anchor/go/abi/<name>.runtime.bin)")
    args = p.parse_args()

    print("==== Mimir strict bytecode verifier ====")
    print(f"  contract : {args.contract}")
    print(f"  rpc      : {args.rpc}")
    print(f"  name     : {args.name}")

    local, immutables = load_artifact(args.name)
    onchain = fetch_onchain_code(args.rpc, args.contract)

    print(f"  local runtime bytecode    : {len(local)} bytes")
    print(f"  on-chain runtime bytecode : {len(onchain)} bytes")
    print(f"  declared immutables       : {sum(len(v) for v in immutables.values())} position(s) across {len(immutables)} state var(s)")

    if len(local) != len(onchain):
        print(f"\n[FAIL] length mismatch — bytecode lengths must match for a valid deploy")
        return 1

    # Mask immutables in both, then strip metadata suffix.
    masked_local, masked_local_count = mask_immutables(local, immutables)
    masked_onchain, _ = mask_immutables(onchain, immutables)
    print(f"  masked {masked_local_count} bytes of immutable storage in both copies")

    stripped_local = strip_metadata(masked_local)
    stripped_onchain = strip_metadata(masked_onchain)
    print(f"  stripped {METADATA_SUFFIX_BYTES} bytes of trailing metadata suffix from both")

    # Compare.
    if stripped_local == stripped_onchain:
        print("\n[OK] STRICT BYTECODE MATCH (after immutable masking + metadata strip)")
        print(f"     deployed bytecode IS the source at this commit.")
        # Also report whether the unmasked, metadata-included diff is zero.
        if local == onchain:
            print(f"     (raw bytecode identical too — no immutables took non-default values)")
        return 0

    print("\n[FAIL] bytecode mismatch")
    diagnose_diff(stripped_local, stripped_onchain, "local", "onchain")
    return 1


if __name__ == "__main__":
    sys.exit(main())
