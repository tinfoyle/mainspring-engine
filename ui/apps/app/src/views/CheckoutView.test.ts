// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, PublicCatalog } from "@spyglass/api";
import CheckoutView from "./CheckoutView.vue";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";

const api = vi.hoisted(() => ({
  createCheckoutSession: vi.fn(),
  emitAnalytics: vi.fn(),
  getBillingStatus: vi.fn(),
  getPrivacyConsent: vi.fn(),
  getPublicCatalog: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => {
  const original = await importOriginal<typeof import("@spyglass/api")>();
  return {
    ...original,
    ...api,
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
  api.getPrivacyConsent.mockReset().mockResolvedValue({ decided: false, analytics: false, marketing: false, renewal_required: false });
  api.emitAnalytics.mockReset().mockResolvedValue(false);
  api.createCheckoutSession.mockReset().mockRejectedValue(new Error("provider unavailable"));
});

afterEach(() => vi.useRealTimers());

async function mountCheckout(path = "/app/checkout?offer=team-monthly-v1") {
  const session = useSessionStore();
  session.accounts = [account];
  session.selectedID = account.account_id;
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/checkout", component: CheckoutView }] });
  await router.push(path);
  await router.isReady();
  const wrapper = mount(CheckoutView, { global: { plugins: [router] } });
  await flushPromises();
  return wrapper;
}

describe("checkout review", () => {
  it("requires active referral application and explicit checkout confirmation", async () => {
    const wrapper = await mountCheckout("/app/checkout?offer=team-monthly-v1&ref=IO-PARTNER1");
    await expectNoAxeViolations(wrapper.element);

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

  it("keeps checkout usable when optional consent lookup is unavailable or does not settle", async () => {
    api.getPrivacyConsent.mockReturnValue(new Promise(() => undefined));
    const wrapper = await mountCheckout();

    expect(wrapper.text()).toContain("$49.00 per month");
    expect(wrapper.text()).not.toContain("Checkout is unavailable");
    expect(api.getBillingStatus).toHaveBeenCalledWith(account.account_id);
    expect(api.emitAnalytics).not.toHaveBeenCalled();
  });

  it("records the rendered checkout review after slower consent resolves without delaying checkout", async () => {
    let resolveConsent: ((value: { decided: boolean; analytics: boolean; marketing: boolean; renewal_required: boolean }) => void) | undefined;
    api.getPrivacyConsent.mockReturnValue(new Promise((resolve) => { resolveConsent = resolve; }));
    api.emitAnalytics.mockResolvedValue(true);
    const wrapper = await mountCheckout();

    expect(wrapper.text()).toContain("$49.00 per month");
    expect(api.emitAnalytics).not.toHaveBeenCalled();

    resolveConsent?.({ decided: true, analytics: true, marketing: false, renewal_required: false });
    await flushPromises();
    expect(api.emitAnalytics).toHaveBeenCalledOnce();
    expect(api.emitAnalytics).toHaveBeenCalledWith(true, {
      name: "checkout_reviewed",
      fields: { offer_code: "team-monthly-v1", referral_present: "false" }
    });
  });

  it("preserves checkout-return outcomes until slower analytics consent resolves", async () => {
    let resolveConsent: ((value: { decided: boolean; analytics: boolean; marketing: boolean; renewal_required: boolean }) => void) | undefined;
    api.getPrivacyConsent.mockReturnValue(new Promise((resolve) => { resolveConsent = resolve; }));
    api.emitAnalytics.mockResolvedValue(true);
    api.getBillingStatus.mockResolvedValue({ has_customer: true, can_manage: true, can_start_checkout: false, subscriptions: [{
      offer_code: "team-monthly-v1", catalog_version: 2, state: "active", current_period_start: "2026-08-25T12:00:00Z", current_period_end: "2026-09-25T12:00:00Z", last_synced_at: "2026-08-25T12:01:00Z"
    }] });
    const wrapper = await mountCheckout("/app/checkout?offer=team-monthly-v1&status=billing");

    expect(wrapper.text()).toContain("Your subscription is active");
    expect(api.emitAnalytics).not.toHaveBeenCalled();

    resolveConsent?.({ decided: true, analytics: true, marketing: false, renewal_required: false });
    await flushPromises();
    expect(api.emitAnalytics).toHaveBeenCalledTimes(3);
    expect(api.emitAnalytics).toHaveBeenCalledWith(true, {
      name: "checkout_returned",
      fields: { offer_code: "team-monthly-v1", result: "returned" }
    });
    expect(api.emitAnalytics).toHaveBeenCalledWith(true, {
      name: "subscription_projected",
      fields: { offer_code: "team-monthly-v1", result: "active" }
    });
    expect(api.emitAnalytics).toHaveBeenCalledWith(true, {
      name: "checkout_reviewed",
      fields: { offer_code: "team-monthly-v1", referral_present: "false" }
    });
    wrapper.unmount();
  });

  it("does not silently substitute a different offer when signup intent is no longer published", async () => {
    api.getBillingStatus.mockResolvedValue({ has_customer: true, can_manage: true, can_start_checkout: false, subscriptions: [{
      offer_code: "team-monthly-v1", catalog_version: 2, state: "active", last_synced_at: "2026-08-25T12:01:00Z"
    }] });
    const wrapper = await mountCheckout("/app/checkout?offer=retired-annual-v1&status=billing");

    expect(wrapper.text()).toContain("offer selected before signup is no longer available");
    expect(wrapper.get<HTMLSelectElement>("#checkout-offer").element.value).toBe("");
    expect(wrapper.findAll("button").find((button) => button.text() === "Continue to Stripe")?.attributes("disabled")).toBeDefined();
    expect(wrapper.text()).not.toContain("Your subscription is active");

    await wrapper.get("#checkout-offer").setValue("team-monthly-v1");
    expect(wrapper.text()).not.toContain("offer selected before signup is no longer available");
    expect(wrapper.get<HTMLSelectElement>("#checkout-offer").element.value).toBe("team-monthly-v1");
    wrapper.unmount();
  });

  it("reuses one checkout request identity after a recoverable provider failure", async () => {
    const wrapper = await mountCheckout();
    await wrapper.get('input[type="checkbox"]').setValue(true);
    const continueButton = () => wrapper.findAll("button").find((button) => button.text() === "Continue to Stripe");

    await continueButton()?.trigger("click");
    await flushPromises();
    await continueButton()?.trigger("click");
    await flushPromises();

    expect(api.createCheckoutSession).toHaveBeenCalledTimes(2);
    const firstID = api.createCheckoutSession.mock.calls[0]?.[2];
    const secondID = api.createCheckoutSession.mock.calls[1]?.[2];
    expect(firstID).toMatch(/^[0-9a-f-]{36}$/);
    expect(secondID).toBe(firstID);
  });

  it("rejects a non-HTTPS hosted checkout destination", async () => {
    api.createCheckoutSession.mockResolvedValue({ session_id: "cs_unsafe", url: "http://checkout.invalid/session", expires_at: "2026-08-25T12:00:00Z" });
    const wrapper = await mountCheckout();
    await wrapper.get('input[type="checkbox"]').setValue(true);
    await wrapper.findAll("button").find((button) => button.text() === "Continue to Stripe")?.trigger("click");
    await flushPromises();

    expect(wrapper.text()).toContain("unsafe destination");
    expect(api.emitAnalytics).not.toHaveBeenCalledWith(true, expect.objectContaining({ name: "checkout_redirected" }));
  });

  it("waits for a signed active projection instead of trusting the return redirect", async () => {
    vi.useFakeTimers();
    api.getBillingStatus
      .mockResolvedValueOnce({ has_customer: true, can_manage: true, can_start_checkout: false, subscriptions: [] })
      .mockResolvedValueOnce({ has_customer: true, can_manage: true, can_start_checkout: false, subscriptions: [{
        offer_code: "team-monthly-v1", catalog_version: 2, state: "active", current_period_start: "2026-08-25T12:00:00Z", current_period_end: "2026-09-25T12:00:00Z", last_synced_at: "2026-08-25T12:01:00Z"
      }] });
    const wrapper = await mountCheckout("/app/checkout?offer=team-monthly-v1&status=billing");
    expect(wrapper.text()).toContain("access is being confirmed");

    await vi.advanceTimersByTimeAsync(2500);
    await flushPromises();
    expect(wrapper.text()).toContain("Your subscription is active");
    expect(api.getBillingStatus).toHaveBeenCalledTimes(2);
    wrapper.unmount();
  });

  it("renders a failed signed projection without claiming paid access", async () => {
    api.getBillingStatus.mockResolvedValue({ has_customer: true, can_manage: true, can_start_checkout: true, subscriptions: [{
      offer_code: "team-monthly-v1", catalog_version: 2, state: "incomplete_expired", last_synced_at: "2026-08-25T12:01:00Z"
    }] });
    const wrapper = await mountCheckout("/app/checkout?offer=team-monthly-v1&status=billing");

    expect(wrapper.text()).toContain("subscription did not become active");
    expect(wrapper.text()).toContain("No paid access was granted");
    expect(wrapper.text()).not.toContain("Your subscription is active");
  });
});
