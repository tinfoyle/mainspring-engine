// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";
import BillingView from "./BillingView.vue";

const api = vi.hoisted(() => ({ createBillingPortalSession: vi.fn(), createPurchaseCheckoutSession: vi.fn(), getAITokenBalance: vi.fn(), getBillingStatus: vi.fn(), getPublicCatalog: vi.fn(), redeemAITokenPromotion: vi.fn() }));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));
const account = { account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_state: "active", account_version: 2, cell_id: "cell-a", display_name: "Northstar", placement_generation: 1, role: "owner", slug: "northstar", owner_enrollment_required: false, entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 4, evaluated_at: "2026-08-24T00:00:00Z", version: 1, packages: [] } } satisfies AccountChoice;
const status = { has_customer: true, can_manage: true, can_start_checkout: false, commissioning_purchased: false, subscriptions: [{ state: "active", offer_code: "team-monthly-v2", catalog_version: 4, current_period_start: "2026-08-01T00:00:00Z", current_period_end: "2026-09-01T00:00:00Z", last_synced_at: "2026-08-24T00:00:00Z" }] } as const;
beforeEach(() => { setActivePinia(createPinia()); Object.values(api).forEach((mock) => mock.mockReset()); api.getBillingStatus.mockResolvedValue(status); api.getAITokenBalance.mockResolvedValue({ available: 8700, reserved: 300, consumed: 1000, included: 8000, purchased: 500, promotion: 200 }); api.getPublicCatalog.mockResolvedValue({ version: 4, published_at: "2026-08-24T00:00:00Z", limits: [], packages: [], offers: [{ code: "team-monthly-v2", plan_code: "team", plan_version: 2, currency: "USD", amount_minor: 5000, billing_interval: "month", effective_from: "2026-08-01T00:00:00Z" }], plans: [{ code: "team", version: 2, name: "Infinite Ocean Team", description: "Complete team plan", packages: {} }], ai_token_renewal_grant: { code: "team_renewal_v1", version: 1, quantity: 10000, disclosure: "Included per renewal." }, ai_token_bundles: [{ code: "tokens_10k_v1", version: 1, quantity: 10000, currency: "USD", amount_minor: 1000, effective_from: "2026-08-01T00:00:00Z", disclosure: "Purchased tokens do not expire while active." }], commissioning_offer: { code: "commissioning_v1", version: 1, currency: "USD", amount_minor: 25000, effective_from: "2026-08-01T00:00:00Z", disclosure: "Collaborative setup for one team." }, ai_complexity_rates: [] }); api.createBillingPortalSession.mockRejectedValue(new Error("provider unavailable")); api.createPurchaseCheckoutSession.mockRejectedValue(new Error("purchase provider unavailable")); api.redeemAITokenPromotion.mockResolvedValue({ grant: { definition_code: "launch_bonus", catalog_version: 4, quantity: 1000, expires_at: "2026-10-01T00:00:00Z", created_at: "2026-08-27T00:00:00Z" }, balance: { available: 9700, reserved: 300, consumed: 1000, included: 8000, purchased: 500, promotion: 1200 } }); const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; });
describe("Account billing surface", () => {
  it("renders subscription and AI Token truth and reuses portal retry identity", async () => { const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/billing", component: BillingView }] }); await router.push("/app/billing"); await router.isReady(); const wrapper = mount(BillingView, { global: { plugins: [router] } }); await flushPromises(); expect(wrapper.text()).toContain("Infinite Ocean Team"); expect(wrapper.text()).toContain("8,700 available"); expect(wrapper.text()).toContain("10,000 Tokens · $10.00"); await expectNoAxeViolations(wrapper.element); const button = wrapper.findAll("button").find((item) => item.text() === "Manage in Stripe"); await button?.trigger("click"); await flushPromises(); await button?.trigger("click"); await flushPromises(); expect(api.createBillingPortalSession).toHaveBeenCalledTimes(2); expect(api.createBillingPortalSession.mock.calls[0]?.[1]).toBe(api.createBillingPortalSession.mock.calls[1]?.[1]); expect(wrapper.text()).toContain("provider unavailable"); });
  it("keeps billing management hidden from an ordinary member", async () => { const session = useSessionStore(); session.accounts = [{ ...account, role: "member" }]; api.getBillingStatus.mockResolvedValue({ ...status, can_manage: false }); const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/billing", component: BillingView }] }); await router.push("/app/billing"); await router.isReady(); const wrapper = mount(BillingView, { global: { plugins: [router] } }); await flushPromises(); expect(wrapper.text()).toContain("Billing administrator access required"); expect(wrapper.text()).not.toContain("Manage in Stripe"); });
  it("explains the governed recovery deadline without hiding Stripe recovery", async () => {
	api.getBillingStatus.mockResolvedValue({ ...status, lifecycle: { state: "restricted", trigger: "payment_failure", effective_at: "2026-08-01T00:00:00Z", restriction_at: "2026-08-08T00:00:00Z", delete_at: "2026-08-31T00:00:00Z" } });
	const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/billing", component: BillingView }] }); await router.push("/app/billing"); await router.isReady();
	const wrapper = mount(BillingView, { global: { plugins: [router] } }); await flushPromises();
	expect(wrapper.text()).toContain("Account access is restricted"); expect(wrapper.text()).toContain("Deletion handoff"); expect(wrapper.text()).toContain("Manage in Stripe"); await expectNoAxeViolations(wrapper.element);
  });
  it("starts Catalog-frozen token checkout with one retry identity", async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/billing", component: BillingView }] }); await router.push("/app/billing"); await router.isReady();
    const wrapper = mount(BillingView, { global: { plugins: [router] } }); await flushPromises();
    const buy = () => wrapper.findAll("button").find((item) => item.text() === "Buy Tokens");
    await buy()?.trigger("click"); await flushPromises(); await buy()?.trigger("click"); await flushPromises();
    expect(api.createPurchaseCheckoutSession).toHaveBeenCalledTimes(2);
    expect(api.createPurchaseCheckoutSession.mock.calls[0]?.[1]).toEqual({ kind: "ai_token_top_up", item_code: "tokens_10k_v1" });
    expect(api.createPurchaseCheckoutSession.mock.calls[1]?.[2]).toBe(api.createPurchaseCheckoutSession.mock.calls[0]?.[2]);
    expect(wrapper.text()).toContain("purchase provider unavailable");
  });
  it("redeems a normalized promotion and refreshes the visible shared balance", async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/billing", component: BillingView }] }); await router.push("/app/billing"); await router.isReady();
    const wrapper = mount(BillingView, { global: { plugins: [router] } }); await flushPromises();
    await wrapper.get("#promotion-code").setValue("Launch_Bonus"); await wrapper.get("form.billing-promotion").trigger("submit"); await flushPromises();
    expect(api.redeemAITokenPromotion).toHaveBeenCalledWith(account.account_id, { promotion_code: "launch_bonus" }, expect.stringMatching(/^[0-9a-f-]{36}$/));
    expect(wrapper.text()).toContain("9,700 available"); expect(wrapper.text()).toContain("1,000 promotional AI Tokens were added");
  });
  it("shows durable commissioning ownership instead of offering a duplicate purchase", async () => {
    api.getBillingStatus.mockResolvedValue({ ...status, commissioning_purchased: true });
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/billing", component: BillingView }] }); await router.push("/app/billing"); await router.isReady();
    const wrapper = mount(BillingView, { global: { plugins: [router] } }); await flushPromises();
    expect(wrapper.text()).toContain("Standard commissioning is already recorded");
    expect(wrapper.findAll("button").some((item) => item.text() === "Buy assisted setup")).toBe(false);
  });
});
