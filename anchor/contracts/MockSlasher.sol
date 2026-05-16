// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "./IEigenLayer.sol";

/// @title MockSlasher
/// @notice Test double for EigenLayer's AllocationManager.slash entry point.
///         Records slash events + cumulative wadSlashed per operator.
///
/// @dev    DO NOT deploy to a real network. There is no authentication on
///         `slash()` in this mock — that responsibility lives in the calling
///         contract (MimirValidationRegistry). The real AllocationManager
///         enforces that only registered AVS service-managers may invoke slash.
contract MockSlasher is ISlasher {
    /// @dev operator => cumulative wadSlashed across all slash() calls
    mapping(address => uint256) private _totalSlashed;

    /// @notice Emitted on every slash() invocation. Mirrors what real
    ///         EigenLayer AllocationManager emits, in flattened form.
    event Slashed(
        address indexed operator,
        address indexed slasher,
        uint256         wadSlashed,
        bytes32         reasonHash
    );

    function slash(address operator, uint256 wadSlashed, bytes32 reasonHash) external override {
        // Real EigenLayer caps wadSlashed at WAD (1e18). We replicate the cap
        // to make sure callers can't accidentally over-slash in tests.
        require(wadSlashed > 0,        "slasher: wadSlashed must be > 0");
        require(wadSlashed <= 1e18,    "slasher: wadSlashed must be <= 1e18");
        require(operator != address(0), "slasher: operator zero");

        _totalSlashed[operator] += wadSlashed;
        emit Slashed(operator, msg.sender, wadSlashed, reasonHash);
    }

    function totalSlashed(address operator) external view override returns (uint256) {
        return _totalSlashed[operator];
    }
}
