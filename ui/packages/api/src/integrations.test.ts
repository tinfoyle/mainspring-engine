import { afterEach, describe, expect, it, vi } from "vitest";
import type { IntegrationConnection } from "./generated/api-types";
import { listIntegrationConnections, readIntegrationWeb, reviseIntegrationConnection, searchIntegrationWeb } from "./integrations";

afterEach(() => vi.unstubAllGlobals());
const connection = { id: "connection/id", account_id: "account", name: "Research", kind: "web_research", state: "active", current_revision_id: "revision", current_revision: 2, credential_id: "credential", credential_generation: 1, version: 4, created_by: { user_id: "user" }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" } satisfies IntegrationConnection;

describe("Integrations client", () => {
  it("preserves filters and binds versioned and capture commands", async () => {
    const fetcher = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(connection), { status: 200, headers: { "content-type": "application/json" } }))); vi.stubGlobal("fetch", fetcher);
    await listIntegrationConnections("account", { state: "active", kind: "web_research", cursor: "opaque+/=" });
    await reviseIntegrationConnection("account", connection, { name: "Research", capabilities: ["web.research"], scope: { https_origin: "https://example.com", path_prefix: "/docs" } });
    await searchIntegrationWeb("account", { connection_id: connection.id, query: "launch policy" });
    await readIntegrationWeb("account", { connection_id: connection.id, url: "https://example.com/docs/policy" });
    expect(String(fetcher.mock.calls[0]?.[0])).toContain("cursor=opaque%2B%2F%3D");
    expect(new Headers(fetcher.mock.calls[1]?.[1]?.headers).get("If-Match")).toBe('W/"4"');
    expect(new Headers(fetcher.mock.calls[1]?.[1]?.headers).get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
    expect(new Headers(fetcher.mock.calls[2]?.[1]?.headers).get("Idempotency-Key")).toBeNull();
    expect(new Headers(fetcher.mock.calls[3]?.[1]?.headers).get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("reuses one identity across an exact failed retry", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ title: "Unavailable" }), { status: 503, headers: { "content-type": "application/problem+json" } })); vi.stubGlobal("fetch", fetcher);
    const input = { name: "Research", capabilities: ["web.research"], scope: { https_origin: "https://example.com", path_prefix: "/docs" } } as const;
    await expect(reviseIntegrationConnection("account", connection, input)).rejects.toThrow();
    await expect(reviseIntegrationConnection("account", connection, input)).rejects.toThrow();
    expect(new Headers(fetcher.mock.calls[0]?.[1]?.headers).get("Idempotency-Key")).toBe(new Headers(fetcher.mock.calls[1]?.[1]?.headers).get("Idempotency-Key"));
  });
});
