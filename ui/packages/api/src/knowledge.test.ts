import { afterEach, describe, expect, it, vi } from "vitest";
import type { KnowledgeClaim } from "./generated/api-types";
import { decideKnowledgeClaim, listProposedKnowledgeClaims } from "./knowledge";

afterEach(() => vi.unstubAllGlobals());

describe("Knowledge client", () => {
  it("drains opaque claim cursors through a bounded client", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [], next_cursor: "opaque-page" }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [] }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    await listProposedKnowledgeClaims("10000000-0000-4000-8000-000000000001");
    expect(String(fetcher.mock.calls[1]?.[0])).toContain("cursor=opaque-page");
  });

  it("preserves decision idempotency and current claim version across retry", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ detail: "Retry" }), { status: 503, headers: { "content-type": "application/problem+json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ claim: { state: "accepted" } }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    const claim = { id: "20000000-0000-4000-8000-000000000002", version: 6 } as KnowledgeClaim;
    const input = { accept: true, reason: "The cited evidence supports this value." };
    await expect(decideKnowledgeClaim("10000000-0000-4000-8000-000000000001", claim, input)).rejects.toThrow("Retry");
    await decideKnowledgeClaim("10000000-0000-4000-8000-000000000001", claim, input);
    const first = new Headers(fetcher.mock.calls[0]?.[1]?.headers);
    const second = new Headers(fetcher.mock.calls[1]?.[1]?.headers);
    expect(first.get("Idempotency-Key")).toBe(second.get("Idempotency-Key"));
    expect(first.get("If-Match")).toBe('W/"6"');
  });
});
