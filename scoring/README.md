# @enchanter-labs/quality-oracle-scoring

**MVP scoring engine** for the Enchanter Labs Quality Oracle AVS.

Wraps an Anthropic API call with the Enchanter Labs 5-axis + 8-assertion rubric and returns a structured `ScoringVerdict` that the Go issuer (P1) signs into a provenance envelope.

---

## Quickstart

```bash
export ANTHROPIC_API_KEY=sk-ant-...
make install
make dev            # starts Fastify on :9090
make demo           # POST sample/request.json, print verdict JSON
```

---

## API

### `POST /v1/score`

Score a tool-call result.

**Request body** — `ToolCallResult`:

```json
{
  "request": {
    "tool_name": "generate_prompt",
    "tool_use_id": "tu_001",
    "input": { "topic": "..." },
    "model_id": "claude-sonnet-4-6"
  },
  "result": {
    "tool_use_id": "tu_001",
    "content": [{ "type": "text", "text": "..." }]
  }
}
```

**Response** — `ScoringVerdict`:

```json
{
  "axes": [
    { "axis": "clarity", "score": 9.0, "rationale": "..." },
    ...
  ],
  "assertions": [
    { "assertion": "has_role", "passed": true, "rationale": "..." },
    ...
  ],
  "sigma": 0.0,
  "overall": 9.0,
  "verdict": "DEPLOY",
  "sources": [],
  "scored_at": "2026-05-13T10:00:00.000Z",
  "model_used": "claude-sonnet-4-6"
}
```

### `GET /v1/healthz`

```json
{ "status": "ok", "model": "claude-sonnet-4-6" }
```

---

## Verdict rules

| Verdict | Criteria |
|---------|----------|
| `DEPLOY` | σ < 0.45 **and** overall ≥ 9.0 **and** all 5 axes ≥ 7.0 **and** 8/8 assertions pass |
| `FAIL`  | `registry_mismatch` or `technique_stale` flagged by the model |
| `HOLD`  | All other cases |

σ = population standard deviation of the 5 axis scores.

---

## 5 Axes

| Axis | What it measures |
|------|-----------------|
| `clarity` | Unambiguous, immediately actionable result |
| `specificity` | Concrete detail, no placeholder filler |
| `faithfulness_to_source` | All facts grounded in input or cited; no hallucination |
| `safety` | No harmful / policy-violating content |
| `structure` | Format matches declared target model and schema |

## 8 SAT Assertions

`has_role` · `has_task` · `has_format` · `has_constraints` · `has_edge_cases` · `no_hedges` · `no_filler` · `has_structure`

---

## Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `ANTHROPIC_API_KEY` | Yes | Anthropic API key used for scoring calls |

---

## Development

```bash
make lint     # tsc --noEmit (type check only)
make test     # vitest run (unit tests, no API calls — client is mocked)
make build    # tsc (emit to dist/)
```

---

## MVP vs production

This MVP uses a **single `claude-sonnet-4-6` call** per score request.

Production will replace this with Wixie's full Opus/Sonnet/Haiku tier dispatch:
- **Opus** — orchestrator/judgment: selects scoring strategy
- **Sonnet** — executor: runs the convergence rubric
- **Haiku** — validator: checks metadata consistency, score freshness

State is **in-memory only** in this MVP. Production uses the Wixie inference substrate (`plugins/inference-engine/`) for persistent envelope archival and cross-session evidence accumulation.

Source tracking in MVP extracts URLs from rationale text. Production integrates the full E5 Static-Dynamic Dual Verification provenance chain.

---

## Architecture

```
POST /v1/score
      │
      ▼
ToolCallResultSchema.parse()   ← Zod input validation
      │
      ▼
score(toolCall, anthropic)
      │
      ├─ buildScoringSystemPrompt()  ← rubric.ts
      ├─ client.messages.create()    ← Anthropic SDK, tool_use forced
      ├─ validateAxes()              ← checks all 5 axes present
      ├─ validateAssertions()        ← checks all 8 assertions present
      ├─ computeSigma()
      ├─ determineVerdict()
      └─ extractSources()
      │
      ▼
ScoringVerdictSchema.parse()   ← Zod output validation
      │
      ▼
Response JSON → Go issuer (P1) signs envelope
```
