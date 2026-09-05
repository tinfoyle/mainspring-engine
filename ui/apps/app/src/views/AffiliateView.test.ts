// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { expectNoAxeViolations } from "../test/accessibility";
import AffiliateView from "./AffiliateView.vue";

const api = vi.hoisted(() => ({
  cancelAffiliateSupportRequest: vi.fn(),
  enrollAffiliate: vi.fn(),
  getAffiliateProgram: vi.fn(),
  getAffiliateStatement: vi.fn(),
  getAffiliateSupportRequests: vi.fn(),
  replaceAffiliateCode: vi.fn(),
  submitAffiliateSupportRequest: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => ({ ...await importOriginal<typeof import("@spyglass/api")>(), ...api }));

async function mountView() {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: "/app/affiliate", component: AffiliateView },
    { path: "/app/privacy", component: { template: "<main>Privacy</main>" } }
  ] });
  await router.push("/app/affiliate");
  await router.isReady();
  const wrapper = mount(AffiliateView, { global: { plugins: [router] } });
  await flushPromises();
  await expectNoAxeViolations(wrapper.element);
  return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
  api.getAffiliateProgram.mockReset();
  api.getAffiliateStatement.mockReset();
  api.getAffiliateSupportRequests.mockReset();
  api.getAffiliateSupportRequests.mockResolvedValue({ requests: [] });
  api.enrollAffiliate.mockReset();
  api.replaceAffiliateCode.mockReset();
  api.submitAffiliateSupportRequest.mockReset();
  api.cancelAffiliateSupportRequest.mockReset();
});

describe("Affiliate identity dashboard", () => {
  it("keeps verified-erasure evidence outside ordinary Affiliate access", async () => {
    api.getAffiliateProgram.mockResolvedValue({ enrollment_open: true, attribution_enabled: true, terms_version: 2,
      rule_version: 3, settlement_mode: "account_credit_with_support_check", retention_restricted: true });
    const wrapper = await mountView();
    expect(wrapper.text()).toContain("Affiliate records are restricted");
    expect(wrapper.text()).toContain("cannot enroll again");
    expect(wrapper.text()).toContain("seven-year period");
    expect(wrapper.text()).toContain("Referred customer subscriptions are unchanged");
    expect(wrapper.find('a[href="/app/privacy"]').exists()).toBe(true);
    expect(api.getAffiliateStatement).not.toHaveBeenCalled();
    expect(api.getAffiliateSupportRequests).not.toHaveBeenCalled();
  });

  it("does not advertise candidate economics while settlement is unresolved", async () => {
    api.getAffiliateProgram.mockResolvedValue({ enrollment_open: false, attribution_enabled: false, terms_version: 1, rule_version: 1, settlement_mode: "unconfigured" });
    const wrapper = await mountView();
    expect(wrapper.findAll("dd")[2]?.text()).toBe("Not approved");
    expect(wrapper.text()).toContain("The affiliate program is not open yet");
    expect(wrapper.text()).not.toContain("$10");
    expect(api.getAffiliateStatement).not.toHaveBeenCalled();
  });

  it("shows generated code and aggregate immutable entries without customer identity", async () => {
    api.getAffiliateProgram.mockResolvedValue({ enrollment_open: true, attribution_enabled: true, terms_version: 2, rule_version: 3, settlement_mode: "account_credit", enrollment: {
      affiliate_id: "10000000-0000-4000-8000-000000000001", user_id: "20000000-0000-4000-8000-000000000002", public_code: "IO-PARTNER1", terms_version: 2, rule_version: 3, state: "active", version: 1, created_at: "2026-08-24T20:00:00Z"
    } });
    api.getAffiliateStatement.mockResolvedValue({ affiliate_id: "10000000-0000-4000-8000-000000000001", referred_subscriptions: 4, currency: "USD", pending_minor: 1000, available_minor: 2000, reserved_minor: 0, settled_minor: 0, reversed_minor: 0, voided_minor: 0, check_threshold_minor: 10000, check_eligible: false, entries: [{
      entry_id: "30000000-0000-4000-8000-000000000003", affiliate_id: "10000000-0000-4000-8000-000000000001", attribution_id: "40000000-0000-4000-8000-000000000004", rule_version: 3, cycle: 2, kind: "earned", state: "pending", amount_minor: 1000, currency: "USD", available_at: "2026-09-24T20:00:00Z", created_at: "2026-08-24T20:00:00Z"
    }, {
      entry_id: "30000000-0000-4000-8000-000000000005", affiliate_id: "10000000-0000-4000-8000-000000000001", attribution_id: "40000000-0000-4000-8000-000000000004", rule_version: 3, cycle: 3, kind: "earned", state: "settled", amount_minor: 2000, currency: "USD", available_at: "2026-10-24T20:00:00Z", created_at: "2026-09-24T20:00:00Z"
    }] });
    const wrapper = await mountView();
    expect(wrapper.text()).toContain("IO-PARTNER1");
    expect(wrapper.get<HTMLInputElement>('#affiliate-referral-link').element.value).toBe(new URL("/app/checkout?ref=IO-PARTNER1", window.location.origin).href);
    expect(wrapper.text()).toContain("A link only proposes the code");
    expect(wrapper.findAll("button").some((button) => button.text() === "Copy referral link" && !button.attributes("disabled"))).toBe(true);
    expect(wrapper.text()).toContain("Qualifying cycle 2");
    expect(wrapper.text()).toContain("Referred subscriptions");
    expect(wrapper.text()).toContain("4");
    expect(wrapper.text()).toContain("August 2026");
    expect(wrapper.text()).toContain("September 2026");
    expect(wrapper.text()).toContain("$10.00");
    expect(wrapper.text()).not.toContain("referred customer@example.com");
    expect(wrapper.get('[aria-label="Commission totals"]').attributes("role")).toBe("group");
  });

  it("requires explicit confirmation and replaces only the current code version", async () => {
    api.getAffiliateProgram.mockResolvedValue({ enrollment_open: true, attribution_enabled: true, terms_version: 2, rule_version: 3, settlement_mode: "account_credit", enrollment: {
      affiliate_id: "10000000-0000-4000-8000-000000000001", user_id: "20000000-0000-4000-8000-000000000002", public_code: "IO-PARTNER1", terms_version: 2, rule_version: 3, state: "active", version: 4, created_at: "2026-08-24T20:00:00Z"
    } });
    api.getAffiliateStatement.mockResolvedValue({ affiliate_id: "10000000-0000-4000-8000-000000000001", referred_subscriptions: 0, currency: "", pending_minor: 0, available_minor: 0, reserved_minor: 0, settled_minor: 0, reversed_minor: 0, voided_minor: 0, check_threshold_minor: 10000, check_eligible: false, entries: [] });
    api.replaceAffiliateCode.mockResolvedValue({ enrollment_open: true, attribution_enabled: true, terms_version: 2, rule_version: 3, settlement_mode: "account_credit", enrollment: {
      affiliate_id: "10000000-0000-4000-8000-000000000001", user_id: "20000000-0000-4000-8000-000000000002", public_code: "IO-PARTNER2", terms_version: 2, rule_version: 3, state: "active", version: 5, created_at: "2026-08-24T20:00:00Z"
    } });

    const wrapper = await mountView();
    await wrapper.findAll("button").find((button) => button.text() === "Replace public code")?.trigger("click");
    expect(wrapper.text()).toContain("every link using it will stop creating future referrals");
    expect(api.replaceAffiliateCode).not.toHaveBeenCalled();
    await wrapper.findAll("button").find((button) => button.text() === "Confirm code replacement")?.trigger("click");
    await flushPromises();
    expect(api.replaceAffiliateCode).toHaveBeenCalledWith({ expected_version: 4 });
    expect(wrapper.text()).toContain("IO-PARTNER2");
    expect(wrapper.text()).toContain("Existing subscription credit is unchanged");
  });

  it("keeps historical ledger access while disabling a suspended referral code", async () => {
    api.getAffiliateProgram.mockResolvedValue({ enrollment_open: false, attribution_enabled: false, terms_version: 2, rule_version: 3, settlement_mode: "account_credit", enrollment: {
      affiliate_id: "10000000-0000-4000-8000-000000000001", user_id: "20000000-0000-4000-8000-000000000002", public_code: "IO-PAUSED1", terms_version: 2, rule_version: 3, state: "suspended", version: 2, created_at: "2026-08-24T20:00:00Z"
    } });
    api.getAffiliateStatement.mockResolvedValue({ affiliate_id: "10000000-0000-4000-8000-000000000001", referred_subscriptions: 2, currency: "USD", pending_minor: 0, available_minor: 1000, reserved_minor: 0, settled_minor: 2000, reversed_minor: 1000, voided_minor: 0, check_threshold_minor: 10000, check_eligible: false, entries: [] });

    const wrapper = await mountView();
    expect(wrapper.text()).toContain("Referral attribution is paused");
    expect(wrapper.text()).toContain("historical commission records remain available");
    const copy = wrapper.findAll("button").find((button) => button.text() === "Copy code");
    expect(copy?.attributes("disabled")).toBeDefined();
    expect(wrapper.find("#affiliate-referral-link").exists()).toBe(false);
    expect(wrapper.findAll("button").find((button) => button.text() === "Copy referral link")?.attributes("disabled")).toBeDefined();
    expect(wrapper.text()).toContain("$20.00");
    expect(wrapper.text()).toContain("$10.00");
  });

  it("offers a structured enrollment appeal without a free-text or customer-data field", async () => {
    api.getAffiliateProgram.mockResolvedValue({ enrollment_open: false, attribution_enabled: false, terms_version: 2, rule_version: 3, settlement_mode: "account_credit", enrollment: {
      affiliate_id: "10000000-0000-4000-8000-000000000001", user_id: "20000000-0000-4000-8000-000000000002", public_code: "IO-PAUSED1", terms_version: 2, rule_version: 3, state: "suspended", version: 2, created_at: "2026-08-24T20:00:00Z"
    } });
    api.getAffiliateStatement.mockResolvedValue({ affiliate_id: "10000000-0000-4000-8000-000000000001", referred_subscriptions: 0, currency: "", pending_minor: 0, available_minor: 0, reserved_minor: 0, settled_minor: 0, reversed_minor: 0, voided_minor: 0, check_threshold_minor: 10000, check_eligible: false, entries: [] });
    api.submitAffiliateSupportRequest.mockResolvedValue({ request_id: "50000000-0000-4000-8000-000000000005", affiliate_id: "10000000-0000-4000-8000-000000000001", kind: "enrollment_appeal", state: "submitted", version: 1, created_at: "2026-08-25T12:00:00Z", updated_at: "2026-08-25T12:00:00Z" });

    const wrapper = await mountView();
    expect(wrapper.find("textarea").exists()).toBe(false);
    expect(wrapper.text()).toContain("Do not send customer names, payment details, or referred-business information");
    await wrapper.findAll("button").find((button) => button.text() === "Request status review")?.trigger("click");
    await flushPromises();
    expect(api.submitAffiliateSupportRequest).toHaveBeenCalledWith({ kind: "enrollment_appeal" });
    expect(wrapper.text()).toContain("Submitted");
  });
});
