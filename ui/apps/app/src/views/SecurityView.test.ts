// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";
import { expectNoAxeViolations } from "../test/accessibility";
import { useSessionStore } from "../stores/session";
import SecurityView from "./SecurityView.vue";

const api = vi.hoisted(() => ({
  getCurrentIdentity: vi.fn(), getSecurityPosture: vi.fn(), getPasskeys: vi.fn(), getRecoveryCodeStatus: vi.fn(),
  getMFAMethods: vi.fn(), beginMFAReauthentication: vi.fn(), completeMFAReauthentication: vi.fn(),
  getActiveSessions: vi.fn(), getSecurityEvents: vi.fn(), getSupportAccessHistory: vi.fn(), getMCPGrants: vi.fn(), confirmPassword: vi.fn(),
  beginContactChange: vi.fn(), renamePasskey: vi.fn(), deletePasskey: vi.fn(), compromisePasskey: vi.fn(),
  rotateRecoveryCodes: vi.fn(), consumeRecoveryCode: vi.fn(), revokeSession: vi.fn(), revokeAllSessions: vi.fn(), revokeMCPGrant: vi.fn(),
  getPrivacyConsent: vi.fn(), emitAnalytics: vi.fn()
}));
const webauthn = vi.hoisted(() => ({ registerPasskey: vi.fn(), reauthenticateWithPasskey: vi.fn() }));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));
vi.mock("../webauthn", () => webauthn);

const currentSession = { id: "10000000-0000-4000-8000-000000000001", client_label: "Firefox on laptop", authenticated_at: "2026-08-24T20:00:00Z", reauthenticated_at: "2026-08-24T20:00:00Z", last_seen_at: "2026-08-24T20:00:00Z", expires_at: "2026-09-24T20:00:00Z", current: true, authentication_method: "password", authentication_assurance: "single_factor", reauthentication_method: "password", reauthentication_assurance: "single_factor" } as const;
async function mountView(path = "/app/security") { const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/security", component: SecurityView }] }); const pinia = createPinia(); useSessionStore(pinia).selectedID = "30000000-0000-4000-8000-000000000003"; await router.push(path); await router.isReady(); return mount(SecurityView, { global: { plugins: [pinia, router] } }); }
beforeEach(() => {
  for (const mock of [...Object.values(api), ...Object.values(webauthn)]) mock.mockReset();
  api.getCurrentIdentity.mockResolvedValue({ user_id: "20000000-0000-4000-8000-000000000002", primary_email: "owner@example.com" });
  api.getSecurityPosture.mockResolvedValue({ passkey_count: 1, recovery_codes_configured: true, recovery_codes_remaining: 8, mfa_method_count: 0, owner_ready: true });
  api.getMFAMethods.mockResolvedValue({ methods: [] });
  api.getPasskeys.mockResolvedValue({ passkeys: [{ id: "key", name: "Laptop", created_at: "2026-08-24T20:00:00Z", backup_eligible: true, backed_up: true }] });
  api.getRecoveryCodeStatus.mockResolvedValue({ configured: true, version: 1, remaining: 8, created_at: "2026-08-24T20:00:00Z" });
  api.getActiveSessions.mockResolvedValue({ sessions: [currentSession] }); api.getSecurityEvents.mockResolvedValue({ events: [{ type: "passkey_added", occurred_at: "2026-08-24T20:00:00Z" }] });
  api.getSupportAccessHistory.mockResolvedValue({ events: [{ id: "40000000-0000-4000-8000-000000000004", staff_display_name: "Support Person", action: "support_view_opened", ticket: "SUP-1042", reason: "Customer asked for billing help", occurred_at: "2026-08-24T20:00:00Z" }] });
  api.getMCPGrants.mockResolvedValue({ grants: [{ grant_id: "30000000-0000-4000-8000-000000000003", client_id: "client", client_name: "Codex", created_at: "2026-08-24T20:00:00Z" }] });
  api.getPrivacyConsent.mockResolvedValue({ decided: true, analytics: true, marketing: false, renewal_required: false });
  api.emitAnalytics.mockResolvedValue(true);
});
describe("Security surface", () => {
  it("explains a checkout reauthentication handoff before showing security methods", async () => {
    const wrapper = await mountView("/app/security?return_to=%2Fapp%2Fcheckout&status=strong_reauthentication_required"); await flushPromises();
    const notice = wrapper.get('[aria-labelledby="checkout-reauthentication-title"]');
    expect(notice.text()).toContain("Checkout needs a quick security check");
    expect(notice.text()).toContain("Password confirmation does not authorize checkout");
    expect(notice.text()).toContain("return you to Checkout automatically");
    expect(notice.get('a[href="#security-confirmation"]')).toBeDefined();
    await expectNoAxeViolations(wrapper.element);
  });
  it("renders the complete identity boundary without Account coupling", async () => {
    const wrapper = await mountView(); await flushPromises();
    expect(wrapper.text()).toContain("owner@example.com"); expect(wrapper.text()).toContain("Laptop");
    expect(wrapper.text()).toContain("Firefox on laptop"); expect(wrapper.text()).toContain("Codex"); expect(wrapper.text()).toContain("Passkey added");
    expect(wrapper.text()).toContain("Support Person"); expect(wrapper.text()).toContain("SUP-1042");
    await expectNoAxeViolations(wrapper.element);
  });
  it("keeps recovery codes visible exactly after rotation", async () => {
    api.rotateRecoveryCodes.mockResolvedValue({ status: { configured: true, version: 2, remaining: 10 }, codes: ["a", "b"] });
    const wrapper = await mountView(); await flushPromises();
    await wrapper.findAll("button").find((button) => button.text() === "Replace recovery codes")?.trigger("click"); await flushPromises();
    expect(wrapper.text()).toContain("Save these now"); expect(wrapper.text()).toContain("a"); expect(wrapper.text()).toContain("b");
    expect(api.emitAnalytics).not.toHaveBeenCalled();
  });
  it("records first owner-security completion only after authoritative readiness", async () => {
    api.getSecurityPosture
      .mockResolvedValueOnce({ passkey_count: 1, recovery_codes_configured: false, recovery_codes_remaining: 0, mfa_method_count: 0, owner_ready: false })
      .mockResolvedValueOnce({ passkey_count: 1, recovery_codes_configured: true, recovery_codes_remaining: 10, mfa_method_count: 0, owner_ready: true });
    api.getRecoveryCodeStatus
      .mockResolvedValueOnce({ configured: false, version: 0, remaining: 0 })
      .mockResolvedValueOnce({ configured: true, version: 1, remaining: 10 });
    api.rotateRecoveryCodes.mockResolvedValue({ status: { configured: true, version: 1, remaining: 10 }, codes: ["first-code"] });

    const wrapper = await mountView(); await flushPromises();
    await wrapper.findAll("button").find((button) => button.text() === "Create recovery codes")?.trigger("click"); await flushPromises();

    expect(api.getSecurityPosture).toHaveBeenCalledTimes(2);
    expect(api.emitAnalytics).toHaveBeenCalledOnce();
    expect(api.emitAnalytics).toHaveBeenCalledWith(true, {
      name: "security_enrollment_completed",
      fields: { method: "passkey_recovery_codes" }
    });
    expect(api.getSecurityPosture.mock.invocationCallOrder.at(-1)).toBeLessThan(api.emitAnalytics.mock.invocationCallOrder[0]!);
  });
  it("requires a typed phrase before signing out everywhere", async () => {
    api.revokeAllSessions.mockResolvedValue(undefined); const assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);
    const wrapper = await mountView(); await flushPromises();
    await wrapper.findAll("button").find((button) => button.text() === "Sign out everywhere")?.trigger("click");
    expect(wrapper.get("form[role=dialog] button[type=submit]").attributes("disabled")).toBeDefined();
    await wrapper.get("form[role=dialog] input").setValue("SIGN OUT"); await wrapper.get("form[role=dialog]").trigger("submit"); await flushPromises();
    expect(api.revokeAllSessions).toHaveBeenCalled(); expect(assign).toHaveBeenCalled(); assign.mockRestore();
  });
});
