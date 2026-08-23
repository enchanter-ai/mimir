// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

// -----------------------------------------------------------------------------
// MimirValidationRegistry — Foundry test suite
//
// Run with:  forge test --match-path contracts/MimirValidationRegistry.t.sol -vvv
// Coverage:  forge coverage --match-path contracts/MimirValidationRegistry.t.sol
//
// NOTE ON TOOLCHAIN: the canonical, CI-run coverage for these contracts lives in
// the Go simulated-EVM suite (anchor/go/*_test.go), because compile.js does not
// vendor forge-std and excludes *.t.sol from the solc build. This Foundry file
// is kept in lockstep with that suite; if you have Foundry installed it runs
// standalone, and it documents the intended behavior in Solidity terms.
//
// The MimirValidationRegistry constructor is
//   (IServiceManager serviceManager, ISlasher slasher, uint256 slashWad).
// Permissionless mode: pass address(0) for both handles.
// AVS mode:            pass non-zero handles (operator gating + slashing gate).
// -----------------------------------------------------------------------------

import "forge-std/Test.sol";
import "./MimirValidationRegistry.sol";
import "./MockServiceManager.sol";
import "./MockSlasher.sol";

contract MimirValidationRegistryTest is Test {
    MimirValidationRegistry registry; // permissionless-mode instance

    address constant ISSUER_A = address(0xA1);
    address constant ISSUER_B = address(0xB2);
    address constant THIRD_PARTY = address(0xC3);

    bytes32 constant DIGEST_1 = keccak256("envelope-1");
    bytes32 constant DIGEST_2 = keccak256("envelope-2");
    bytes32 constant DIGEST_3 = keccak256("envelope-3");

    uint256 constant FAR_FUTURE = 9_999_999_999;
    uint256 constant NO_EXPIRY  = 0;

    // VF-11: trusted Slashing-Reporter key. Its address authorizes slashes;
    // tests sign the canonical slash message with REPORTER_PK via vm.sign.
    uint256 constant REPORTER_PK = 0xA11CE;
    address REPORTER = vm.addr(REPORTER_PK);

    function setUp() public {
        // Permissionless mode: no service manager, no slasher, default wad,
        // no slashing reporter (unused when the AVS handles are zero).
        registry = new MimirValidationRegistry(
            IServiceManager(address(0)),
            ISlasher(address(0)),
            0,
            address(0)
        );
    }

    // Helper: spin up an AVS-mode registry wired to fresh mocks, with REPORTER
    // as the trusted Slashing-Reporter key.
    function _deployAvs(uint256 slashWad)
        internal
        returns (MimirValidationRegistry avs, MockServiceManager mgr, MockSlasher sl)
    {
        mgr = new MockServiceManager();
        sl  = new MockSlasher();
        avs = new MimirValidationRegistry(
            IServiceManager(address(mgr)),
            ISlasher(address(sl)),
            slashWad,
            REPORTER
        );
    }

    // _signSlashProof builds a 65-byte (r,s,v) EIP-191 signature by REPORTER_PK
    // over the canonical slash message that MimirValidationRegistry verifies:
    //   m = keccak256(abi.encode(chainid, registry, digest, issuer, slashWad))
    //   ethSigned = keccak256("\x19Ethereum Signed Message:\n32" || m)
    function _signSlashProof(
        MimirValidationRegistry avs,
        bytes32 digest,
        address issuer,
        uint256 slashWad
    ) internal view returns (bytes memory) {
        bytes32 m = keccak256(
            abi.encode(block.chainid, address(avs), digest, issuer, slashWad)
        );
        bytes32 ethSigned = keccak256(
            abi.encodePacked("\x19Ethereum Signed Message:\n32", m)
        );
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(REPORTER_PK, ethSigned);
        return abi.encodePacked(r, s, v);
    }

    // -----------------------------------------------------------------------
    // Test 1 — register + verify round trip (permissionless)
    // -----------------------------------------------------------------------
    function test_RegisterAndVerify() public {
        vm.prank(ISSUER_A);
        registry.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        (address issuer, uint256 expiry, bool revoked) = registry.verify(DIGEST_1);

        assertEq(issuer, ISSUER_A, "issuer mismatch");
        assertEq(expiry, FAR_FUTURE, "expiry mismatch");
        assertFalse(revoked, "should not be revoked");
        assertTrue(registry.exists(DIGEST_1), "should exist");
        assertTrue(registry.isValid(DIGEST_1), "should be valid");
    }

    // -----------------------------------------------------------------------
    // Test 2 — re-register same digest reverts
    // -----------------------------------------------------------------------
    function test_ReregisterReverts() public {
        registry.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        vm.expectRevert(
            abi.encodeWithSelector(
                MimirValidationRegistry.DigestAlreadyRegistered.selector,
                DIGEST_1
            )
        );
        registry.register(DIGEST_1, ISSUER_B, FAR_FUTURE);
    }

    // -----------------------------------------------------------------------
    // Test 3 — revoke flips revoked flag to true (permissionless)
    // -----------------------------------------------------------------------
    function test_RevokeFlipsFlag() public {
        registry.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        (, , bool pre) = registry.verify(DIGEST_1);
        assertFalse(pre, "pre-condition");

        bytes memory proof = abi.encode("replay-artifact-hash-placeholder");
        vm.prank(THIRD_PARTY);
        registry.revoke(DIGEST_1, proof);

        (, , bool revoked) = registry.verify(DIGEST_1);
        assertTrue(revoked, "revoked flag should be true");
        assertFalse(registry.isValid(DIGEST_1), "isValid should be false after revoke");
    }

    // -----------------------------------------------------------------------
    // Test 4 — expired entry returns correct data and isValid == false
    // -----------------------------------------------------------------------
    function test_ExpiredEntryBehavior() public {
        uint256 expiry = block.timestamp + 100;
        registry.register(DIGEST_2, ISSUER_B, expiry);

        assertTrue(registry.isValid(DIGEST_2), "should be valid before expiry");
        vm.warp(block.timestamp + 101);
        assertFalse(registry.isValid(DIGEST_2), "should be invalid after expiry");

        (address issuer, uint256 storedExpiry, bool revoked) = registry.verify(DIGEST_2);
        assertEq(issuer, ISSUER_B);
        assertEq(storedExpiry, expiry);
        assertFalse(revoked);
    }

    // -----------------------------------------------------------------------
    // Test 5 — non-issuer revoke succeeds (open-challenge model), no slasher
    // -----------------------------------------------------------------------
    function test_NonIssuerRevokeSucceeds() public {
        registry.register(DIGEST_3, ISSUER_A, FAR_FUTURE);

        vm.prank(THIRD_PARTY);
        registry.revoke(DIGEST_3, "proof-from-third-party");

        (, , bool revoked) = registry.verify(DIGEST_3);
        assertTrue(revoked, "third-party revoke should succeed");
    }

    // -----------------------------------------------------------------------
    // Test 6 — revoke of unknown digest reverts
    // -----------------------------------------------------------------------
    function test_RevokeUnknownReverts() public {
        vm.expectRevert(
            abi.encodeWithSelector(
                MimirValidationRegistry.DigestNotFound.selector,
                DIGEST_1
            )
        );
        registry.revoke(DIGEST_1, "proof");
    }

    // -----------------------------------------------------------------------
    // Test 7 — double-revoke reverts
    // -----------------------------------------------------------------------
    function test_DoubleRevokeReverts() public {
        registry.register(DIGEST_1, ISSUER_A, FAR_FUTURE);
        registry.revoke(DIGEST_1, "proof");

        vm.expectRevert(
            abi.encodeWithSelector(
                MimirValidationRegistry.AlreadyRevoked.selector,
                DIGEST_1
            )
        );
        registry.revoke(DIGEST_1, "proof-2");
    }

    // -----------------------------------------------------------------------
    // Test 8 — no-expiry entry (expiry == 0) never expires
    // -----------------------------------------------------------------------
    function test_NoExpiryNeverExpires() public {
        registry.register(DIGEST_1, ISSUER_A, NO_EXPIRY);
        vm.warp(block.timestamp + 365 days * 100);
        assertTrue(registry.isValid(DIGEST_1), "no-expiry entry should always be valid");
    }

    // -----------------------------------------------------------------------
    // Test 9 — batch register
    // -----------------------------------------------------------------------
    function test_BatchRegister() public {
        bytes32[] memory digests = new bytes32[](2);
        address[] memory issuers = new address[](2);
        uint256[] memory expiries = new uint256[](2);

        digests[0]  = DIGEST_1;   issuers[0]  = ISSUER_A; expiries[0]  = FAR_FUTURE;
        digests[1]  = DIGEST_2;   issuers[1]  = ISSUER_B; expiries[1]  = NO_EXPIRY;

        registry.registerBatch(digests, issuers, expiries);

        assertTrue(registry.exists(DIGEST_1));
        assertTrue(registry.exists(DIGEST_2));
        assertFalse(registry.exists(DIGEST_3));
    }

    // -----------------------------------------------------------------------
    // Test 10 — events are emitted on register and revoke
    // -----------------------------------------------------------------------
    function test_EventsEmitted() public {
        vm.expectEmit(true, true, false, true);
        emit MimirValidationRegistry.Registered(DIGEST_1, ISSUER_A, FAR_FUTURE);
        registry.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        bytes memory proof = "proof-bytes";
        vm.expectEmit(true, true, false, true);
        emit MimirValidationRegistry.Revoked(DIGEST_1, address(this), proof.length);
        registry.revoke(DIGEST_1, proof);
    }

    // =======================================================================
    // VF-11 REGRESSION TESTS — unverified proof MUST NOT slash
    // =======================================================================

    // (a) A slash attempt with a bogus/unverified proof MUST NOT slash.
    //     Any third party can call revoke() in AVS mode; the revocation flag
    //     flips (open-challenge model preserved) but no stake is slashed,
    //     because _verifySlashProof() rejects the unverified proof.
    function test_VF11_UnverifiedProofDoesNotSlash() public {
        (MimirValidationRegistry avs, MockServiceManager mgr, MockSlasher sl) =
            _deployAvs(2.5e17); // 25%

        // Operator registers + anchors their own envelope.
        mgr.registerOperator(ISSUER_A);
        vm.prank(ISSUER_A);
        avs.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        assertEq(sl.totalSlashed(ISSUER_A), 0, "pre-revoke slashed must be 0");

        // Attacker submits a bogus proof to slash the honest issuer.
        vm.prank(THIRD_PARTY);
        avs.revoke(DIGEST_1, "totally-bogus-proof");

        // VF-11: no slash occurred.
        assertEq(sl.totalSlashed(ISSUER_A), 0, "VF-11: bogus proof must NOT slash");

        // Revocation flag still flipped (permissionless challenge intact).
        (, , bool revoked) = avs.verify(DIGEST_1);
        assertTrue(revoked, "revoke flag should still flip");
        assertFalse(avs.isValid(DIGEST_1), "isValid should be false after revoke");
    }

    // (a') The VF-11 "withheld" event is emitted instead of SlashTriggered.
    function test_VF11_EmitsWithheldEventNotSlashTriggered() public {
        (MimirValidationRegistry avs, MockServiceManager mgr, ) = _deployAvs(0);
        mgr.registerOperator(ISSUER_A);
        vm.prank(ISSUER_A);
        avs.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        bytes memory proof = "bogus";
        // Expect the withheld-proof event with the correct reasonHash.
        vm.expectEmit(true, true, true, true);
        emit MimirValidationRegistry.SlashWithheldUnverifiedProof(
            DIGEST_1, ISSUER_A, THIRD_PARTY, keccak256(proof)
        );
        vm.prank(THIRD_PARTY);
        avs.revoke(DIGEST_1, proof);
    }

    // (b) Legitimate NON-SLASH paths still work in AVS mode:
    //     operator registration + anchoring + verify round-trip.
    function test_AvsLegitimateRegisterAndVerify() public {
        (MimirValidationRegistry avs, MockServiceManager mgr, ) = _deployAvs(0);

        // Non-operator cannot anchor.
        vm.prank(ISSUER_B);
        vm.expectRevert(
            abi.encodeWithSelector(
                MimirValidationRegistry.NotAnOperator.selector,
                ISSUER_B
            )
        );
        avs.register(DIGEST_2, ISSUER_B, FAR_FUTURE);

        // Registered operator can anchor their own envelope and verify it.
        mgr.registerOperator(ISSUER_A);
        vm.prank(ISSUER_A);
        avs.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        (address issuer, , ) = avs.verify(DIGEST_1);
        assertEq(issuer, ISSUER_A, "operator anchor round-trip");
        assertTrue(avs.isValid(DIGEST_1), "should be valid");
    }

    // (c) Intended permissionless revocation WITHOUT slashing still works.
    //     Permissionless mode has no slasher at all; any address revokes and
    //     the flag flips.
    function test_PermissionlessRevokeWithoutSlashing() public {
        registry.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        vm.prank(THIRD_PARTY);
        registry.revoke(DIGEST_1, "any-proof");

        (, , bool revoked) = registry.verify(DIGEST_1);
        assertTrue(revoked, "permissionless third-party revoke should flip flag");
        assertFalse(registry.avsModeEnabled(), "permissionless mode: AVS disabled");
    }

    // =======================================================================
    // VF-11 IMPLEMENTATION TESTS — a VALID reporter proof enables slashing
    // =======================================================================

    // (d) A VALID reporter-signed proof slashes by EXACTLY slashWad and emits
    //     SlashTriggered. This is the legitimate-slash path the suite lacked.
    function test_VF11_ValidProofSlashesExactWad() public {
        uint256 wad = 2.5e17; // 25%
        (MimirValidationRegistry avs, MockServiceManager mgr, MockSlasher sl) =
            _deployAvs(wad);

        mgr.registerOperator(ISSUER_A);
        vm.prank(ISSUER_A);
        avs.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        assertEq(sl.totalSlashed(ISSUER_A), 0, "pre-revoke slashed must be 0");

        bytes memory proof = _signSlashProof(avs, DIGEST_1, ISSUER_A, wad);

        vm.expectEmit(true, true, false, true);
        emit MimirValidationRegistry.SlashTriggered(
            DIGEST_1, ISSUER_A, wad, keccak256(proof)
        );

        vm.prank(THIRD_PARTY); // revoke is permissionless; proof carries authority
        avs.revoke(DIGEST_1, proof);

        assertEq(sl.totalSlashed(ISSUER_A), wad, "valid proof must slash by exactly slashWad");

        (, , bool revoked) = avs.verify(DIGEST_1);
        assertTrue(revoked, "revoke flag should flip");
        assertFalse(avs.isValid(DIGEST_1), "isValid false after revoke");
    }

    // (e) A proof signed by the WRONG key must NOT slash.
    function test_VF11_WrongKeyProofDoesNotSlash() public {
        uint256 wad = 1e17;
        (MimirValidationRegistry avs, MockServiceManager mgr, MockSlasher sl) =
            _deployAvs(wad);

        mgr.registerOperator(ISSUER_A);
        vm.prank(ISSUER_A);
        avs.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        // Sign with an attacker key over the correct message.
        uint256 attackerPk = 0xBAD;
        bytes32 m = keccak256(
            abi.encode(block.chainid, address(avs), DIGEST_1, ISSUER_A, wad)
        );
        bytes32 ethSigned = keccak256(
            abi.encodePacked("\x19Ethereum Signed Message:\n32", m)
        );
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(attackerPk, ethSigned);
        bytes memory proof = abi.encodePacked(r, s, v);

        vm.expectEmit(true, true, true, true);
        emit MimirValidationRegistry.SlashWithheldUnverifiedProof(
            DIGEST_1, ISSUER_A, THIRD_PARTY, keccak256(proof)
        );
        vm.prank(THIRD_PARTY);
        avs.revoke(DIGEST_1, proof);

        assertEq(sl.totalSlashed(ISSUER_A), 0, "wrong-key proof must NOT slash");
    }

    // (f) A reporter signature over the WRONG digest must NOT slash the target.
    function test_VF11_WrongDigestProofDoesNotSlash() public {
        uint256 wad = 1e17;
        (MimirValidationRegistry avs, MockServiceManager mgr, MockSlasher sl) =
            _deployAvs(wad);

        mgr.registerOperator(ISSUER_A);
        vm.startPrank(ISSUER_A);
        avs.register(DIGEST_1, ISSUER_A, FAR_FUTURE);
        avs.register(DIGEST_2, ISSUER_A, FAR_FUTURE);
        vm.stopPrank();

        // Reporter signs for DIGEST_2 but we submit it against DIGEST_1.
        bytes memory proof = _signSlashProof(avs, DIGEST_2, ISSUER_A, wad);

        vm.prank(THIRD_PARTY);
        avs.revoke(DIGEST_1, proof);

        assertEq(sl.totalSlashed(ISSUER_A), 0, "wrong-digest proof must NOT slash");
    }

    // (g) A malformed (wrong-length) proof must NOT slash.
    function test_VF11_MalformedProofDoesNotSlash() public {
        uint256 wad = 1e17;
        (MimirValidationRegistry avs, MockServiceManager mgr, MockSlasher sl) =
            _deployAvs(wad);

        mgr.registerOperator(ISSUER_A);
        vm.prank(ISSUER_A);
        avs.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        vm.prank(THIRD_PARTY);
        avs.revoke(DIGEST_1, hex"deadbeef"); // 4 bytes, not 65

        assertEq(sl.totalSlashed(ISSUER_A), 0, "malformed proof must NOT slash");
        (, , bool revoked) = avs.verify(DIGEST_1);
        assertTrue(revoked, "revoke flag should still flip");
    }
}
