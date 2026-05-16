// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

// -----------------------------------------------------------------------------
// Minimal EigenLayer interfaces — the slice MimirValidationRegistry depends on.
//
// We intentionally do NOT vendor the full EigenLayer contract suite here:
//   - the live deployment addresses are operator-step concerns
//   - the real ABIs evolve faster than we want to track in this repo
//   - testing the wiring is what matters; the wiring is what these interfaces define
//
// Real EigenLayer mapping (as of v2 / 2026-Q1):
//   IServiceManager  → eigenlayer-contracts/src/ServiceManagerBase.sol
//                      (or the AVS's project-specific subclass).
//   ISlasher         → eigenlayer-contracts/src/AllocationManager.sol
//                      (the v2 "slash()" entry point that reduces operator
//                      magnitude by a wadSlashed fraction).
//
// For Holesky / mainnet deployment, replace the mock addresses passed to
// MimirValidationRegistry's constructor with the canonical EigenLayer core
// addresses for the target network. See anchor/README.md "Deploy" section.
// -----------------------------------------------------------------------------

/// @title IServiceManager
/// @notice The AVS contract that EigenLayer operators register with.
///         Mimir-side need: query whether an address is an active operator.
interface IServiceManager {
    /// @return true iff `operator` is registered with this AVS and has
    ///         allocated stake to it.
    function isOperator(address operator) external view returns (bool);
}

/// @title ISlasher
/// @notice The EigenLayer entry point that reduces a slashable operator's
///         restaked allocation when a fraud proof is accepted.
///
///         The real EigenLayer v2 entry point is `AllocationManager.slash()`,
///         which takes a `SlashingParams` struct. We use a flattened shape
///         here for clarity: real wiring will adapt at the AVS shim layer.
interface ISlasher {
    /// @notice Slash an operator's allocation for fraud.
    /// @param operator   the address whose stake gets reduced
    /// @param wadSlashed fraction of allocated stake to remove,
    ///                   expressed in WAD (1e18 = 100%, 1e17 = 10%)
    /// @param reasonHash 32-byte commitment to the off-chain reason (e.g.,
    ///                   keccak256 of the replay-artifact reference)
    function slash(address operator, uint256 wadSlashed, bytes32 reasonHash) external;

    /// @notice View: how much of `operator`'s allocation has been slashed
    ///         cumulatively (sum of wadSlashed across all slash() calls).
    function totalSlashed(address operator) external view returns (uint256);
}
