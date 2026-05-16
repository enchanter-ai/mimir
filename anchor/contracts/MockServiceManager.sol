// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "./IEigenLayer.sol";

/// @title MockServiceManager
/// @notice Test double for EigenLayer's ServiceManagerBase. Operators are
///         registered/deregistered via direct calls; the production version
///         derives operator status from delegated stake + AVS opt-in.
///
/// @dev    DO NOT deploy to a real network. This contract has NO access
///         control on register/deregister — anyone can add or remove operators.
///         Production replacement: pass the real ServiceManager address to
///         MimirValidationRegistry's constructor.
contract MockServiceManager is IServiceManager {
    mapping(address => bool) private _operators;

    event OperatorRegistered(address indexed operator);
    event OperatorDeregistered(address indexed operator);

    function registerOperator(address operator) external {
        _operators[operator] = true;
        emit OperatorRegistered(operator);
    }

    function deregisterOperator(address operator) external {
        _operators[operator] = false;
        emit OperatorDeregistered(operator);
    }

    function isOperator(address operator) external view override returns (bool) {
        return _operators[operator];
    }
}
