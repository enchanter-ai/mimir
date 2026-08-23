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

    /// @notice The single trusted Slashing-Reporter ECDSA key. A fraud `proof`
    ///         only authorizes a slash when it is an EIP-191 signature by THIS
    ///         address over the canonical slash message (see `_verifySlashProof`).
    ///         Set at construction; in AVS mode it MUST be non-zero. In
    ///         permissionless dev mode it is unused and may be address(0).
    address public immutable slashingReporter;

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

    /// @notice Emitted when an AVS-mode revoke set the revocation flag but did
    ///         NOT slash, because the submitted fraud `proof` was not
    ///         cryptographically verified (see VF-11 / `_verifySlashProof`).
    ///         The revocation itself still took effect; only the economic
    ///         penalty was withheld. The off-chain dispute system can listen
    ///         for this to detect disputes awaiting a verifiable proof.
    event SlashWithheldUnverifiedProof(
        bytes32 indexed envelopeDigest,
        address indexed issuer,
        address indexed reporter,
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
    /// @param _slashingReporter Trusted Slashing-Reporter ECDSA address whose
    ///                          EIP-191 signature over the canonical slash
    ///                          message authorizes a slash. MUST be non-zero in
    ///                          AVS mode; ignored (may be address(0)) in
    ///                          permissionless dev mode.
    constructor(
        IServiceManager _serviceManager,
        ISlasher _slasher,
        uint256 _slashWad,
        address _slashingReporter
    ) {
        // The two AVS handles must be set together — half-configuration would
        // produce confusing security properties.
        require(
            (address(_serviceManager) == address(0)) == (address(_slasher) == address(0)),
            "registry: serviceManager and slasher must be set together"
        );
        // In AVS mode the slash authority MUST be configured — otherwise no
        // proof could ever be verified and slashing would be permanently dead.
        require(
            address(_serviceManager) == address(0) || _slashingReporter != address(0),
            "registry: slashingReporter required in AVS mode"
        );
        serviceManager   = _serviceManager;
        slasher          = _slasher;
        slashWad         = _slashWad == 0 ? 1e17 : _slashWad;
        slashingReporter = _slashingReporter;
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
    ///         revocation itself is permissionless by design (open-challenge
    ///         model) and only sets an on-chain flag — it moves no funds.
    ///
    ///         In AVS mode the economic slash is gated on cryptographic
    ///         verification of `proof` (see `_verifySlashProof`). A revoke
    ///         with an unverified proof still flips the flag but does NOT
    ///         slash. This is the VF-11 fix — see the SECURITY note below.
    ///
    /// @param envelopeDigest  digest of the entry to revoke
    /// @param proof           in the production AVS slice this is the canonical
    ///                        replay-artifact reference; it MUST be verifiable
    ///                        before it can justify a slash
    function revoke(bytes32 envelopeDigest, bytes calldata proof) external {
        Entry storage e = _entries[envelopeDigest];
        if (!e.exists)  revert DigestNotFound(envelopeDigest);
        if (e.revoked)  revert AlreadyRevoked(envelopeDigest);

        e.revoked = true;
        address issuer = e.issuer;

        // Permissionless: sets a flag only, no funds move.
        emit Revoked(envelopeDigest, msg.sender, proof.length);

        // ---------------------------------------------------------------------
        // SECURITY (VF-11): The economic slash MUST NOT be triggerable by an
        // arbitrary caller with an unverified `proof`. The previous code called
        // `slasher.slash(...)` unconditionally on every AVS-mode revoke, so any
        // address could slash an honest issuer for the price of gas.
        //
        // The slash now fires ONLY when `_verifySlashProof(...)` confirms the
        // fraud `proof` is an EIP-191 signature by the trusted `slashingReporter`
        // key over the canonical slash message (chainid + this contract +
        // envelopeDigest + issuer + slashWad). A revoke with any other proof
        // still flips the revoked flag (open-challenge model) but emits
        // `SlashWithheldUnverifiedProof` and moves no stake. Revocation stays
        // permissionless; only the economic penalty is gated.
        //   >>> Slashing is now ENABLED behind reporter-key attestation.
        //   >>> REQUIRES HUMAN SECURITY REVIEW + Sepolia REDEPLOY BEFORE PRODUCTION. <<<
        // ---------------------------------------------------------------------
        if (avsModeEnabled()) {
            bytes32 reasonHash = keccak256(proof);
            if (_verifySlashProof(envelopeDigest, issuer, proof)) {
                slasher.slash(issuer, slashWad, reasonHash);
                emit SlashTriggered(envelopeDigest, issuer, slashWad, reasonHash);
            } else {
                emit SlashWithheldUnverifiedProof(envelopeDigest, issuer, msg.sender, reasonHash);
            }
        }
    }

    /// @dev SECURITY (VF-11): Cryptographic gate for the economic slash.
    ///      Returns true ONLY for a fraud `proof` that cryptographically
    ///      justifies slashing `issuer` for `envelopeDigest`.
    ///
    ///      SCHEME (locked design — see the VF-11 implementation note):
    ///        - Authority: a single trusted Slashing-Reporter ECDSA key
    ///          (`slashingReporter`, set at construction).
    ///        - `proof` is a 65-byte `(r, s, v)` secp256k1 signature by that key
    ///          over the EIP-191 ("\x19Ethereum Signed Message:\n32") digest of
    ///          the canonical slash message
    ///              m = keccak256(abi.encode(
    ///                      block.chainid, address(this),
    ///                      envelopeDigest, issuer, slashWad));
    ///          Binding chainid + this contract + the (once-revocable) digest +
    ///          the issuer + the wad gives cross-chain / cross-contract / replay
    ///          safety: a signature is valid for exactly one slash on one chain,
    ///          and the digest can only be revoked once (see `revoke`).
    ///        - Settlement is IMMEDIATE on a valid proof (no challenge window).
    ///
    ///      Malleability / validity guards: reject any `proof` whose length is
    ///      not 65 bytes, whose `v` is not 27 or 28, or whose `s` is in the
    ///      upper half of the curve order (EIP-2 low-s), and reject an
    ///      `ecrecover` of address(0).
    ///
    ///      NOTE: this authorizes REAL slashing. REQUIRES HUMAN SECURITY REVIEW
    ///      + Sepolia redeploy before production. The reporter key is a trusted
    ///      component; its custody/rotation is an operational concern documented
    ///      for the security reviewer.
    function _verifySlashProof(
        bytes32 envelopeDigest,
        address issuer,
        bytes calldata proof
    ) internal view returns (bool) {
        // A valid ECDSA signature is exactly 65 bytes: r (32) || s (32) || v (1).
        if (proof.length != 65) {
            return false;
        }

        bytes32 r;
        bytes32 s;
        uint8   v;
        assembly {
            // proof is calldata; load the three components.
            r := calldataload(proof.offset)
            s := calldataload(add(proof.offset, 32))
            // v is the 33rd byte; load the word starting there and take its
            // most-significant byte.
            v := byte(0, calldataload(add(proof.offset, 64)))
        }

        // EIP-2: reject the malleable upper-half s. secp256k1n/2 =
        // 0x7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF5D576E7357A4501DDFE92F46681B20A0.
        if (uint256(s) > 0x7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF5D576E7357A4501DDFE92F46681B20A0) {
            return false;
        }
        if (v != 27 && v != 28) {
            return false;
        }

        // Reconstruct the canonical slash message and apply the EIP-191 prefix.
        bytes32 m = keccak256(
            abi.encode(block.chainid, address(this), envelopeDigest, issuer, slashWad)
        );
        bytes32 ethSigned = keccak256(
            abi.encodePacked("\x19Ethereum Signed Message:\n32", m)
        );

        address recovered = ecrecover(ethSigned, v, r, s);
        if (recovered == address(0)) {
            return false;
        }
        return recovered == slashingReporter;
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
