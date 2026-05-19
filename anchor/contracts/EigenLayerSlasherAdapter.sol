// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "./IEigenLayer.sol";

// -----------------------------------------------------------------------------
// EigenLayerSlasherAdapter — bridge from Mimir's narrow ISlasher interface
//                            to EigenLayer v2's AllocationManager.slash().
//
// Why this exists
// ---------------
// Mimir's contracts target a small, stable, restaking-agnostic ISlasher shape:
//
//   slash(address operator, uint256 wadSlashed, bytes32 reasonHash)
//
// Real EigenLayer v2 AllocationManager.slash takes a richer struct that
// includes operatorSetId, an array of IStrategy contracts, an array of
// per-strategy wadsToSlash values, and a free-text description:
//
//   slash(SlashingParams { operator, operatorSetId, strategies[],
//                          wadsToSlash[], description })
//
// This adapter is the thin shim operators deploy alongside Mimir's registry
// when they want slashes routed to a real EigenLayer AllocationManager.
// It:
//   - implements Mimir's ISlasher (so MimirValidationRegistry can call it
//     unchanged)
//   - holds the real AllocationManager address + the AVS's operatorSetId +
//     the strategies array as immutable construction-time config
//   - on each slash() call, builds a SlashingParams struct with all wads
//     set to the wadSlashed value, the operator from Mimir, and the
//     description set to hex(reasonHash); then calls AllocationManager.slash
//
// Same pattern can be used for other restaking primitives (Symbiotic, Karak)
// by swapping out the adapter — Mimir's registry stays unchanged.
//
// Threat model addition
// ---------------------
// The adapter must be authorised at the real AllocationManager to call slash.
// In EigenLayer v2 this means the adapter's address must be the AVS's
// ServiceManager (or be authorized by it) — operators handle that in their
// EigenLayer registration flow.
// -----------------------------------------------------------------------------

/// @notice Minimal EigenLayer v2 AllocationManager interface — just the
///         slice the adapter needs. NOT a full vendor — we stick to the
///         `slash` entry point.
interface IEigenLayerAllocationManager {
    /// @notice Slashing parameters per EigenLayer v2 spec.
    /// @param operator        the operator to slash
    /// @param operatorSetId   the AVS-defined operator-set the operator belongs to
    /// @param strategies      list of strategy contracts to slash from
    /// @param wadsToSlash     per-strategy slash fraction in WAD (1e18 = 100%)
    /// @param description     free-text reason (we encode our reasonHash here)
    struct SlashingParams {
        address operator;
        uint32  operatorSetId;
        address[] strategies;   // typed `IStrategy[]` in the real ABI; address[] for the narrow shim
        uint256[] wadsToSlash;
        string  description;
    }

    function slash(SlashingParams calldata params) external;
}


/// @title EigenLayerSlasherAdapter
/// @notice Implements Mimir's ISlasher by translating to EigenLayer v2's
///         AllocationManager.slash(SlashingParams).
contract EigenLayerSlasherAdapter is ISlasher {
    /// @notice The real EigenLayer AllocationManager (or a fork-compatible mock).
    IEigenLayerAllocationManager public immutable allocationManager;

    /// @notice The AVS's operator-set id (per EigenLayer v2 AVS registration).
    uint32 public immutable operatorSetId;

    /// @notice Strategies to slash from. All slashes apply uniformly across
    ///         these — the per-strategy wadsToSlash array is filled with the
    ///         wadSlashed value from Mimir's ISlasher.slash call.
    address[] private _strategies;

    /// @notice Cumulative slashing per operator (mirrors what real
    ///         AllocationManager records, kept locally for ISlasher.totalSlashed
    ///         contract).
    mapping(address => uint256) private _totalSlashed;

    /// @notice Emitted whenever the adapter forwards a slash to AllocationManager.
    event AdapterSlashed(
        address indexed operator,
        uint256         wadSlashed,
        bytes32         reasonHash,
        uint256         strategyCount
    );

    /// @param _allocationManager  the real EigenLayer AllocationManager (or
    ///                            mock with the same SlashingParams ABI)
    /// @param _operatorSetId      this AVS's operator-set id
    /// @param strategies          strategies to slash; cannot be empty
    constructor(
        IEigenLayerAllocationManager _allocationManager,
        uint32 _operatorSetId,
        address[] memory strategies
    ) {
        require(address(_allocationManager) != address(0), "adapter: AM zero");
        require(strategies.length > 0,                     "adapter: strategies empty");
        allocationManager = _allocationManager;
        operatorSetId     = _operatorSetId;
        _strategies       = strategies;
    }

    /// @notice ISlasher.slash entry point — called by MimirValidationRegistry.
    /// Translates Mimir's (operator, wadSlashed, reasonHash) tuple into an
    /// EigenLayer v2 SlashingParams struct with:
    ///   - the same operator
    ///   - this adapter's pinned operatorSetId + strategies
    ///   - wadsToSlash = [wadSlashed, wadSlashed, ...] (one entry per strategy,
    ///     all equal)
    ///   - description = hex-string of reasonHash (32 bytes → 66-char string)
    function slash(address operator, uint256 wadSlashed, bytes32 reasonHash) external override {
        require(operator != address(0),  "adapter: operator zero");
        require(wadSlashed > 0,          "adapter: wad zero");
        require(wadSlashed <= 1e18,      "adapter: wad > 1e18");

        // Build wadsToSlash array — same wad per strategy.
        uint256 n = _strategies.length;
        uint256[] memory wads = new uint256[](n);
        for (uint256 i = 0; i < n; ) {
            wads[i] = wadSlashed;
            unchecked { ++i; }
        }

        IEigenLayerAllocationManager.SlashingParams memory params = IEigenLayerAllocationManager.SlashingParams({
            operator:      operator,
            operatorSetId: operatorSetId,
            strategies:    _strategies,
            wadsToSlash:   wads,
            description:   _bytes32ToHexString(reasonHash)
        });

        allocationManager.slash(params);

        _totalSlashed[operator] += wadSlashed;
        emit AdapterSlashed(operator, wadSlashed, reasonHash, n);
    }

    function totalSlashed(address operator) external view override returns (uint256) {
        return _totalSlashed[operator];
    }

    /// @notice Read-only view of the strategies this adapter slashes against.
    function strategies() external view returns (address[] memory) {
        return _strategies;
    }

    // --- internal helpers ---------------------------------------------------

    /// @dev Convert a bytes32 to its lowercase hex string with 0x prefix.
    function _bytes32ToHexString(bytes32 b) internal pure returns (string memory) {
        bytes memory out = new bytes(66);
        out[0] = "0"; out[1] = "x";
        bytes memory alphabet = "0123456789abcdef";
        for (uint256 i = 0; i < 32; ) {
            out[2 + i*2]     = alphabet[uint8(b[i] >> 4)];
            out[2 + i*2 + 1] = alphabet[uint8(b[i] & 0x0f)];
            unchecked { ++i; }
        }
        return string(out);
    }
}
