/**
 * score.test.ts — Unit tests for the Quality Oracle scoring engine.
 *
 * Uses vitest. Mocks the Anthropic client to avoid live API calls.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";
import type { Mock } from "vitest";
import Anthropic from "@anthropic-ai/sdk";
import { score, computeSigma, determineVerdict } from "../src/score.js";
import type { ToolCallResult } from "../src/types.js";
import { AXES, ASSERTIONS } from "../src/types.js";

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const SAMPLE_TOOL_CALL: ToolCallResult = {
  request: {
    tool_name: "generate_prompt",
    tool_use_id: "tu_test_001",
    input: { topic: "write a haiku about rain", target_model: "claude-sonnet-4-6" },
    model_id: "claude-sonnet-4-6",
  },
  result: {
    tool_use_id: "tu_test_001",
    content: [
      {
        type: "text",
        text: "You are a poet. Write a haiku about rain.\n\nFormat: three lines, 5-7-5 syllables.\n\nConstraints: imagery only, no abstract concepts.\n\nEdge cases: if the theme is ambiguous, default to autumn rain.",
      },
    ],
  },
};

/** Build a fake submit_score tool-use API response. */
function buildFakeApiResponse(
  overrides: {
    scores?: number[];
    allPass?: boolean;
    registryMismatch?: boolean;
    techniqueStale?: boolean;
  } = {}
): { content: Anthropic.ContentBlock[]; stop_reason: string } {
  const scores = overrides.scores ?? [9, 9, 9, 9, 9];
  const allPass = overrides.allPass ?? true;

  const axes = AXES.map((axis, i) => ({
    axis,
    score: scores[i] ?? 9,
    rationale: `Rationale for ${axis}.`,
  }));

  const assertions = ASSERTIONS.map((assertion) => ({
    assertion,
    passed: allPass,
    rationale: `Rationale for ${assertion}.`,
  }));

  const toolUseBlock: Anthropic.ToolUseBlock = {
    type: "tool_use",
    id: "tu_fake_score",
    name: "submit_score",
    input: {
      axes,
      assertions,
      registry_mismatch: overrides.registryMismatch ?? false,
      technique_stale: overrides.techniqueStale ?? false,
    },
  };

  return {
    content: [toolUseBlock],
    stop_reason: "tool_use",
  };
}

// ---------------------------------------------------------------------------
// σ computation
// ---------------------------------------------------------------------------

describe("computeSigma", () => {
  it("returns 0 for uniform scores [9,9,9,9,9]", () => {
    expect(computeSigma([9, 9, 9, 9, 9])).toBe(0);
  });

  it("returns 0 for a single value", () => {
    expect(computeSigma([7])).toBe(0);
  });

  it("returns correct σ for [6,8,10,6,8] ≈ 1.497", () => {
    const sigma = computeSigma([6, 8, 10, 6, 8]);
    expect(sigma).toBeCloseTo(1.497, 2);
  });

  it("returns correct σ for [9,9,9,9,10] ≈ 0.4", () => {
    const sigma = computeSigma([9, 9, 9, 9, 10]);
    expect(sigma).toBeCloseTo(0.4, 2);
  });
});

// ---------------------------------------------------------------------------
// Verdict determination
// ---------------------------------------------------------------------------

describe("determineVerdict", () => {
  const makeAxes = (scores: number[]) =>
    AXES.map((axis, i) => ({ axis, score: scores[i] ?? 9, rationale: "ok" }));

  const makeAssertions = (allPass: boolean) =>
    ASSERTIONS.map((assertion) => ({ assertion, passed: allPass, rationale: "ok" }));

  it("returns DEPLOY when all gates pass", () => {
    expect(
      determineVerdict(makeAxes([9, 9, 9, 9, 9]), makeAssertions(true), 0, 9.0, false, false)
    ).toBe("DEPLOY");
  });

  it("returns HOLD when σ ≥ 0.45", () => {
    expect(
      determineVerdict(makeAxes([9, 9, 9, 9, 10]), makeAssertions(true), 0.45, 9.2, false, false)
    ).toBe("HOLD");
  });

  it("returns HOLD when overall < 9.0", () => {
    expect(
      determineVerdict(makeAxes([8, 8, 8, 8, 8]), makeAssertions(true), 0, 8.0, false, false)
    ).toBe("HOLD");
  });

  it("returns HOLD when any axis < 7.0", () => {
    expect(
      determineVerdict(makeAxes([9, 9, 6, 9, 9]), makeAssertions(true), 0, 8.4, false, false)
    ).toBe("HOLD");
  });

  it("returns HOLD when not all assertions pass", () => {
    const assertions = makeAssertions(true);
    assertions[0] = { ...assertions[0], passed: false };
    expect(
      determineVerdict(makeAxes([9, 9, 9, 9, 9]), assertions, 0, 9.0, false, false)
    ).toBe("HOLD");
  });

  it("returns FAIL when registry_mismatch is true (overrides DEPLOY eligibility)", () => {
    expect(
      determineVerdict(makeAxes([9, 9, 9, 9, 9]), makeAssertions(true), 0, 9.0, true, false)
    ).toBe("FAIL");
  });

  it("returns FAIL when technique_stale is true", () => {
    expect(
      determineVerdict(makeAxes([9, 9, 9, 9, 9]), makeAssertions(true), 0, 9.0, false, true)
    ).toBe("FAIL");
  });
});

// ---------------------------------------------------------------------------
// score() integration (mocked Anthropic client)
// ---------------------------------------------------------------------------

describe("score()", () => {
  let mockClient: Anthropic;

  beforeEach(() => {
    mockClient = {
      messages: {
        create: vi.fn(),
      },
    } as unknown as Anthropic;
  });

  it("returns a structured ScoringVerdict with DEPLOY on perfect scores", async () => {
    (mockClient.messages.create as Mock).mockResolvedValueOnce(
      buildFakeApiResponse({ scores: [9, 9, 9, 9, 9], allPass: true })
    );

    const verdict = await score(SAMPLE_TOOL_CALL, mockClient);

    expect(verdict.verdict).toBe("DEPLOY");
    expect(verdict.sigma).toBe(0);
    expect(verdict.overall).toBe(9);
    expect(verdict.axes).toHaveLength(5);
    expect(verdict.assertions).toHaveLength(8);
    expect(verdict.scored_at).toBeTruthy();
    expect(verdict.model_used).toBe("claude-sonnet-4-6");
  });

  it("returns HOLD when any score is low", async () => {
    (mockClient.messages.create as Mock).mockResolvedValueOnce(
      buildFakeApiResponse({ scores: [7, 7, 7, 7, 7], allPass: true })
    );

    const verdict = await score(SAMPLE_TOOL_CALL, mockClient);
    expect(verdict.verdict).toBe("HOLD");
  });

  it("returns FAIL when registry_mismatch is signalled", async () => {
    (mockClient.messages.create as Mock).mockResolvedValueOnce(
      buildFakeApiResponse({ scores: [9, 9, 9, 9, 9], allPass: true, registryMismatch: true })
    );

    const verdict = await score(SAMPLE_TOOL_CALL, mockClient);
    expect(verdict.verdict).toBe("FAIL");
  });

  it("throws if the model does not call submit_score", async () => {
    (mockClient.messages.create as Mock).mockResolvedValueOnce({
      content: [{ type: "text", text: "Here is my analysis..." }],
      stop_reason: "end_turn",
    });

    await expect(score(SAMPLE_TOOL_CALL, mockClient)).rejects.toThrow(
      "Scoring model did not call submit_score"
    );
  });

  it("throws on missing axis in model response", async () => {
    const badResponse = buildFakeApiResponse({ scores: [9, 9, 9, 9, 9], allPass: true });
    // Remove one axis from the tool input
    const toolBlock = badResponse.content[0] as Anthropic.ToolUseBlock;
    const input = toolBlock.input as { axes: Array<{ axis: string }> };
    input.axes = input.axes.filter((a) => a.axis !== "safety");

    (mockClient.messages.create as Mock).mockResolvedValueOnce(badResponse);

    await expect(score(SAMPLE_TOOL_CALL, mockClient)).rejects.toThrow(
      "Scoring model did not return axis: safety"
    );
  });
});
