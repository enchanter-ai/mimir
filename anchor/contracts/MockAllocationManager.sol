// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "./EigenLayerSlasherAdapter.sol";

/// @title MockAllocationManager
/// @notice Test double for EigenLayer v2 AllocationManager — implements the
///         exact `slash(SlashingParams)` ABI so EigenLayerSlasherAdapter can
///         exercise the real call path in tests.
///
/// @dev    DO NOT deploy to a real network. There's no access control on slash.
///         Production replacement: the canonical AllocationManager address
///         for the target network (mainnet / Hoodi / etc.) per
///         https://github.com/Layr-Labs/eigenlayer-contracts/blob/dev/script/output
contract MockAllocationManager is IEigenLayerAllocationManager {
    /// @dev operator => cumulative wadSlashed across all slash() calls,
    ///      summed over all per-strategy entries (matching the adapter's
    ///      "single wad per strategy" assumption).
    mapping(address => uint256) private _totalSlashedPerOperator;

    /// @notice Number of strategies most-recently passed to slash() — useful
    ///         for tests asserting the adapter packed the array correctly.
    uint256 public lastStrategyCount;

    /// @notice Most-recent operatorSetId — useful for tests asserting the
    ///         adapter pinned the right set.
    uint32 public lastOperatorSetId;

    /// @notice Most-recent description string — useful for tests asserting
    ///         the adapter encoded reasonHash correctly.
    string public lastDescription;

    /// @notice Emitted on every slash call. Mirrors what real EigenLayer
    ///         emits (flattened) so test harnesses can listen.
    event SlashRecorded(
        address indexed operator,
        uint32  indexed operatorSetId,
        uint256[]       wadsToSlash,
        string          description
    );

    function slash(SlashingParams calldata params) external override {
        require(params.operator != address(0),                 "AM: operator zero");
        require(params.strategies.length > 0,                  "AM: empty strategies");
        require(params.strategies.length == params.wadsToSlash.length, "AM: array len mismatch");

        // Sum the per-strategy wads. The adapter passes the same wad per
        // strategy, so we use the first entry; we still validate all entries
        // are equal so a future buggy adapter is loud, not silent.
        uint256 wad = params.wadsToSlash[0];
        for (uint256 i = 1; i < params.wadsToSlash.length; ) {
            require(params.wadsToSlash[i] == wad, "AM: non-uniform wads (adapter bug?)");
            unchecked { ++i; }
        }
        require(wad > 0,        "AM: wad zero");
        require(wad <= 1e18,    "AM: wad > 1e18");

        _totalSlashedPerOperator[params.operator] += wad;
        lastStrategyCount = params.strategies.length;
        lastOperatorSetId = params.operatorSetId;
        lastDescription   = params.description;

        emit SlashRecorded(params.operator, params.operatorSetId, params.wadsToSlash, params.description);
    }

    function totalSlashedRecorded(address operator) external view returns (uint256) {
        return _totalSlashedPerOperator[operator];
    }
}
