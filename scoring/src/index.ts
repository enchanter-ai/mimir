/**
 * index.ts — Barrel export for @enchanter-labs/quality-oracle-scoring.
 *
 * Public surface:
 *   - score()          — the main scoring function
 *   - computeSigma()   — σ computation (useful for test harness)
 *   - determineVerdict() — verdict logic (useful for test harness)
 *   - buildScoringSystemPrompt() — rubric prompt builder
 *   - Types re-exported for downstream consumers
 */

export { score, computeSigma, determineVerdict, extractSources, SCORING_MODEL } from "./score.js";
export { buildScoringSystemPrompt, SCORING_TOOL_SCHEMA, AXIS_DEFINITIONS, ASSERTION_DEFINITIONS } from "./rubric.js";
export type {
  ToolCallResult,
  ToolCallRequest,
  AxisScore,
  AssertionResult,
  ScoringVerdict,
  SourceClaim,
  Axis,
  Assertion,
  Verdict,
} from "./types.js";
export { ToolCallResultSchema, ScoringVerdictSchema, AXES, ASSERTIONS, VERDICTS } from "./types.js";
