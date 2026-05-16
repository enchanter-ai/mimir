/**
 * produce-verify.test.ts — Roundtrip test: produce → verify.
 *
 * Generates an Ed25519 key pair, produces an envelope, and verifies it.
 * Expected outcome: "cryptographically_valid" (Level 2).
 *
 * DRAFT — for spec validation only.
 */

import * as ed25519 from "@noble/ed25519";
import { produce } from "../src/produce";
import { verify } from "../src/verify";
import type {
  Ed25519SigningKey,
  Ed25519VerificationKey,
  VerificationKey,
  Source,
} from "../src/types";

// @noble/ed25519 requires a sha512 sync implementation for node < 20
// Use the node:crypto backed synchronous implementation
import { sha512 } from "@noble/hashes/sha512";
ed25519.etc.sha512Sync = (...m) => sha512(ed25519.etc.concatBytes(...m));

// ─── Key material ─────────────────────────────────────────────────────────

function generateEd25519KeyPair(): { privateKey: Uint8Array; publicKey: Uint8Array } {
  const privateKey = ed25519.utils.randomPrivateKey();
  const publicKey = ed25519.getPublicKey(privateKey);
  return { privateKey, publicKey };
}

// ─── Fixtures ─────────────────────────────────────────────────────────────

const TOOL_CALL_ID = "tc_01HXYZK4Q7M9N1J5R8B2C6D3E0";
const TOOL_ID = "did:web:example.com:tools:wiki-search";
const KEY_ID = "did:web:example.com#keys-1";

const REQUEST_PARAMS = {
  name: "wiki-search",
  arguments: { query: "Eiffel Tower height" },
};

const RESULT_CONTENT = [
  { type: "text", text: "The Eiffel Tower is 330 meters tall." },
];

const SOURCES: Source[] = [
  {
    type: "web",
    url: "https://en.wikipedia.org/wiki/Eiffel_Tower",
    retrieved_at: "2026-05-13T14:23:10.998Z",
    weight: 1.0,
  },
];

// ─── Tests ─────────────────────────────────────────────────────────────────

describe("produce + verify roundtrip (Ed25519)", () => {
  let privateKey: Uint8Array;
  let publicKey: Uint8Array;

  beforeAll(() => {
    const kp = generateEd25519KeyPair();
    privateKey = kp.privateKey;
    publicKey = kp.publicKey;
  });

  test("produces a signed envelope with all required fields", async () => {
    const signingKey: Ed25519SigningKey = {
      alg: "Ed25519",
      privateKey,
      keyId: KEY_ID,
    };

    const envelope = await produce(REQUEST_PARAMS, RESULT_CONTENT, signingKey, {
      tool_call_id: TOOL_CALL_ID,
      tool_id: TOOL_ID,
      tool_version: "1.4.2",
      invoked_by: "did:agent:0xf3a9c2",
      invoked_at: "2026-05-13T14:23:11.412Z",
      sources: SOURCES,
    });

    expect(envelope.version).toBe("mcp-provenance/2026-05-13-ed25519");
    expect(envelope.tool_call_id).toBe(TOOL_CALL_ID);
    expect(envelope.tool_id).toBe(TOOL_ID);
    expect(envelope.tool_version).toBe("1.4.2");
    expect(envelope.request_digest).toMatch(/^sha-256:[0-9a-f]{64}$/);
    expect(envelope.result_digest).toMatch(/^sha-256:[0-9a-f]{64}$/);
    expect(envelope.signature.protected_header.alg).toBe("Ed25519");
    expect(envelope.signature.protected_header.key_id).toBe(KEY_ID);
    expect(typeof envelope.signature.value).toBe("string");
    expect(envelope.signature.value.length).toBeGreaterThan(0);
    // Ed25519 signature is 64 bytes = 86 base64url chars without padding (§ 11)
    expect(envelope.signature.value.length).toBe(86);
  });

  test("verify returns cryptographically_valid for a correctly signed envelope", async () => {
    const signingKey: Ed25519SigningKey = {
      alg: "Ed25519",
      privateKey,
      keyId: KEY_ID,
    };

    const envelope = await produce(REQUEST_PARAMS, RESULT_CONTENT, signingKey, {
      tool_call_id: TOOL_CALL_ID,
      tool_id: TOOL_ID,
      tool_version: "1.4.2",
      invoked_by: "did:agent:0xf3a9c2",
      invoked_at: new Date().toISOString(), // use now to avoid clock skew rejection
      sources: SOURCES,
    });

    const outcome = await verify(envelope, REQUEST_PARAMS, RESULT_CONTENT, {
      expectedToolCallId: TOOL_CALL_ID,
      resolveKey: async (_toolId: string, _keyId: string): Promise<VerificationKey | null> => {
        const vk: Ed25519VerificationKey = {
          alg: "Ed25519",
          publicKey,
          keyId: KEY_ID,
        };
        return vk;
      },
    });

    expect(outcome.level).toBe("cryptographically_valid");
    expect(outcome.errors).toHaveLength(0);
  });

  test("verify returns invalid when result content is tampered", async () => {
    const signingKey: Ed25519SigningKey = {
      alg: "Ed25519",
      privateKey,
      keyId: KEY_ID,
    };

    const envelope = await produce(REQUEST_PARAMS, RESULT_CONTENT, signingKey, {
      tool_call_id: TOOL_CALL_ID,
      tool_id: TOOL_ID,
      tool_version: "1.4.2",
      invoked_by: "did:agent:0xf3a9c2",
      invoked_at: new Date().toISOString(),
      sources: SOURCES,
    });

    // Tamper: different result content
    const tamperedResult = [{ type: "text", text: "TAMPERED" }];

    const outcome = await verify(envelope, REQUEST_PARAMS, tamperedResult, {
      resolveKey: async (): Promise<VerificationKey | null> => ({
        alg: "Ed25519",
        publicKey,
        keyId: KEY_ID,
      }),
    });

    expect(outcome.level).toBe("invalid");
    expect(outcome.errors).toContain("VE-010");
  });

  test("verify returns invalid when signature is corrupted", async () => {
    const signingKey: Ed25519SigningKey = {
      alg: "Ed25519",
      privateKey,
      keyId: KEY_ID,
    };

    const envelope = await produce(REQUEST_PARAMS, RESULT_CONTENT, signingKey, {
      tool_call_id: TOOL_CALL_ID,
      tool_id: TOOL_ID,
      tool_version: "1.4.2",
      invoked_by: "did:agent:0xf3a9c2",
      invoked_at: new Date().toISOString(),
      sources: SOURCES,
    });

    // Corrupt the signature value
    const corrupted = {
      ...envelope,
      signature: {
        ...envelope.signature,
        value: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      },
    };

    const outcome = await verify(corrupted, REQUEST_PARAMS, RESULT_CONTENT, {
      resolveKey: async (): Promise<VerificationKey | null> => ({
        alg: "Ed25519",
        publicKey,
        keyId: KEY_ID,
      }),
    });

    expect(outcome.level).toBe("invalid");
    expect(outcome.errors).toContain("VE-008");
  });

  test("verify returns well_formed when no key resolver is provided", async () => {
    const signingKey: Ed25519SigningKey = {
      alg: "Ed25519",
      privateKey,
      keyId: KEY_ID,
    };

    const envelope = await produce(REQUEST_PARAMS, RESULT_CONTENT, signingKey, {
      tool_call_id: TOOL_CALL_ID,
      tool_id: TOOL_ID,
      tool_version: "1.4.2",
      invoked_by: "did:agent:0xf3a9c2",
      invoked_at: new Date().toISOString(),
      sources: SOURCES,
    });

    // No resolveKey → Level 1 only
    const outcome = await verify(envelope, REQUEST_PARAMS, RESULT_CONTENT, {});

    expect(outcome.level).toBe("well_formed");
  });

  test("verify returns invalid for tool_call_id mismatch (VE-002)", async () => {
    const signingKey: Ed25519SigningKey = {
      alg: "Ed25519",
      privateKey,
      keyId: KEY_ID,
    };

    const envelope = await produce(REQUEST_PARAMS, RESULT_CONTENT, signingKey, {
      tool_call_id: TOOL_CALL_ID,
      tool_id: TOOL_ID,
      tool_version: "1.4.2",
      invoked_by: "did:agent:0xf3a9c2",
      invoked_at: new Date().toISOString(),
      sources: SOURCES,
    });

    const outcome = await verify(envelope, REQUEST_PARAMS, RESULT_CONTENT, {
      expectedToolCallId: "tc_DIFFERENT_ID",
    });

    expect(outcome.level).toBe("invalid");
    expect(outcome.errors).toContain("VE-002");
  });
});
