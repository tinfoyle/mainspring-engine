// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";
import SetupView from "./SetupView.vue";

const api = vi.hoisted(() => ({
  getSecurityPosture: vi.fn(), getPasskeys: vi.fn(), getRecoveryCodeStatus: vi.fn(),
  rotateRecoveryCodes: vi.fn(), getPrivacyConsent: vi.fn(), emitAnalytics: vi.fn(),
  beginMFAEnrollment: vi.fn(), completeMFAEnrollment: vi.fn()
}));
const webauthn = vi.hoisted(() => ({ registerPasskey: vi.fn() }));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));
vi.mock("../webauthn", () => webauthn);

async function mountView(returnTo = "/app/your-turn") {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/app/setup", component: SetupView },
      { path: "/app/your-turn", component: { template: "<h1>Your Turn</h1>" } },
      { path: "/app/work", component: { template: "<h1>Work</h1>" } }
    ]
  });
  const pinia = createPinia();
  const session = useSessionStore(pinia);
  session.load = vi.fn().mockResolvedValue(undefined);
  await router.push({ path: "/app/setup", query: { return_to: returnTo } });
  await router.isReady();
  return { wrapper: mount(SetupView, { global: { plugins: [pinia, router] } }), router, session };
}

beforeEach(() => {
  for (const mock of [...Object.values(api), ...Object.values(webauthn)]) mock.mockReset();
  api.getSecurityPosture.mockResolvedValue({ passkey_count: 0, recovery_codes_configured: false, recovery_codes_remaining: 0, mfa_method_count: 0, owner_ready: false });
  api.getPasskeys.mockResolvedValue({ passkeys: [] });
  api.getRecoveryCodeStatus.mockResolvedValue({ configured: false, remaining: 0 });
  api.getPrivacyConsent.mockResolvedValue({ decided: true, analytics: true, marketing: false, renewal_required: false });
  api.emitAnalytics.mockResolvedValue(true);
});

describe("first-login account setup", () => {
  it("starts with a plain, accessible choice of required factors", async () => {
    const { wrapper } = await mountView();
    await flushPromises();
    expect(wrapper.get("h1").text()).toBe("How would you like to set up two-factor authentication?");
    expect(wrapper.text()).toContain("Passkey Recommended");
    expect(wrapper.text()).toContain("Text message");
    expect(wrapper.text()).toContain("Email code Not recommended");
    await expectNoAxeViolations(wrapper.element);
  });

  it("moves from passkey enrollment to recovery codes", async () => {
    webauthn.registerPasskey.mockResolvedValue({ id: "key", name: "Shop phone" });
    const { wrapper } = await mountView();
    await flushPromises();
    await wrapper.findAll("button").find((button) => button.text().includes("Passkey"))?.trigger("click");
    await wrapper.get("input").setValue("Shop phone");
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(webauthn.registerPasskey).toHaveBeenCalledWith("Shop phone");
    expect(wrapper.get("h1").text()).toBe("Save recovery codes");
    expect(wrapper.text()).toContain("Step 3 of 3");
  });

  it("enrolls a text-message factor without asking for a passkey", async () => {
    api.beginMFAEnrollment.mockResolvedValue({ challenge_id: "30000000-0000-4000-8000-000000000003", kind: "sms", destination_hint: "phone ending in 0199", expires_at: "2026-08-31T12:10:00Z", development_code: "123456" });
    api.completeMFAEnrollment.mockResolvedValue({ id: "40000000-0000-4000-8000-000000000004", kind: "sms", destination_hint: "phone ending in 0199", created_at: "2026-08-31T12:00:00Z" });
    const { wrapper, router } = await mountView("/app/work"); await flushPromises();
    await wrapper.findAll("button").find((button) => button.text().includes("Text message"))?.trigger("click");
    expect(wrapper.text()).toContain("Reply STOP to opt out or HELP for help.");
    expect(wrapper.text()).toContain("Consent is not a condition of purchase.");
    expect(wrapper.get('a[href="https://www.infiniteocean.net/privacy"]').text()).toBe("Privacy Policy");
    expect(wrapper.get('a[href="https://www.infiniteocean.net/terms"]').text()).toBe("Terms and Conditions");
    await wrapper.get('input[type="tel"]').setValue("(202) 555-0199"); await wrapper.get("form").trigger("submit"); await flushPromises();
    expect(wrapper.text()).toContain("phone ending in 0199");
    expect((wrapper.get('input[autocomplete="one-time-code"]').element as HTMLInputElement).value).toBe("123456");
    await wrapper.get("form").trigger("submit"); await flushPromises();
    expect(api.completeMFAEnrollment).toHaveBeenCalledWith("30000000-0000-4000-8000-000000000003", "123456");
    expect(router.currentRoute.value.path).toBe("/app/work");
  });

  it("shows one-time codes and requires an explicit saved confirmation", async () => {
    api.getSecurityPosture.mockResolvedValue({ passkey_count: 1, recovery_codes_configured: false, recovery_codes_remaining: 0, mfa_method_count: 0, owner_ready: false });
    api.getPasskeys.mockResolvedValue({ passkeys: [{ id: "key", name: "Laptop" }] });
    api.rotateRecoveryCodes.mockResolvedValue({
      status: { configured: true, remaining: 10, version: 1 }, codes: ["ocean-one", "ocean-two"]
    });
    const { wrapper, router, session } = await mountView("/app/work");
    await flushPromises();
    await wrapper.findAll("button").find((button) => button.text() === "Create recovery codes")?.trigger("click");
    await flushPromises();
    expect(wrapper.text()).toContain("ocean-one");
    expect(wrapper.text()).toContain("This is the only time Spyglass can show these codes.");
    const finish = wrapper.findAll("button").find((button) => button.text() === "Finish setup");
    expect(finish?.attributes("disabled")).toBeDefined();
    await wrapper.get('input[type="checkbox"]').setValue(true);
    await finish?.trigger("click");
    await flushPromises();
    expect(api.emitAnalytics).toHaveBeenCalledWith(true, {
      name: "security_enrollment_completed", fields: { method: "passkey_recovery_codes" }
    });
    expect(session.load).toHaveBeenCalled();
    expect(router.currentRoute.value.path).toBe("/app/work");
  });

  it("never follows an external return address", async () => {
    api.getSecurityPosture.mockResolvedValue({ passkey_count: 1, recovery_codes_configured: true, recovery_codes_remaining: 10, mfa_method_count: 0, owner_ready: true });
    api.getPasskeys.mockResolvedValue({ passkeys: [{ id: "key", name: "Laptop" }] });
    api.getRecoveryCodeStatus.mockResolvedValue({ configured: true, remaining: 10 });
    const { router } = await mountView("//example.com/steal");
    await flushPromises();
    expect(router.currentRoute.value.path).toBe("/app/your-turn");
  });
});
