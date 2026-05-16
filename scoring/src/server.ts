/**
 * server.ts — Fastify HTTP server exposing the Quality Oracle scoring API.
 *
 * Endpoints:
 *   POST /v1/score    — score a tool-call result, return ScoringVerdict
 *   GET  /v1/healthz  — liveness check
 *
 * Listens on :9090.
 */

import Fastify from "fastify";
import Anthropic from "@anthropic-ai/sdk";
import { pino } from "pino";
import { score, SCORING_MODEL } from "./score.js";
import { ToolCallResultSchema } from "./types.js";
import type { ScoringVerdict } from "./types.js";
import { ZodError } from "zod";

const PORT = 9090;

const log = pino({ name: "quality-oracle-server" });

// ---------------------------------------------------------------------------
// Anthropic client (singleton, reads ANTHROPIC_API_KEY from env)
// ---------------------------------------------------------------------------

function buildAnthropicClient(): Anthropic {
  const apiKey = process.env["ANTHROPIC_API_KEY"];
  if (!apiKey) {
    throw new Error(
      "ANTHROPIC_API_KEY environment variable is not set. " +
        "Export it before starting the server."
    );
  }
  return new Anthropic({ apiKey });
}

// ---------------------------------------------------------------------------
// Server bootstrap
// ---------------------------------------------------------------------------

export async function buildServer(): Promise<ReturnType<typeof Fastify>> {
  const anthropic = buildAnthropicClient();

  const app = Fastify({
    logger: false, // we use pino directly
  });

  // ---- POST /v1/score -------------------------------------------------------

  app.post<{
    Body: unknown;
    Reply: ScoringVerdict | { error: string; details?: unknown };
  }>("/v1/score", async (request, reply) => {
    let toolCall;
    try {
      toolCall = ToolCallResultSchema.parse(request.body);
    } catch (err) {
      if (err instanceof ZodError) {
        log.warn({ issues: err.issues }, "invalid request body");
        return reply.status(400).send({ error: "invalid_request", details: err.issues });
      }
      return reply.status(400).send({ error: "invalid_request" });
    }

    try {
      const verdict = await score(toolCall, anthropic);
      return reply.status(200).send(verdict);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      log.error({ err: message }, "scoring failed");
      return reply.status(502).send({ error: "scoring_failed", details: message });
    }
  });

  // ---- GET /v1/healthz ------------------------------------------------------

  app.get<{ Reply: { status: string; model: string } }>(
    "/v1/healthz",
    async (_request, reply) => {
      return reply.status(200).send({ status: "ok", model: SCORING_MODEL });
    }
  );

  return app;
}

// ---------------------------------------------------------------------------
// Entry point (when run directly via tsx)
// ---------------------------------------------------------------------------

async function main(): Promise<void> {
  const app = await buildServer();

  try {
    await app.listen({ port: PORT, host: "0.0.0.0" });
    log.info({ port: PORT }, "quality-oracle-scoring server listening");
  } catch (err) {
    log.error({ err }, "server failed to start");
    process.exit(1);
  }
}

main().catch((err: unknown) => {
  console.error("Fatal:", err);
  process.exit(1);
});
