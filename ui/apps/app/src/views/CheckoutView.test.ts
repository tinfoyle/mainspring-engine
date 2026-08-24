// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, PublicCatalog } from "@spyglass/api";
import CheckoutView from "./CheckoutView.vue";
import { useSessionStore } from "../stores/session";

const api = vi.hoisted(() => ({
  createCheckoutSession: vi.fn(),
  getBillingStatus: vi.fn(),
  getPublicCatalog: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => {
  const original = await importOriginal<typeof import("@spyglass/api")>();
  return {
    ...original,
    ...api,
    getPrivacyConsent: vi.fn().mockResolvedValue({ decided: false, analytics: false, marketing: false, renewal_required: false }),
    emitAnalytics: vi.fn().mockResolvedValue(false)
  };
});

const account = {
  account_id: "10000000-0000-4000-8000-000000000001",
  account_type: "free",
  account_version: 1,
  cell_id: "cell-us-east-01",
  display_name: "Northstar Studio",
  placement_generation: 1,
  role: "owner",
  slug: "northstar-studio",
  owner_enrollment_required: false,
  entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-24T20:00:00Z", version: 3, packages: [] }
} satisfies AccountChoice;

const catalog = {
  version: 2,
  published_at: "2026-08-24T20:00:00Z",
  limits: [], packages: [],
  plans: [{ code: "team", version: 1, name: "Team", description: "A governed operating workspace.", packages: { work: "enabled", knowledge: "enabled" } }],
  offers: [{ code: "team-monthly-v1", plan_code: "team", plan_version: 1, currency: "USD", amount_minor: 4900, billing_interval: "month", effective_from: "2026-08-20T20:00:00Z" }]
} satisfies PublicCatalog;

beforeEach(() => {
  setActivePinia(createPinia());
  api.getPublicCatalog.mockReset().mockResolvedValue(catalog);
  api.getBillingStatus.mockReset().mockResolvedValue({ has_customer: false, can_manage: true, can_start_checkout: true, subscriptions: [] });
  api.createCheckoutSession.mockReset().mockRejectedValue(new Error("provider unavailable"));
});

describe("checkout review", () => {
  it("requires active referral application and explicit checkout confirmation", async () => {
    const session = useSessionStore();
    session.accounts = [account];
    session.selectedID = account.account_id;
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/checkout", component: CheckoutView }] });
    await router.push("/app/checkout?offer=team-monthly-v1&ref=IO-PARTNER1");
    await router.isReady();

    const wrapper = mount(CheckoutView, { global: { plugins: [router] } });
    await flushPromises();

    expect(wrapper.text()).toContain("A referral was proposed by your link");
    expect(wrapper.text()).toContain("$49.00 per month");
    expect(wrapper.get("button:disabled").text()).toBe("Continue to Stripe");

    await wrapper.findAll("button").find((button) => button.text() === "Apply")?.trigger("click");
    expect(wrapper.text()).toContain("Referral IO-PARTNER1 will be validated");
    await wrapper.get('input[type="checkbox"]').setValue(true);
    await wrapper.findAll("button").find((button) => button.text() === "Continue to Stripe")?.trigger("click");
    await flushPromises();

    expect(api.createCheckoutSession).toHaveBeenCalledWith(account.account_id, {
      offer_code: "team-monthly-v1", affiliate_code: "IO-PARTNER1"
    }, expect.stringMatching(/^[0-9a-f-]{36}$/));
    expect(wrapper.text()).toContain("provider unavailable");
    wrapper.unmount();
  });
});
