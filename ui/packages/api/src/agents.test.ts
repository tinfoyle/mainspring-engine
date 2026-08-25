import { afterEach, describe, expect, it, vi } from "vitest";
import { listAgentConversations, startAgentRun } from "./agents";

afterEach(() => vi.unstubAllGlobals());

describe("Agents client", () => {
  it("drains opaque conversation pages", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [], next_cursor: "next-room-page" }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [] }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    await listAgentConversations("10000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000002");
    expect(String(fetcher.mock.calls[1]?.[0])).toContain("cursor=next-room-page");
  });

  it("keeps the same run idempotency key when an exact prompt is retried", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ detail: "Retry" }), { status: 503, headers: { "content-type": "application/problem+json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: "run" }), { status: 201, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    const input = { subject: "Launch review", prompt: "Review launch readiness.", mode: "selected" as const, persona_ids: ["30000000-0000-4000-8000-000000000003"] };
    await expect(startAgentRun("10000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000002", input)).rejects.toThrow("Retry");
    await startAgentRun("10000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000002", input);
    const first = new Headers(fetcher.mock.calls[0]?.[1]?.headers); const second = new Headers(fetcher.mock.calls[1]?.[1]?.headers);
    expect(first.get("Idempotency-Key")).toBeTruthy(); expect(second.get("Idempotency-Key")).toBe(first.get("Idempotency-Key"));
  });
});
