"""etherscan-verify.py — programmatic Etherscan source verification.

Submits the on-chain contract for source verification via Etherscan's
HTTP API. Once verified, anyone visiting the contract page on
etherscan.io sees the Solidity source, ABI, and decoded constructor
args, and can confirm the source-to-bytecode mapping themselves.

Note: we already prove source-to-bytecode strict equality locally via
`scripts/verify_build.py`. Etherscan verification is the *Etherscan*
side of that — a UI convenience for third parties + a community
trust signal.

Requirements:
  - ETHERSCAN_API_KEY env var (free signup at etherscan.io)
  - The contract source is at anchor/contracts/MimirValidationRegistry.sol
    + IEigenLayer.sol (single-file flattened version produced below)

Usage:
  python scripts/etherscan-verify.py \\
      --network sepolia \\
      --contract 0xEbdAa5a99DFde9a4A603aacfE1cC5AcFc0DA4117 \\
      --constructor-args 0x000...000  # ABI-encoded (use deploy.go log)

Exit codes: 0 verified; 1 verification failed; 2 input/api error.
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import time
from pathlib import Path

try:
    import requests
except ImportError:
    import subprocess
    subprocess.run([sys.executable, "-m", "pip", "install", "requests"], check=True)
    import requests


# Etherscan unified v2 API (chain-multiplexed); per-network legacy v1
# endpoints continue to work but the v2 one is forward-compatible.
API_BASE = "https://api.etherscan.io/v2/api"

# chain_id by network slug. Mainnet/Sepolia/Holesky only — extend if needed.
CHAIN_IDS = {
    "mainnet": 1,
    "sepolia": 11155111,
    "holesky": 17000,
}

COMPILER_VERSION = "v0.8.20+commit.a1b79de6"
OPTIMIZER_RUNS = 200
LICENSE_TYPE = 12  # SPDX-License-Identifier: MIT (Etherscan code 12)


def flatten_source() -> str:
    """Concatenate the contract sources into a single Solidity file in the
    order Solidity expects — interface first, then the contract that
    imports it. Etherscan's single-file mode requires the imports to be
    inlined; we vendor IEigenLayer.sol's content + strip the import line
    from the registry."""
    root = Path(__file__).resolve().parent.parent / "anchor" / "contracts"
    iel = (root / "IEigenLayer.sol").read_text(encoding="utf-8")
    reg = (root / "MimirValidationRegistry.sol").read_text(encoding="utf-8")

    # Strip the import line so the flattened file compiles standalone.
    reg = "\n".join(
        line for line in reg.splitlines()
        if not line.strip().startswith('import "./IEigenLayer.sol"')
    )

    # Etherscan requires exactly one SPDX header in the flattened source;
    # drop the second one (from the registry) so only IEigenLayer's header
    # remains.
    reg = "\n".join(
        line for line in reg.splitlines()
        if "SPDX-License-Identifier" not in line
    )

    # Same for the pragma — keep only the first one.
    reg = "\n".join(
        line for line in reg.splitlines()
        if not line.strip().startswith("pragma solidity")
    )

    return iel.rstrip() + "\n\n// ----- contract -----\n\n" + reg.lstrip()


def submit_verification(api_key: str, chain_id: int, contract_address: str,
                        source: str, constructor_args: str) -> str:
    """POST the verification request; return the Etherscan guid."""
    params = {
        "chainid":             str(chain_id),
        "module":              "contract",
        "action":              "verifysourcecode",
        "apikey":              api_key,
        "contractaddress":     contract_address,
        "sourceCode":          source,
        "codeformat":          "solidity-single-file",
        "contractname":        "MimirValidationRegistry",
        "compilerversion":     COMPILER_VERSION,
        "optimizationUsed":    "1",
        "runs":                str(OPTIMIZER_RUNS),
        "constructorArguements": constructor_args.lstrip("0x"),  # API spelling — typo in their docs
        "evmversion":          "",  # default (paris for 0.8.20)
        "licenseType":         str(LICENSE_TYPE),
    }
    r = requests.post(API_BASE, data=params, timeout=30)
    r.raise_for_status()
    body = r.json()
    if body.get("status") != "1":
        sys.exit(f"ERROR submitting verification: {body.get('result') or body}")
    guid = body["result"]
    print(f"  submitted verification request: guid={guid}")
    return guid


def poll_verification(api_key: str, chain_id: int, guid: str, max_wait_s: int = 120) -> bool:
    """Poll Etherscan until the verification finishes; return True if successful."""
    deadline = time.time() + max_wait_s
    while time.time() < deadline:
        params = {
            "chainid": str(chain_id),
            "module":  "contract",
            "action":  "checkverifystatus",
            "apikey":  api_key,
            "guid":    guid,
        }
        r = requests.get(API_BASE, params=params, timeout=30)
        body = r.json()
        result = body.get("result", "")
        if "Pending" in result or "Pending in queue" in result:
            print(f"  pending... ({result})")
            time.sleep(5)
            continue
        if body.get("status") == "1":
            print(f"  [OK] {result}")
            return True
        print(f"  [FAIL] {result}")
        return False
    print(f"  TIMEOUT after {max_wait_s}s")
    return False


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--network", required=True, choices=sorted(CHAIN_IDS.keys()))
    p.add_argument("--contract", required=True, help="Deployed contract address (0x...)")
    p.add_argument("--constructor-args", required=True,
                   help="ABI-encoded constructor args as hex (no 0x or with 0x; the script strips it)")
    args = p.parse_args()

    api_key = os.environ.get("ETHERSCAN_API_KEY")
    if not api_key:
        sys.exit("ERROR: ETHERSCAN_API_KEY env var is required (free at https://etherscan.io/myapikey)")

    chain_id = CHAIN_IDS[args.network]
    print(f"==== Etherscan source verification ====")
    print(f"  network          : {args.network} (chain_id {chain_id})")
    print(f"  contract         : {args.contract}")
    print(f"  compiler         : {COMPILER_VERSION}")
    print(f"  optimizer runs   : {OPTIMIZER_RUNS}")
    print(f"  constructor args : {args.constructor_args[:18]}...")

    print()
    print("[1/3] Flatten source (IEigenLayer.sol + MimirValidationRegistry.sol)")
    flat = flatten_source()
    print(f"  flattened source : {len(flat)} chars")

    print()
    print("[2/3] Submit verification request to Etherscan")
    guid = submit_verification(api_key, chain_id, args.contract, flat, args.constructor_args)

    print()
    print("[3/3] Poll for completion")
    ok = poll_verification(api_key, chain_id, guid)
    if ok:
        print()
        print(f"==== VERIFIED ====")
        print(f"  https://{'' if args.network == 'mainnet' else args.network + '.'}etherscan.io/address/{args.contract}#code")
        return 0
    return 1


if __name__ == "__main__":
    sys.exit(main())
