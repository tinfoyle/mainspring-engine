import { afterEach, describe, expect, it, vi } from "vitest";
import { createBillingPortalSession, createCheckoutSession, createPurchaseCheckoutSession, getAITokenBalance, getBillingStatus, getPublicCatalog, redeemAITokenPromotion } from "./catalog";

afterEach(() => vi.unstubAllGlobals());

describe("catalog and checkout client", () => {
  it("loads the public Catalog and Account billing state", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ version: 4 }), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ can_start_checkout: true }), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ available: 10000 }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await getPublicCatalog();
    await getBillingStatus("account/id");
    await getAITokenBalance("account/id");

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/catalog/public");
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/api/v1/accounts/account%2Fid/billing");
    expect(fetchMock.mock.calls[2]?.[0]).toBe("/api/v1/accounts/account%2Fid/ai-tokens");
  });

  it("starts checkout with an opaque offer, optional referral, and stable request ID", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      session_id: "cs_test", url: "https://checkout.stripe.test/session", expires_at: "2026-08-24T12:00:00Z"
    }), { status: 201, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await createCheckoutSession("11111111-1111-4111-8111-111111111111", {
      offer_code: "team-monthly-v2", affiliate_code: "IO-PARTNER1"
    }, "22222222-2222-4222-8222-222222222222");

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("22222222-2222-4222-8222-222222222222");
    expect(init.body).toBe(JSON.stringify({ offer_code: "team-monthly-v2", affiliate_code: "IO-PARTNER1" }));
  });

  it("opens hosted billing management with a stable request ID", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      session_id: "bps_test", url: "https://billing.stripe.test/session", expires_at: "2026-08-24T12:00:00Z"
    }), { status: 201, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await createBillingPortalSession("account/id", "22222222-2222-4222-8222-222222222222");

    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/accounts/account%2Fid/billing-portal-sessions");
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("22222222-2222-4222-8222-222222222222");
  });

  it("starts a one-time purchase from an opaque Catalog item", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      session_id: "cs_purchase", url: "https://checkout.stripe.test/purchase", expires_at: "2026-08-24T12:00:00Z"
    }), { status: 201, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await createPurchaseCheckoutSession("account/id", { kind: "ai_token_top_up", item_code: "tokens_10k_v1" }, "22222222-2222-4222-8222-222222222222");

    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/accounts/account%2Fid/purchase-checkout-sessions");
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("22222222-2222-4222-8222-222222222222");
    expect(init.body).toBe(JSON.stringify({ kind: "ai_token_top_up", item_code: "tokens_10k_v1" }));
  });

  it("redeems an AI Token promotion with durable request identity", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      grant: { definition_code: "launch_bonus", catalog_version: 7, quantity: 1000000, expires_at: "2026-10-01T12:00:00Z", created_at: "2026-08-26T12:00:00Z" },
      balance: { available: 1000000, reserved: 0, consumed: 0, included: 0, purchased: 0, promotion: 1000000 }
    }), { status: 201, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await redeemAITokenPromotion("account/id", { promotion_code: "launch_bonus" }, "22222222-2222-4222-8222-222222222222");

    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/accounts/account%2Fid/ai-token-promotions");
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("22222222-2222-4222-8222-222222222222");
    expect(init.body).toBe(JSON.stringify({ promotion_code: "launch_bonus" }));
  });
});
