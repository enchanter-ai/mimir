## Attack 02 — One byte flipped mid-signature

Byte at position `len/2` of the 64-byte Ed25519 signature has been XOR-flipped (all 8 bits inverted). The base64url-encoded length is unchanged (still 86 chars), so the envelope passes structural checks. A correct verifier **MUST** reject this because the signature no longer verifies over the canonical form (`VE-008`). Spec §10.2 step 10–11 requires the Ed25519 signature to verify; §15.4 (Timing Side-Channels) demands constant-time rejection to prevent byte-by-byte oracle attacks — this vector exercises that path.
