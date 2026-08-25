import { afterEach, describe, expect, it, vi } from "vitest";
import { APIProblem, requestJSON, setUnauthorizedHandler } from "./client";
import { emitAnalytics } from "./analytics";

afterEach(() => {
  setUnauthorizedHandler();
  vi.unstubAllGlobals();
});

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

  it("notifies the private application when a session expires", async () => {
    const expired = vi.fn();
    setUnauthorizedHandler(expired);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ detail: "Sign in again" }), {
      status: 401, headers: { "content-type": "application/problem+json" }
    })));

    await expect(requestJSON("/api/v1/accounts/account-a/work-items")).rejects.toEqual(expect.objectContaining<Partial<APIProblem>>({ status: 401 }));
    expect(expired).toHaveBeenCalledOnce();
    expect(expired).toHaveBeenCalledWith("/api/v1/accounts/account-a/work-items");
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
