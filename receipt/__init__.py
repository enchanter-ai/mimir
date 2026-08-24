"""Off-chain, quality-gated receipt generation for Mimir (POC signer path).

See :mod:`receipt.deploy_gate` for the gate predicate, the sources[] population,
and the signed-receipt generator.
"""
from .deploy_gate import (
    AXIS_MIN,
    OVERALL_MIN,
    SIGMA_MAX,
    DeployGateError,
    GateDecision,
    SigningKey,
    build_sources,
    evaluate_gate,
    generate_receipt,
    load_verdict,
    verify_receipt,
)

__all__ = [
    "AXIS_MIN",
    "OVERALL_MIN",
    "SIGMA_MAX",
    "DeployGateError",
    "GateDecision",
    "SigningKey",
    "build_sources",
    "evaluate_gate",
    "generate_receipt",
    "load_verdict",
    "verify_receipt",
]
