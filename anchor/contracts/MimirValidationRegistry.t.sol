// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

// -----------------------------------------------------------------------------
// MimirValidationRegistry — Foundry test suite
//
// Run with:  forge test --match-path contracts/MimirValidationRegistry.t.sol -vvv
// Coverage:  forge coverage --match-path contracts/MimirValidationRegistry.t.sol
// -----------------------------------------------------------------------------

import "forge-std/Test.sol";
import "./MimirValidationRegistry.sol";

contract MimirValidationRegistryTest is Test {
    MimirValidationRegistry registry;

    address constant ISSUER_A = address(0xA1);
    address constant ISSUER_B = address(0xB2);
    address constant THIRD_PARTY = address(0xC3);

    bytes32 constant DIGEST_1 = keccak256("envelope-1");
    bytes32 constant DIGEST_2 = keccak256("envelope-2");
    bytes32 constant DIGEST_3 = keccak256("envelope-3");

    uint256 constant FAR_FUTURE = 9_999_999_999;
    uint256 constant NO_EXPIRY  = 0;

    function setUp() public {
        registry = new MimirValidationRegistry();
    }

    // -----------------------------------------------------------------------
    // Test 1 — register + verify round trip
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
    // Test 3 — revoke flips revoked flag to true
    // -----------------------------------------------------------------------
    function test_RevokeFlipsFlag() public {
        registry.register(DIGEST_1, ISSUER_A, FAR_FUTURE);

        assertFalse(registry.verify(DIGEST_1).revoked == true, "pre-condition");

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

        // Before expiry: valid
        assertTrue(registry.isValid(DIGEST_2), "should be valid before expiry");

        // Warp past expiry
        vm.warp(block.timestamp + 101);

        assertFalse(registry.isValid(DIGEST_2), "should be invalid after expiry");

        // verify() still returns the stored values (it does not check expiry itself)
        (address issuer, uint256 storedExpiry, bool revoked) = registry.verify(DIGEST_2);
        assertEq(issuer, ISSUER_B);
        assertEq(storedExpiry, expiry);
        assertFalse(revoked);
    }

    // -----------------------------------------------------------------------
    // Test 5 — non-issuer revoke succeeds (open challenge model)
    //
    // NOTE: In the EigenLayer AVS slice, this call will additionally trigger
    //       ISlasher.freezeOperator(issuer) via the EIGENLAYER_HOOK in
    //       MimirValidationRegistry.revoke().  The economic slash is gated
    //       on replay-artifact verification inside the AVS Slashing Reporter;
    //       only the on-chain revocation flag is set here unconditionally.
    // -----------------------------------------------------------------------
    function test_NonIssuerRevokeSucceeds() public {
        registry.register(DIGEST_3, ISSUER_A, FAR_FUTURE);

        // Any address — not the issuer — can revoke
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

        // Warp a long time into the future
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
}
