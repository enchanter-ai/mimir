/**
 * score.ts — Core scoring function for the Enchanter Labs Quality Oracle.
 *
 * MVP: single Sonnet call with structured tool-use output.
 * Production: Opus orchestrator + Haiku validator + Sonnet executor,
 *             dispatched via Wixie's substrate tier routing.
 */

import Anthropic from "@anthropic-ai/sdk";
import { pino } from "pino";
import type { ToolCallResult, ScoringVerdict, AxisScore, AssertionResult, SourceClaim } from "./types.js";
import { AxisScoreSchema, AssertionResultSchema, ScoringVerdictSchema, AXES, ASSERTIONS } from "./types.js";
import { buildScoringSystemPrompt, SCORING_TOOL_SCHEMA } from "./rubric.js";
import { z } from "zod";

const log = pino({ name: "quality-oracle-scoring" });

/** Model used for MVP scoring. Production uses full tier dispatch. */
export const SCORING_MODEL = "claude-sonnet-4-6" as const;

// ---------------------------------------------------------------------------
// σ computation
// ---------------------------------------------------------------------------

/** Population standard deviation of a numeric array. */
export function computeSigma(values: number[]): number {
  if (values.length === 0) return 0;
  const mean = values.reduce((s, v) => s + v, 0) / values.length;
  const variance = values.reduce((s, v) => s + Math.pow(v - mean, 2), 0) / values.length;
  return Math.sqrt(variance);
}

/**
 * Compute σ over the CONTENT axes only — clarity, specificity,
 * faithfulness_to_source, structure. Safety is excluded because for benign
 * tool outputs it pegs at 10 while content axes naturally cluster 8-9; that
 * 1-2 point gap inflates σ by ~0.5 without reflecting any actual disagreement
 * among scorers about quality.
 *
 * Safety is still enforced separately as a hard floor in determineVerdict
 * (every axis must be ≥ 7.0). The σ-bound exists to detect inter-axis
 * disagreement about quality — safety doesn't disagree about quality, so it
 * doesn't belong in σ.
 */
export function computeContentSigma(axes: AxisScore[]): number {
  const contentAxes = axes
    .filter((a) => a.axis !== "safety")
    .map((a) => a.score);
  return computeSigma(contentAxes);
}

// ---------------------------------------------------------------------------
// Verdict determination
// ---------------------------------------------------------------------------

/**
 * DEPLOY rules (all must hold):
 *   content-axis σ < 0.75   (was 0.45 in the original Wixie rubric)
 *   overall ≥ 9.0
 *   every axis score ≥ 7.0
 *   8/8 assertions pass
 *
 * σ threshold rationale (2026-05-16 calibration POC):
 *   The 0.45 threshold was empirically calibrated against Wixie's prompt
 *   distribution where all 5 axes naturally cluster tightly (8.5-9.5).
 *   Mimir's tool-call distribution under real Sonnet 4.6 judging shows
 *   content-axis σ in the 0.5-0.8 range even on uniformly-excellent
 *   results — Anthropic's API is documented as non-deterministic even
 *   at temperature=0, contributing residual variance. 0.75 was selected
 *   as the 90th-percentile observed σ on results that PASS all other
 *   gates. A real calibration set (~50 labeled cases) would tighten this.
 *
 * FAIL rules (any triggers FAIL before HOLD):
 *   registry_mismatch === true
 *   technique_stale === true
 *
 * Otherwise: HOLD
 */
export const DEPLOY_SIGMA_THRESHOLD = 0.75;

export function determineVerdict(
  axes: AxisScore[],
  assertions: AssertionResult[],
  sigma: number,
  overall: number,
  registryMismatch: boolean,
  techniqueStale: boolean
): "DEPLOY" | "HOLD" | "FAIL" {
  if (registryMismatch || techniqueStale) return "FAIL";

  const allAxesAboveFloor = axes.every((a) => a.score >= 7.0);
  const allAssertionsPass = assertions.every((a) => a.passed);

  if (sigma < DEPLOY_SIGMA_THRESHOLD && overall >= 9.0 && allAxesAboveFloor && allAssertionsPass) {
    return "DEPLOY";
  }
  return "HOLD";
}

// ---------------------------------------------------------------------------
// URL extraction (MVP source tracking)
// ---------------------------------------------------------------------------

const URL_RE = /https?:\/\/[^\s"')>]+/g;

export function extractSources(text: string): SourceClaim[] {
  const matches = text.match(URL_RE) ?? [];
  const seen = new Set<string>();
  return matches
    .filter((url) => {
      if (seen.has(url)) return false;
      seen.add(url);
      return true;
    })
    .map((url) => ({
      type: "url",
      url,
      retrieved_at: new Date().toISOString(),
    }));
}

// ---------------------------------------------------------------------------
// Tool-call content serialisation
// ---------------------------------------------------------------------------

function serialiseToolCallResult(toolCall: ToolCallResult): string {
  const lines: string[] = [
    `Tool name: ${toolCall.request.tool_name}`,
    `Tool use ID: ${toolCall.request.tool_use_id}`,
    `Input: ${JSON.stringify(toolCall.request.input, null, 2)}`,
  ];
  if (toolCall.request.model_id) {
    lines.push(`Target model: ${toolCall.request.model_id}`);
  }
  if (toolCall.result.is_error) {
    lines.push("Result: ERROR");
  }
  toolCall.result.content.forEach((block, i) => {
    if (block.type === "text") {
      lines.push(`Result[${i}] (text):\n${block.text}`);
    } else {
      lines.push(`Result[${i}] (image): <binary image omitted>`);
    }
  });
  return lines.join("\n");
}

// ---------------------------------------------------------------------------
// Raw API response parser
// ---------------------------------------------------------------------------

const RawScoreSchema = z.object({
  axes: z.array(
    z.object({ axis: z.string(), score: z.number(), rationale: z.string() })
  ),
  assertions: z.array(
    z.object({ assertion: z.string(), passed: z.boolean(), rationale: z.string() })
  ),
  registry_mismatch: z.boolean().optional(),
  technique_stale: z.boolean().optional(),
});

function parseToolUseInput(input: unknown): z.infer<typeof RawScoreSchema> {
  return RawScoreSchema.parse(input);
}

function validateAxes(raw: z.infer<typeof RawScoreSchema>["axes"]): AxisScore[] {
  return AXES.map((axisName) => {
    const found = raw.find((r) => r.axis === axisName);
    if (!found) {
      throw new Error(`Scoring model did not return axis: ${axisName}`);
    }
    return AxisScoreSchema.parse(found);
  });
}

function validateAssertions(raw: z.infer<typeof RawScoreSchema>["assertions"]): AssertionResult[] {
  return ASSERTIONS.map((assertionName) => {
    const found = raw.find((r) => r.assertion === assertionName);
    if (!found) {
      throw new Error(`Scoring model did not return assertion: ${assertionName}`);
    }
    return AssertionResultSchema.parse(found);
  });
}

// ---------------------------------------------------------------------------
// Main scoring function
// ---------------------------------------------------------------------------

/**
 * Score a tool-call result against the Enchanter Labs 5-axis + 8-assertion rubric.
 *
 * @param toolCall  The tool call input/output to evaluate.
 * @param client    An initialised Anthropic SDK client.
 * @returns         A validated ScoringVerdict.
 *
 * @throws          If the Anthropic API call fails or returns a malformed response.
 *
 * MVP note: uses a single claude-sonnet-4-6 call. Production replaces this with
 * Opus orchestrator + Haiku validator + Sonnet executor via substrate dispatch.
 */
export async function score(
  toolCall: ToolCallResult,
  client: Anthropic
): Promise<ScoringVerdict> {
  const scoredAt = new Date().toISOString();
  const content = serialiseToolCallResult(toolCall);

  log.info(
    { tool_name: toolCall.request.tool_name, tool_use_id: toolCall.request.tool_use_id },
    "scoring tool call"
  );

  // MOCK_MODE: short-circuit the Anthropic API for offline demos and unit tests.
  // Returns a deterministic stub verdict that passes all DEPLOY gates.
  if (process.env["MOCK_MODE"] === "1") {
    const stubAxes: AxisScore[] = AXES.map((axis) => ({
      axis,
      score: 9.2,
      rationale: `stub: ${axis} score from MOCK_MODE — replace with real Anthropic call for production`,
    }));
    const stubAssertions: AssertionResult[] = ASSERTIONS.map((assertion) => ({
      assertion,
      passed: true,
      rationale: `stub: ${assertion} passed under MOCK_MODE`,
    }));
    const stubScores = stubAxes.map((a) => a.score);
    const sigma = computeContentSigma(stubAxes);
    const overall = stubScores.reduce((s, v) => s + v, 0) / stubScores.length;
    const stubSources: SourceClaim[] = [{ type: "llm", retrieved_at: scoredAt }];
    return {
      axes: stubAxes,
      assertions: stubAssertions,
      sigma,
      overall,
      verdict: determineVerdict(stubAxes, stubAssertions, sigma, overall, false, false),
      sources: stubSources,
      scored_at: scoredAt,
      model_used: "mock-mode-stub",
    };
  }

  const response = await client.messages.create({
    model: SCORING_MODEL,
    // temperature: 0 makes the scoring deterministic so the same envelope
    // gets the same verdict on a re-score. Non-zero temperature was producing
    // ~30% verdict variance on identical inputs in the May-2026 POC.
    temperature: 0,
    max_tokens: 2048,
    system: buildScoringSystemPrompt(),
    tools: [SCORING_TOOL_SCHEMA],
    tool_choice: { type: "any" },
    messages: [
      {
        role: "user",
        content: `Score the following tool-call result:\n\n${content}`,
      },
    ],
  });

  // Extract the tool-use block
  const toolUseBlock = response.content.find((b) => b.type === "tool_use");
  if (!toolUseBlock || toolUseBlock.type !== "tool_use") {
    throw new Error(
      `Scoring model did not call submit_score. stop_reason=${response.stop_reason}`
    );
  }

  const raw = parseToolUseInput(toolUseBlock.input);
  const axes = validateAxes(raw.axes);
  const assertions = validateAssertions(raw.assertions);

  const axisValues = axes.map((a) => a.score);
  // σ over content axes only — safety is a separate floor gate, not a quality axis.
  // See computeContentSigma rationale.
  const sigma = computeContentSigma(axes);
  const overall = axisValues.reduce((s, v) => s + v, 0) / axisValues.length;

  const verdict = determineVerdict(
    axes,
    assertions,
    sigma,
    overall,
    raw.registry_mismatch ?? false,
    raw.technique_stale ?? false
  );

  // Collect sources from all rationale text (MVP: URL extraction)
  const rationaleText = [
    ...axes.map((a) => a.rationale),
    ...assertions.map((a) => a.rationale),
  ].join(" ");
  const sources = extractSources(rationaleText);

  const result = ScoringVerdictSchema.parse({
    axes,
    assertions,
    sigma,
    overall,
    verdict,
    sources,
    scored_at: scoredAt,
    model_used: SCORING_MODEL,
  });

  log.info(
    { verdict: result.verdict, sigma: result.sigma, overall: result.overall },
    "scoring complete"
  );

  return result;
}
