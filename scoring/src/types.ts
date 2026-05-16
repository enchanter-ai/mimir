/**
 * types.ts — Enchanter Labs Quality Oracle scoring types.
 *
 * These types flow through the scoring pipeline:
 *   ToolCallResult → score() → ScoringVerdict → Go issuer (signs envelope)
 */

import { z } from "zod";

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

/** The raw content block of a tool result (mirrors Anthropic's ToolResultBlockParam). */
export const ToolResultContentSchema = z.union([
  z.object({ type: z.literal("text"), text: z.string() }),
  z.object({
    type: z.literal("image"),
    source: z.object({
      type: z.enum(["base64", "url"]),
      media_type: z.string().optional(),
      data: z.string().optional(),
      url: z.string().optional(),
    }),
  }),
]);

/** The request params that were sent to the tool. */
export const ToolCallRequestSchema = z.object({
  tool_name: z.string(),
  tool_use_id: z.string(),
  input: z.record(z.string(), z.unknown()),
  model_id: z.string().optional(),
  prompt_version: z.string().optional(),
});

/** Full input to the scoring engine: the request that triggered the tool call
 *  plus the content returned by the tool. */
export const ToolCallResultSchema = z.object({
  request: ToolCallRequestSchema,
  result: z.object({
    tool_use_id: z.string(),
    content: z.array(ToolResultContentSchema),
    is_error: z.boolean().optional(),
  }),
  metadata: z
    .object({
      session_id: z.string().optional(),
      timestamp: z.string().optional(),
      target_model: z.string().optional(),
    })
    .optional(),
});

export type ToolResultContent = z.infer<typeof ToolResultContentSchema>;
export type ToolCallRequest = z.infer<typeof ToolCallRequestSchema>;
export type ToolCallResult = z.infer<typeof ToolCallResultSchema>;

// ---------------------------------------------------------------------------
// Axis scores
// ---------------------------------------------------------------------------

export const AXES = [
  "clarity",
  "specificity",
  "faithfulness_to_source",
  "safety",
  "structure",
] as const;

export type Axis = (typeof AXES)[number];

export const AxisScoreSchema = z.object({
  axis: z.enum(AXES),
  score: z.number().min(0).max(10),
  rationale: z.string(),
});

export type AxisScore = z.infer<typeof AxisScoreSchema>;

// ---------------------------------------------------------------------------
// SAT-style assertions
// ---------------------------------------------------------------------------

// Mimir SAT assertions — quality gates for TOOL-CALL RESULTS.
// These differ from Wixie's prompt-quality assertions: Mimir scores results
// produced by tool calls, not the prompts that drive them. Each assertion
// must be answerable as PASS/FAIL by reading only the request + result.
export const ASSERTIONS = [
  "request_addressed",
  "cites_source",
  "no_hallucination_markers",
  "no_sycophancy",
  "no_hedges",
  "complete_for_request",
  "format_matches_request",
  "bounded_uncertainty",
] as const;

export type Assertion = (typeof ASSERTIONS)[number];

export const AssertionResultSchema = z.object({
  assertion: z.enum(ASSERTIONS),
  passed: z.boolean(),
  rationale: z.string(),
});

export type AssertionResult = z.infer<typeof AssertionResultSchema>;

// ---------------------------------------------------------------------------
// Source claims (MVP: URL extraction from rationale; production: full tracking)
// ---------------------------------------------------------------------------

export const SourceClaimSchema = z.object({
  type: z.string(),
  url: z.string().optional(),
  retrieved_at: z.string(),
  hash: z.string().optional(),
});

export type SourceClaim = z.infer<typeof SourceClaimSchema>;

// ---------------------------------------------------------------------------
// Verdict
// ---------------------------------------------------------------------------

export const VERDICTS = ["DEPLOY", "HOLD", "FAIL"] as const;
export type Verdict = (typeof VERDICTS)[number];

export const ScoringVerdictSchema = z.object({
  axes: z.array(AxisScoreSchema).length(5),
  assertions: z.array(AssertionResultSchema).length(8),
  sigma: z.number().min(0),
  overall: z.number().min(0).max(10),
  verdict: z.enum(VERDICTS),
  sources: z.array(SourceClaimSchema),
  scored_at: z.string(),
  model_used: z.string(),
});

export type ScoringVerdict = z.infer<typeof ScoringVerdictSchema>;
