/**
 * registry-client.ts — Conforming Provenance Registry client (§ 12.1).
 *
 * Fetches trust-anchor records via the `registry/lookup` JSON-RPC 2.0 method.
 *
 * NOTE: This implementation uses the Node.js built-in `fetch` (Node >= 18).
 * For environments without global fetch, provide a custom fetcher via options.
 *
 * DRAFT — for spec validation only.
 */

import {
  type TrustAnchorRecord,
  type RevocationRecord,
  type RegistryLookupResponse,
} from "./types.js";
import { RegistryError, RECode, RE_JSON_RPC_CODES } from "./errors.js";

// ─── Types ──────────────────────────────────────────────────────────────────

export interface RegistryClientOptions {
  /**
   * Base URL of the Conforming Provenance Registry.
   * E.g., "https://registry.enchanterlabs.io"
   */
  endpoint: string;

  /**
   * Timeout in milliseconds for each request. Default: 10000.
   */
  timeoutMs?: number;

  /**
   * Optional custom HTTP fetcher for testing / alternative environments.
   * Defaults to global `fetch`.
   */
  fetcher?: typeof fetch;
}

/** Internal JSON-RPC 2.0 error shape. */
interface JsonRpcError {
  code: number;
  message: string;
  data?: {
    re_code?: string;
    detail?: string;
  };
}

/** Raw JSON-RPC response from registry. */
interface JsonRpcResponse {
  jsonrpc: string;
  id: number | string;
  result?: unknown;
  error?: JsonRpcError;
}

// ─── RE code reverse-map ────────────────────────────────────────────────────

const JSON_RPC_CODE_TO_RE: Readonly<Record<number, RECode>> = Object.fromEntries(
  Object.entries(RE_JSON_RPC_CODES).map(([k, v]) => [v, k as RECode]),
) as Record<number, RECode>;

// ─── RegistryClient ─────────────────────────────────────────────────────────

export class RegistryClient {
  private readonly endpoint: string;
  private readonly timeoutMs: number;
  private readonly fetcher: typeof fetch;
  private idCounter = 0;

  constructor(opts: RegistryClientOptions) {
    this.endpoint = opts.endpoint.replace(/\/$/, "");
    this.timeoutMs = opts.timeoutMs ?? 10_000;
    this.fetcher = opts.fetcher ?? globalThis.fetch.bind(globalThis);
  }

  /**
   * Perform a `registry/lookup` query (§ 12.1).
   *
   * @param toolId  - The `tool_id` from the provenance envelope.
   * @param keyId   - The `signature.protected_header.key_id` from the envelope.
   * @param atTime  - Optional RFC 3339 timestamp for point-in-time queries.
   * @returns       RegistryLookupResponse.
   * @throws        RegistryError on RE-xxx responses.
   */
  async lookup(
    toolId: string,
    keyId: string,
    atTime?: string,
  ): Promise<RegistryLookupResponse> {
    const params: Record<string, unknown> = {
      tool_id: toolId,
      key_id: keyId,
    };
    if (atTime !== undefined) {
      params["at_time"] = atTime;
    }

    const body = await this.sendRpc("registry/lookup", params);

    // Parse result
    const result = body as Record<string, unknown>;

    const found = Boolean(result["found"]);
    const record = found ? (result["record"] as TrustAnchorRecord | undefined) : undefined;
    const revocation = result["revocation"] as RevocationRecord | null | undefined;
    const feedRoot = String(result["feed_root"] ?? "");
    const responseAt = String(result["response_at"] ?? "");
    const registryId = String(result["registry_id"] ?? "");

    return {
      found,
      ...(record !== undefined && { record }),
      ...(revocation !== undefined && { revocation }),
      feed_root: feedRoot,
      response_at: responseAt,
      registry_id: registryId,
    };
  }

  /**
   * Fetch the signed trust-anchor feed (§ 12.4).
   * URL per § 12.4: `/.well-known/mcp-provenance/trust-anchors.json`
   */
  async fetchTrustAnchorFeed(): Promise<unknown> {
    const url = `${this.endpoint}/.well-known/mcp-provenance/trust-anchors.json`;

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);

    let response: Response;
    try {
      response = await this.fetcher(url, {
        method: "GET",
        headers: { Accept: "application/json" },
        signal: controller.signal,
      });
    } catch (err) {
      throw new RegistryError(
        RECode.FeedUnavailable,
        RE_JSON_RPC_CODES[RECode.FeedUnavailable] ?? -32804,
        `Network error fetching trust-anchor feed: ${String(err)}`,
      );
    } finally {
      clearTimeout(timer);
    }

    if (!response.ok) {
      throw new RegistryError(
        RECode.FeedUnavailable,
        RE_JSON_RPC_CODES[RECode.FeedUnavailable] ?? -32804,
        `HTTP ${response.status} fetching trust-anchor feed`,
      );
    }

    return response.json() as Promise<unknown>;
  }

  /**
   * Fetch registry discovery metadata (§ 12.8).
   * URL per § 12.8: `/.well-known/mcp-provenance/registry.json`
   */
  async fetchDiscoveryMetadata(): Promise<unknown> {
    const url = `${this.endpoint}/.well-known/mcp-provenance/registry.json`;

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);

    let response: Response;
    try {
      response = await this.fetcher(url, {
        method: "GET",
        headers: { Accept: "application/json" },
        signal: controller.signal,
      });
    } catch (err) {
      throw new RegistryError(
        RECode.FeedUnavailable,
        RE_JSON_RPC_CODES[RECode.FeedUnavailable] ?? -32804,
        `Network error fetching registry discovery metadata: ${String(err)}`,
      );
    } finally {
      clearTimeout(timer);
    }

    if (!response.ok) {
      throw new RegistryError(
        RECode.FeedUnavailable,
        RE_JSON_RPC_CODES[RECode.FeedUnavailable] ?? -32804,
        `HTTP ${response.status} fetching registry discovery metadata`,
      );
    }

    return response.json() as Promise<unknown>;
  }

  // ─── Internal ──────────────────────────────────────────────────────────────

  private async sendRpc(method: string, params: unknown): Promise<unknown> {
    const id = ++this.idCounter;
    const payload = JSON.stringify({
      jsonrpc: "2.0",
      id,
      method,
      params,
    });

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);

    let response: Response;
    try {
      response = await this.fetcher(this.endpoint, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json",
        },
        body: payload,
        signal: controller.signal,
      });
    } catch (err) {
      throw new RegistryError(
        RECode.FeedUnavailable,
        RE_JSON_RPC_CODES[RECode.FeedUnavailable] ?? -32804,
        `Network error during ${method}: ${String(err)}`,
      );
    } finally {
      clearTimeout(timer);
    }

    const rpc = (await response.json()) as JsonRpcResponse;

    if (rpc.error) {
      const errCode = rpc.error.code;
      const reCode: RECode =
        JSON_RPC_CODE_TO_RE[errCode] ??
        (rpc.error.data?.re_code as RECode | undefined) ??
        RECode.MethodNotSupported;
      throw new RegistryError(
        reCode,
        errCode,
        rpc.error.data?.detail ?? rpc.error.message,
      );
    }

    return rpc.result;
  }
}
