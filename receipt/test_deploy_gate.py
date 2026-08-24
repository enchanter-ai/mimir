"""Tests for the off-chain quality-gated receipt path (receipt/deploy_gate.py).

Off-chain only: these tests exercise the gate predicate, the sources[]
population, and the Ed25519 signer. They spin up NO service and touch NO chain.

Run:  python -m pytest receipt/test_deploy_gate.py -v
"""
from __future__ import annotations

import copy
import json
from pathlib import Path

import pytest

from receipt.deploy_gate import (
    SIGMA_MAX,
    DeployGateError,
    SigningKey,
    build_sources,
    evaluate_gate,
    generate_receipt,
    verify_receipt,
)

SAMPLES = Path(__file__).parent / "samples"
# Deterministic 32-byte seed so signatures are reproducible across runs.
SEED = bytes(range(32))

REQUEST = {"name": "fetch_document", "arguments": {"url": "https://arxiv.org/abs/1706.03762"}}
RESULT = {"tool_use_id": "tu_test_001", "content": [{"type": "text", "text": "Attention Is All You Need."}]}

COMMON = dict(
    tool_id="did:web:example.com:tools:fetch-document",
    tool_version="1.0.0",
    invoked_at="2026-08-24T12:00:00Z",
    tool_call_id="tu_test_001",
)


def _deploy_verdict() -> dict:
    return json.loads((SAMPLES / "verdict-deploy.json").read_text("utf-8"))


def _hold_verdict() -> dict:
    return json.loads((SAMPLES / "verdict-hold.json").read_text("utf-8"))


# ─── Gate predicate ──────────────────────────────────────────────────────────


def test_passing_verdict_meets_bar():
    d = evaluate_gate(_deploy_verdict())
    assert d.passed, d.reasons
    assert d.sigma < SIGMA_MAX
    assert d.assertions_passed == 8


def test_hold_verdict_fails_bar_with_reasons():
    d = evaluate_gate(_hold_verdict())
    assert not d.passed
    # structure axis is below the 7.0 floor, overall below 9.0, sigma too high,
    # and one assertion fails — several independent reasons.
    joined = " ".join(d.reasons)
    assert "axis_below_floor" in joined
    assert "assertions_failed" in joined


def test_none_verdict_refused():
    assert not evaluate_gate(None).passed


@pytest.mark.parametrize(
    "mutate,reason_key",
    [
        (lambda v: v.update(sigma=0.45), "sigma_too_high"),  # not strictly < 0.45
        (lambda v: v.update(sigma=0.60), "sigma_too_high"),
        (lambda v: v.update(overall=8.99), "overall_too_low"),
        (lambda v: v["axes"][0].update(score=6.9), "axis_below_floor"),
        (lambda v: v["assertions"][3].update(passed=False), "assertions_failed"),
        (lambda v: v.__setitem__("axes", v["axes"][:4]), "axes_count"),
        (lambda v: v.__setitem__("assertions", v["assertions"][:7]), "assertions_count"),
    ],
)
def test_each_bar_component_is_enforced(mutate, reason_key):
    v = _deploy_verdict()
    mutate(v)
    d = evaluate_gate(v)
    assert not d.passed
    assert any(reason_key in r for r in d.reasons), d.reasons


def test_deploy_label_but_failing_numbers_is_refused():
    # A verdict claiming DEPLOY whose numbers fail the strict bar must be refused
    # — the gate does not trust the label.
    v = _deploy_verdict()
    v["sigma"] = 0.9
    assert v["verdict"] == "DEPLOY"
    assert not evaluate_gate(v).passed


def test_label_mismatch_when_numbers_pass_but_label_not_deploy():
    v = _deploy_verdict()
    v["verdict"] = "HOLD"  # numbers still clear the bar
    d = evaluate_gate(v)
    assert not d.passed
    assert any("verdict_label_mismatch" in r for r in d.reasons)


# ─── sources[] population from the eval ──────────────────────────────────────


def test_sources_populated_from_eval_provenance():
    sources = build_sources(_deploy_verdict())
    # one summary + one per axis (5)
    assert len(sources) == 6
    summary = sources[0]
    assert summary["type"] == "x-wixie-eval"
    assert summary["hash"].startswith("sha-256:")
    assert summary["detail"]["model_used"] == "claude-sonnet-4-6"
    assert len(summary["detail"]["assertions"]) == 8
    axis_sources = sources[1:]
    assert all(s["type"] == "x-wixie-eval-axis" for s in axis_sources)
    assert {s["detail"]["axis"] for s in axis_sources} == {
        "clarity", "specificity", "faithfulness_to_source", "safety", "structure",
    }
    # no stub placeholder remains
    assert all("stub:" not in json.dumps(s) for s in sources)


# ─── End-to-end: passing verdict -> signed receipt with sources ──────────────


def test_passing_verdict_produces_signed_receipt_with_sources():
    key = SigningKey.from_seed(SEED)
    receipt = generate_receipt(verdict=_deploy_verdict(), **_gen_args(key))
    # sources populated from the eval, not a stub
    assert len(receipt["sources"]) == 6
    assert receipt["sources"][0]["type"] == "x-wixie-eval"
    # signature present and verifies
    assert receipt["signature"]["value"]
    assert verify_receipt(receipt, key.public_key_b64url())
    # digests are spec-formatted
    assert receipt["request_digest"].startswith("sha-256:")
    assert receipt["result_digest"].startswith("sha-256:")


def test_tampered_receipt_fails_verification():
    key = SigningKey.from_seed(SEED)
    receipt = generate_receipt(verdict=_deploy_verdict(), **_gen_args(key))
    tampered = copy.deepcopy(receipt)
    tampered["sources"][0]["detail"]["overall"] = 1.0
    assert not verify_receipt(tampered, key.public_key_b64url())


# ─── End-to-end: failing / missing verdict -> refusal ────────────────────────


def test_failing_verdict_refuses_receipt():
    key = SigningKey.from_seed(SEED)
    with pytest.raises(DeployGateError) as exc:
        generate_receipt(verdict=_hold_verdict(), **_gen_args(key))
    assert exc.value.reasons  # carries machine-readable reasons


def test_missing_verdict_file_refuses_receipt():
    key = SigningKey.from_seed(SEED)
    with pytest.raises(DeployGateError):
        generate_receipt(verdict=str(SAMPLES / "does-not-exist.json"), **_gen_args(key))


def test_refusal_produces_no_receipt_object():
    key = SigningKey.from_seed(SEED)
    produced = None
    try:
        produced = generate_receipt(verdict=_hold_verdict(), **_gen_args(key))
    except DeployGateError:
        pass
    assert produced is None


def _gen_args(key: SigningKey) -> dict:
    return dict(request=REQUEST, result=RESULT, signing_key=key, **COMMON)


# ─── CLI smoke ───────────────────────────────────────────────────────────────


def test_cli_refuses_hold_with_nonzero_exit():
    from receipt.deploy_gate import _main

    rc = _main(["--verdict", str(SAMPLES / "verdict-hold.json")])
    assert rc == 1


def test_cli_signs_deploy_with_zero_exit():
    from receipt.deploy_gate import _main

    rc = _main(["--verdict", str(SAMPLES / "verdict-deploy.json")])
    assert rc == 0
