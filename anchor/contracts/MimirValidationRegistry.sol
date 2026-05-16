// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "./IEigenLayer.sol";

// -----------------------------------------------------------------------------
// MimirValidationRegistry — ERC-8004 Validation Registry + EigenLayer slashing.
//
// On-chain settlement layer for Mimir envelope provenance. Each entry anchors
// a keccak256 digest of a signed off-chain Provenance Envelope. Third parties
// challenge fraudulent envelopes via revoke(), which both flips the entry's
// revoked flag AND calls the EigenLayer Slasher to reduce the issuer's
// restaked allocation.
//
// EigenLayer integration mode is set at construction:
//   - serviceManager == address(0) && slasher == address(0):
//       Permissionless mode. Anyone may register or revoke. No slashing.
//       Useful for local dev / smoke tests.
//   - both non-zero:
//       AVS mode. Only addresses that IServiceManager.isOperator() returns
//       true for may register. Revoke() additionally calls Slasher.slash()
//       to reduce the issuer's wad allocation.
//
// For Holesky / mainnet deployment, see anchor/README.md for the canonical
// EigenLayer core addresses to pass to the constructor.
// -----------------------------------------------------------------------------

/// @title MimirValidationRegistry
/// @notice ERC-8004 Validation Registry with optional EigenLayer slashing.
contract MimirValidationRegistry {
    // -----------------------------------------------------------------------
    // Storage
    // -----------------------------------------------------------------------

    struct Entry {
        address issuer;     // off-chain signing key / AVS operator address
        uint256 expiry;     // unix timestamp; 0 = never expires
        bool    revoked;    // true once a fraud proof has been submitted
        bool    exists;     // guard against zero-value false-negatives
    }

    /// @dev envelopeDigest => Entry
    mapping(bytes32 => Entry) private _entries;

    /// @notice Address of the EigenLayer ServiceManager (or address(0) in
    ///         permissionless mode).
    IServiceManager public immutable serviceManager;

    /// @notice Address of the EigenLayer Slasher / AllocationManager (or
    ///         address(0) in permissionless mode).
    ISlasher public immutable slasher;

    /// @notice Fraction of operator allocation to slash on each accepted
    ///         fraud proof, in WAD (1e18 = 100%). Defaults to 10% (1e17).
    uint256 public immutable slashWad;

    // -----------------------------------------------------------------------
    // Events
    // -----------------------------------------------------------------------

    event Registered(
        bytes32 indexed envelopeDigest,
        address indexed issuer,
        uint256         expiry
    );

    event Revoked(
        bytes32 indexed envelopeDigest,
        address indexed revokedBy,
        uint256         proofLength
    );

    /// @notice Emitted when the slasher is successfully invoked. The
    ///         off-chain dispute system listens for this to update operator
    ///         reputation scores.
    event SlashTriggered(
        bytes32 indexed envelopeDigest,
        address indexed issuer,
        uint256         wadSlashed,
        bytes32         reasonHash
    );

    // -----------------------------------------------------------------------
    // Errors
    // -----------------------------------------------------------------------

    error DigestAlreadyRegistered(bytes32 envelopeDigest);
    error DigestNotFound(bytes32 envelopeDigest);
    error AlreadyRevoked(bytes32 envelopeDigest);
    error NotAnOperator(address caller);
    error IssuerMustBeCaller(address caller, address issuer);

    // -----------------------------------------------------------------------
    // Constructor
    // -----------------------------------------------------------------------

    /// @param _serviceManager  EigenLayer ServiceManager (or address(0) for
    ///                          permissionless dev mode).
    /// @param _slasher         EigenLayer Slasher / AllocationManager
    ///                          (or address(0) for no-slashing dev mode).
    /// @param _slashWad        Fraction of allocation to slash per fraud
    ///                          proof, in WAD. Pass 0 to use the default 1e17 (10%).
    constructor(IServiceManager _serviceManager, ISlasher _slasher, uint256 _slashWad) {
        // The two AVS handles must be set together — half-configuration would
        // produce confusing security properties.
        require(
            (address(_serviceManager) == address(0)) == (address(_slasher) == address(0)),
            "registry: serviceManager and slasher must be set together"
        );
        serviceManager = _serviceManager;
        slasher        = _slasher;
        slashWad       = _slashWad == 0 ? 1e17 : _slashWad;
    }

    /// @notice Returns true when this contract is configured for AVS mode
    ///         (operator gating + slashing on revoke).
    function avsModeEnabled() public view returns (bool) {
        return address(serviceManager) != address(0);
    }

    // -----------------------------------------------------------------------
    // Write paths
    // -----------------------------------------------------------------------

    /// @notice Anchor a single envelope digest. In AVS mode the caller MUST
    ///         be a registered operator AND must be the `issuer` parameter
    ///         (you can only anchor your own envelopes).
    function register(
        bytes32 envelopeDigest,
        address issuer,
        uint256 expiry
    ) external {
        if (avsModeEnabled()) {
            if (!serviceManager.isOperator(msg.sender)) {
                revert NotAnOperator(msg.sender);
            }
            if (issuer != msg.sender) {
                revert IssuerMustBeCaller(msg.sender, issuer);
            }
        }

        if (_entries[envelopeDigest].exists) {
            revert DigestAlreadyRegistered(envelopeDigest);
        }

        _entries[envelopeDigest] = Entry({
            issuer:  issuer,
            expiry:  expiry,
            revoked: false,
            exists:  true
        });

        emit Registered(envelopeDigest, issuer, expiry);
    }

    /// @notice Batch-register multiple digests in a single transaction.
    ///         Same operator-gating rules as register() apply in AVS mode:
    ///         every entry's issuer must be msg.sender.
    function registerBatch(
        bytes32[] calldata envelopeDigests,
        address[] calldata issuers,
        uint256[] calldata expiries
    ) external {
        uint256 len = envelopeDigests.length;
        require(len == issuers.length && len == expiries.length, "length mismatch");

        bool avs = avsModeEnabled();
        if (avs && !serviceManager.isOperator(msg.sender)) {
            revert NotAnOperator(msg.sender);
        }

        for (uint256 i = 0; i < len; ) {
            address iss = issuers[i];
            if (avs && iss != msg.sender) {
                revert IssuerMustBeCaller(msg.sender, iss);
            }

            bytes32 d = envelopeDigests[i];
            if (_entries[d].exists) {
                revert DigestAlreadyRegistered(d);
            }
            _entries[d] = Entry({
                issuer:  iss,
                expiry:  expiries[i],
                revoked: false,
                exists:  true
            });
            emit Registered(d, iss, expiries[i]);
            unchecked { ++i; }
        }
    }

    /// @notice Submit a fraud proof to revoke an envelope. Anyone may call;
    ///         the slashing decision lives in the Slasher (real EigenLayer
    ///         enforces the AVS authorization there). In AVS mode this also
    ///         triggers a stake slash against the envelope's issuer.
    ///
    /// @param envelopeDigest  digest of the entry to revoke
    /// @param proof           arbitrary bytes; in the production AVS slice this
    ///                        is the canonical replay-artifact reference
    function revoke(bytes32 envelopeDigest, bytes calldata proof) external {
        Entry storage e = _entries[envelopeDigest];
        if (!e.exists)  revert DigestNotFound(envelopeDigest);
        if (e.revoked)  revert AlreadyRevoked(envelopeDigest);

        e.revoked = true;
        address issuer = e.issuer;

        emit Revoked(envelopeDigest, msg.sender, proof.length);

        // Slashing hook — only fires when AVS mode is configured.
        if (avsModeEnabled()) {
            bytes32 reasonHash = keccak256(proof);
            slasher.slash(issuer, slashWad, reasonHash);
            emit SlashTriggered(envelopeDigest, issuer, slashWad, reasonHash);
        }
    }

    // -----------------------------------------------------------------------
    // Read paths
    // -----------------------------------------------------------------------

    function verify(bytes32 envelopeDigest)
        external
        view
        returns (address issuer, uint256 expiry, bool revoked)
    {
        Entry storage e = _entries[envelopeDigest];
        return (e.issuer, e.expiry, e.revoked);
    }

    function exists(bytes32 envelopeDigest) external view returns (bool) {
        return _entries[envelopeDigest].exists;
    }

    function isValid(bytes32 envelopeDigest) external view returns (bool) {
        Entry storage e = _entries[envelopeDigest];
        if (!e.exists || e.revoked) return false;
        if (e.expiry != 0 && block.timestamp > e.expiry) return false;
        return true;
    }
}
