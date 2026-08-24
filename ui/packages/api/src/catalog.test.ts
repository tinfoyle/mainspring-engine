import { afterEach, describe, expect, it, vi } from "vitest";
import { createCheckoutSession, getBillingStatus, getPublicCatalog } from "./catalog";

afterEach(() => vi.unstubAllGlobals());

describe("catalog and checkout client", () => {
  it("loads the public Catalog and Account billing state", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ version: 4 }), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ can_start_checkout: true }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await getPublicCatalog();
    await getBillingStatus("account/id");

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/catalog/public");
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/api/v1/accounts/account%2Fid/billing");
  });

  it("starts checkout with an opaque offer, optional referral, and stable request ID", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      session_id: "cs_test", url: "https://checkout.stripe.test/session", expires_at: "2026-08-24T12:00:00Z"
    }), { status: 201, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await createCheckoutSession("11111111-1111-4111-8111-111111111111", {
      offer_code: "team-monthly-v1", affiliate_code: "IO-PARTNER1"
    }, "22222222-2222-4222-8222-222222222222");

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("22222222-2222-4222-8222-222222222222");
    expect(init.body).toBe(JSON.stringify({ offer_code: "team-monthly-v1", affiliate_code: "IO-PARTNER1" }));
  });
});
