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
  rotateRecoveryCodes: vi.fn(), getPrivacyConsent: vi.fn(), emitAnalytics: vi.fn()
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
  api.getSecurityPosture.mockResolvedValue({ passkey_count: 0, recovery_codes_configured: false, recovery_codes_remaining: 0, owner_ready: false });
  api.getPasskeys.mockResolvedValue({ passkeys: [] });
  api.getRecoveryCodeStatus.mockResolvedValue({ configured: false, remaining: 0 });
  api.getPrivacyConsent.mockResolvedValue({ decided: true, analytics: true, marketing: false, renewal_required: false });
  api.emitAnalytics.mockResolvedValue(true);
});

describe("first-login account setup", () => {
  it("starts with a focused, accessible passkey step", async () => {
    const { wrapper } = await mountView();
    await flushPromises();
    expect(wrapper.get("h1").text()).toBe("Add a passkey");
    expect(wrapper.text()).toContain("Step 1 of 2");
    expect(wrapper.text()).toContain("You can keep signing in with Google or your password.");
    await expectNoAxeViolations(wrapper.element);
  });

  it("moves from passkey enrollment to recovery codes", async () => {
    webauthn.registerPasskey.mockResolvedValue({ id: "key", name: "Shop phone" });
    const { wrapper } = await mountView();
    await flushPromises();
    await wrapper.get("input").setValue("Shop phone");
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(webauthn.registerPasskey).toHaveBeenCalledWith("Shop phone");
    expect(wrapper.get("h1").text()).toBe("Save recovery codes");
    expect(wrapper.text()).toContain("Step 2 of 2");
  });

  it("shows one-time codes and requires an explicit saved confirmation", async () => {
    api.getSecurityPosture.mockResolvedValue({ passkey_count: 1, recovery_codes_configured: false, recovery_codes_remaining: 0, owner_ready: false });
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
    api.getSecurityPosture.mockResolvedValue({ passkey_count: 1, recovery_codes_configured: true, recovery_codes_remaining: 10, owner_ready: true });
    api.getPasskeys.mockResolvedValue({ passkeys: [{ id: "key", name: "Laptop" }] });
    api.getRecoveryCodeStatus.mockResolvedValue({ configured: true, remaining: 10 });
    const { router } = await mountView("//example.com/steal");
    await flushPromises();
    expect(router.currentRoute.value.path).toBe("/app/your-turn");
  });
});
