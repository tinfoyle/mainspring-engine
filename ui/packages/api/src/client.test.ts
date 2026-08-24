import { afterEach, describe, expect, it, vi } from "vitest";
import { APIProblem, requestJSON } from "./client";
import { emitAnalytics } from "./analytics";

afterEach(() => vi.unstubAllGlobals());

describe("requestJSON", () => {
  it("uses same-origin credentials and typed JSON", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), {
      status: 200, headers: { "content-type": "application/json" }
    }));
    vi.stubGlobal("fetch", fetcher);
    await expect(requestJSON<{ ok: boolean }>("/api/test")).resolves.toEqual({ ok: true });
    expect(fetcher.mock.calls[0]?.[1]?.credentials).toBe("same-origin");
  });

  it("normalizes problem details", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ detail: "No access" }), {
      status: 403, headers: { "content-type": "application/problem+json" }
    })));
    await expect(requestJSON("/api/test")).rejects.toEqual(expect.objectContaining<Partial<APIProblem>>({ status: 403, message: "No access" }));
  });
});

describe("emitAnalytics", () => {
  it("does not make a request without consent", async () => {
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    await expect(emitAnalytics(false, { name: "landing_viewed" })).resolves.toBe(false);
    expect(fetcher).not.toHaveBeenCalled();
  });
});
