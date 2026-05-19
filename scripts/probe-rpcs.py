"""probe-rpcs.py — sanity-test a list of EVM JSON-RPC endpoints.

Useful when a public RPC starts misbehaving (as Holesky did 2026-05-16)
and you need to find a working alternative quickly.

For each endpoint, the probe does:
  1. eth_chainId        — confirms the endpoint speaks JSON-RPC + returns
                          the expected chain.
  2. eth_blockNumber    — confirms the endpoint sees recent state.
  3. eth_getBalance(0x0) — confirms read calls work.
  4. Latency: median of 3 calls.

Exit codes: 0 at least one endpoint passed; 1 none passed.

Usage:
  python scripts/probe-rpcs.py --chain sepolia
  python scripts/probe-rpcs.py --chain holesky
"""
from __future__ import annotations

import argparse
import json
import statistics
import sys
import time

import requests

# Lists curated 2026-05. Update as providers come and go.
ENDPOINTS = {
    "sepolia": [
        "https://ethereum-sepolia.publicnode.com",
        "https://rpc.sepolia.org",
        "https://eth-sepolia.public.blastapi.io",
        "https://sepolia.gateway.tenderly.co",
        "https://1rpc.io/sepolia",
    ],
    "holesky": [
        # Holesky deprecated by Ethereum Foundation as of 2025; left here for
        # historical probes. Use Hoodi for new testnet work.
        "https://ethereum-holesky.publicnode.com",
        "https://ethereum-holesky-rpc.publicnode.com",
        "https://1rpc.io/holesky",
        "https://holesky.gateway.tenderly.co",
        "https://rpc.holesky.ethpandaops.io",
    ],
    "hoodi": [
        # Hoodi: Ethereum Foundation's successor to Holesky (launched 2025;
        # positioned as the EigenLayer-friendly testnet).
        "https://ethereum-hoodi.publicnode.com",
        "https://ethereum-hoodi-rpc.publicnode.com",
        "https://rpc.hoodi.ethpandaops.io",
    ],
    "mainnet": [
        "https://ethereum.publicnode.com",
        "https://eth.llamarpc.com",
        "https://rpc.ankr.com/eth",
        "https://cloudflare-eth.com",
        "https://eth.drpc.org",
    ],
}

EXPECTED_CHAIN_ID = {
    "sepolia": 11155111,
    "holesky": 17000,
    "hoodi":   560048,
    "mainnet": 1,
}


def rpc(url: str, method: str, params: list, timeout: float = 5.0):
    body = {"jsonrpc": "2.0", "method": method, "params": params, "id": 1}
    t0 = time.time()
    try:
        r = requests.post(url, json=body, timeout=timeout)
    except requests.RequestException as e:
        return None, time.time() - t0, f"network: {e}"
    elapsed = time.time() - t0
    if r.status_code != 200:
        return None, elapsed, f"HTTP {r.status_code}: {r.text[:100]}"
    try:
        data = r.json()
    except Exception as e:
        return None, elapsed, f"JSON parse: {e}: {r.text[:100]}"
    if "error" in data:
        return None, elapsed, f"RPC error: {data['error']}"
    return data.get("result"), elapsed, None


def probe(url: str, expected_chain: int) -> dict:
    out = {"url": url, "ok": False, "errors": []}

    # eth_chainId
    res, _, err = rpc(url, "eth_chainId", [])
    if err:
        out["errors"].append(f"chainId: {err}")
        return out
    chain_id = int(res, 16) if isinstance(res, str) and res.startswith("0x") else None
    if chain_id != expected_chain:
        out["errors"].append(f"chainId: got {chain_id}, expected {expected_chain}")
        return out
    out["chain_id"] = chain_id

    # eth_blockNumber
    res, _, err = rpc(url, "eth_blockNumber", [])
    if err:
        out["errors"].append(f"blockNumber: {err}")
        return out
    block = int(res, 16) if isinstance(res, str) and res.startswith("0x") else None
    out["latest_block"] = block

    # eth_getBalance — read call
    res, _, err = rpc(url, "eth_getBalance", ["0x0000000000000000000000000000000000000000", "latest"])
    if err:
        out["errors"].append(f"getBalance: {err}")
        return out

    # 3-call latency
    samples = []
    for _ in range(3):
        _, e, _ = rpc(url, "eth_chainId", [])
        samples.append(e)
    out["median_latency_ms"] = round(statistics.median(samples) * 1000.0, 1)
    out["ok"] = True
    return out


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--chain", required=True, choices=sorted(ENDPOINTS.keys()))
    args = p.parse_args()

    eps = ENDPOINTS[args.chain]
    expected = EXPECTED_CHAIN_ID[args.chain]
    print(f"==== EVM RPC probe ({args.chain}, expected chain_id {expected}) ====")
    print(f"  probing {len(eps)} endpoint(s)\n")

    results = []
    for url in eps:
        result = probe(url, expected)
        results.append(result)
        if result["ok"]:
            print(f"  [OK]  {url:55s}  block {result['latest_block']}  median {result['median_latency_ms']}ms")
        else:
            print(f"  [FAIL] {url:55s}  {result['errors'][0] if result['errors'] else 'unknown'}")

    print()
    working = [r for r in results if r["ok"]]
    print(f"summary: {len(working)}/{len(results)} working")
    if working:
        # Sort by median latency ascending so the recommendation is clear.
        best = sorted(working, key=lambda r: r["median_latency_ms"])[0]
        print(f"\nrecommended (lowest median latency): {best['url']}  ({best['median_latency_ms']}ms)")
        return 0
    print("\nNO working endpoint found.")
    print("Options:")
    print("  - sign up for an Alchemy or Infura account (free tier covers most needs)")
    print("  - wait a few minutes and retry (transient public-RPC outages are common)")
    return 1


if __name__ == "__main__":
    sys.exit(main())
