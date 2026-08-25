import { afterEach, describe, expect, it, vi } from "vitest";
import type { WorkItem } from "./generated/api-types";
import { listWork, transitionWork } from "./work";

afterEach(() => vi.unstubAllGlobals());

describe("Work client", () => {
  it("encodes Account-scoped filters without exposing transport fields", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [] }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    await listWork("10000000-0000-4000-8000-000000000001", { search: "launch plan", state: "in_progress", kind: "ticket", limit: 30 });
    expect(String(fetcher.mock.calls[0]?.[0])).toContain("q=launch+plan&state=in_progress&kind=ticket");
  });

  it("reuses an operation key on retry and binds the current item version", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ detail: "Retry" }), { status: 503, headers: { "content-type": "application/problem+json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ state: "done" }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    const item = { id: "20000000-0000-4000-8000-000000000002", version: 8 } as WorkItem;
    await expect(transitionWork("10000000-0000-4000-8000-000000000001", item, { to: "done", reason: "The result is verified." })).rejects.toThrow("Retry");
    await transitionWork("10000000-0000-4000-8000-000000000001", item, { to: "done", reason: "The result is verified." });
    const first = new Headers(fetcher.mock.calls[0]?.[1]?.headers);
    const second = new Headers(fetcher.mock.calls[1]?.[1]?.headers);
    expect(first.get("Idempotency-Key")).toBeTruthy();
    expect(second.get("Idempotency-Key")).toBe(first.get("Idempotency-Key"));
    expect(first.get("If-Match")).toBe('W/"8"');
  });
});
