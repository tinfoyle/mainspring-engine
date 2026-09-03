import { afterEach, describe, expect, it, vi } from "vitest";
import { APIProblem, isAPIProblem, requestJSON, setUnauthorizedHandler } from "./client";
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

describe("isAPIProblem", () => {
  it("recognizes API problems that crossed a JavaScript bundle boundary", () => {
    const foreignProblem = Object.assign(new Error("Request failed with status 404"), {
      name: "APIProblem",
      status: 404
    });

    expect(foreignProblem).not.toBeInstanceOf(APIProblem);
    expect(isAPIProblem(foreignProblem)).toBe(true);
    expect(isAPIProblem(new Error("ordinary failure"))).toBe(false);
  });
});

describe("emitAnalytics", () => {
  it("does not make a request without consent", async () => {
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    await expect(emitAnalytics(false, { name: "landing_viewed" })).resolves.toBe(false);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("uses a navigation-safe request without triggering the application request boundary", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(null, { status: 202 }));
    vi.stubGlobal("fetch", fetcher);

    await expect(emitAnalytics(true, { name: "signup_handoff_started", fields: { offer_code: "team-monthly-v1" } })).resolves.toBe(true);
    expect(fetcher).toHaveBeenCalledWith("/api/v1/analytics/events", expect.objectContaining({
      method: "POST",
      credentials: "same-origin",
      keepalive: true
    }));
  });

  it("reports a rejected optional event without invoking session-expiry navigation", async () => {
    const unauthorized = vi.fn();
    setUnauthorizedHandler(unauthorized);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 401 })));
    await expect(emitAnalytics(true, { name: "landing_viewed" })).resolves.toBe(false);
    expect(unauthorized).not.toHaveBeenCalled();
  });
});
