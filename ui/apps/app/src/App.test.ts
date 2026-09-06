// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App.vue";
import { applicationEntryPoint } from "./applicationEntry";
import { router } from "./router";
import { useSessionStore } from "./stores/session";
import { expectNoAxeViolations } from "./test/accessibility";

const analytics = vi.hoisted(() => ({
  emitAnalytics: vi.fn(), getPrivacyConsent: vi.fn(), getSecurityPosture: vi.fn(),
  listAgentBoardrooms: vi.fn(), listAttentionQueue: vi.fn(), getPasskeys: vi.fn(), getRecoveryCodeStatus: vi.fn(), logout: vi.fn()
}));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...analytics }));
const fetcher = vi.fn();
vi.stubGlobal("fetch", fetcher);

beforeEach(() => {
  sessionStorage.clear(); localStorage.clear();
  analytics.listAgentBoardrooms.mockReset().mockResolvedValue([]);
  analytics.listAttentionQueue.mockReset().mockResolvedValue([]);
  fetcher.mockReset().mockResolvedValue(new Response(JSON.stringify({ accounts: [] }), { status: 200 }));
  analytics.getPrivacyConsent.mockReset().mockResolvedValue({ decided: false, analytics: false, marketing: false, renewal_required: false });
  analytics.emitAnalytics.mockReset().mockResolvedValue(false);
  analytics.getSecurityPosture.mockReset().mockResolvedValue({ passkey_count: 0, recovery_codes_configured: false, recovery_codes_remaining: 0, mfa_method_count: 0, owner_ready: false });
  analytics.getPasskeys.mockReset().mockResolvedValue({ passkeys: [] });
  analytics.getRecoveryCodeStatus.mockReset().mockResolvedValue({ configured: false, remaining: 0 });
  analytics.logout.mockReset().mockResolvedValue(undefined);
});

describe("application shell", () => {
  it("opens a conversation workspace and keeps administration in Settings", async () => {
    await router.push("/app"); await router.isReady();
    const pinia = createPinia(), session = useSessionStore(pinia);
    session.loaded = true; vi.spyOn(session, "load").mockResolvedValue();
    const wrapper = mount(App, { global: { plugins: [pinia, router] } });
    expect(router.currentRoute.value.path).toBe("/app/workspace");
    expect(wrapper.get("nav").attributes("aria-label")).toBe("Workspace views");
    expect(wrapper.get("nav").text()).toContain("Needs you");
    expect(wrapper.get("nav").text()).not.toContain("Billing");
    expect(wrapper.get("nav").text()).not.toContain("Security");
    expect(wrapper.get('a[href="/app/settings"]').text()).toBe("Settings");
    expect(wrapper.get(".workspace-chat").attributes("role")).toBe("main");
    await expectNoAxeViolations(wrapper.element); wrapper.unmount();
  });

  it("adds enabled and read-only package areas to the Workspace group", async () => {
    await router.push("/app/your-turn");
    await router.isReady();
    const pinia = createPinia();
    const session = useSessionStore(pinia);
    session.accounts = [{
      account_id: "10000000-0000-4000-8000-000000000001",
      account_type: "paid", account_state: "active",
      account_version: 1,
      cell_id: "cell-a",
      display_name: "Northstar",
      placement_generation: 1,
      role: "owner",
      slug: "northstar",
      owner_enrollment_required: false,
      entitlements: {
        account_id: "10000000-0000-4000-8000-000000000001",
        catalog_version: 2,
        evaluated_at: "2026-08-25T12:00:00Z",
        version: 3,
        packages: [
          { code: "work", mode: "enabled", sources: ["subscription"], version: 1 },
          { code: "knowledge", mode: "read_only", sources: ["subscription"], version: 1 },
          { code: "agents", mode: "enabled", sources: ["subscription"], version: 1 },
          { code: "finance", mode: "enabled", sources: ["subscription"], version: 1 },
          { code: "integrations", mode: "enabled", sources: ["subscription"], version: 1 },
          { code: "marketing", mode: "suspended", sources: ["subscription"], version: 1 }
        ]
      }
    }];
    session.selectedID = session.accounts[0]?.account_id;
    session.loaded = true;
    vi.spyOn(session, "load").mockResolvedValue();
    const wrapper = mount(App, { global: { plugins: [pinia, router] } });
    const workspace = wrapper.get('[aria-label="Workspace views"]');
    expect(workspace.text()).toContain("Work");
    expect(workspace.text()).toContain("Knowledge");
    expect(workspace.text()).toContain("Documents");
    expect(workspace.text()).toContain("Schedules");
    expect(workspace.text()).toContain("Finance");
    expect(workspace.text()).not.toContain("Integrations");
    expect(workspace.text()).not.toContain("Marketing");
    expect(wrapper.find('a[href="/app/settings"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it("limits a restricted Account to recovery and privacy navigation", async () => {
	const account = {
		account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid" as const, account_state: "restricted" as const,
		account_version: 3, cell_id: "cell-a", display_name: "Northstar", placement_generation: 1,
		role: "owner" as const, slug: "northstar", owner_enrollment_required: false,
		entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-27T12:00:00Z", version: 3, packages: [] }
	};
	fetcher.mockResolvedValue(new Response(JSON.stringify({ user_id: "20000000-0000-4000-8000-000000000002", selected_account_id: account.account_id, accounts: [account] }), { status: 200 }));
	sessionStorage.setItem("spyglass_application_entered", "1");
	await router.push("/app/your-turn");
	await router.isReady();
	const pinia = createPinia();
	const session = useSessionStore(pinia); session.accounts = [account]; session.selectedID = account.account_id;
	const wrapper = mount(App, { global: { plugins: [pinia, router] } });
	await flushPromises();
	expect(router.currentRoute.value.path).toBe("/app/billing");
	expect(wrapper.get(".session-notice").text()).toContain("Billing, security, privacy, and data export");
    expect(wrapper.get("nav").text()).not.toContain("Work");
    await router.push("/app/settings"); await flushPromises();
    const settings = wrapper.get(".settings-page");
    expect(settings.text()).toContain("Billing");
    expect(settings.text()).toContain("Security");
    expect(settings.text()).toContain("Export your data");
    expect(settings.text()).toContain("Privacy");
    expect(settings.text()).not.toContain("Agents & tools");
    expect(settings.text()).not.toContain("Close account");
	wrapper.unmount();
  });

  it("takes an owner with incomplete setup to the focused wizard", async () => {
    const account = {
      account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid" as const, account_state: "active" as const,
      account_version: 1, cell_id: "cell-a", display_name: "Northstar", placement_generation: 1,
      role: "owner" as const, slug: "northstar", owner_enrollment_required: true,
      entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-27T12:00:00Z", version: 3, packages: [] }
    };
    fetcher.mockResolvedValue(new Response(JSON.stringify({
      user_id: "20000000-0000-4000-8000-000000000002", selected_account_id: account.account_id, accounts: [account]
    }), { status: 200 }));
    sessionStorage.setItem("spyglass_application_entered", "1");
    await router.push("/app/your-turn");
    await router.isReady();
    const wrapper = mount(App, { global: { plugins: [createPinia(), router] } });
    await flushPromises();
    expect(router.currentRoute.value.path).toBe("/app/setup");
    expect(router.currentRoute.value.query.return_to).toBe("/app/your-turn");
    expect(wrapper.get("h1").text()).toBe("How would you like to set up two-factor authentication?");
    expect(wrapper.find("nav").exists()).toBe(false);
    expect(wrapper.get(".workspace-shell").classes()).toContain("workspace-setup");
    wrapper.unmount();
  });

  it("records first application entry once per tab after authenticated session load", async () => {
    fetcher.mockResolvedValue(new Response(JSON.stringify({ user_id: "20000000-0000-4000-8000-000000000002", accounts: [] }), { status: 200 }));
    analytics.getPrivacyConsent.mockResolvedValue({ decided: true, analytics: true, marketing: false, renewal_required: false });
    analytics.emitAnalytics.mockResolvedValue(true);
    await router.push("/app/your-turn");
    await router.isReady();

    const first = mount(App, { global: { plugins: [createPinia(), router] } });
    await flushPromises();
    expect(analytics.emitAnalytics).toHaveBeenCalledOnce();
    expect(analytics.emitAnalytics).toHaveBeenCalledWith(true, {
      name: "application_entered",
      fields: { entry_point: "your_turn" }
    });
    expect(sessionStorage.getItem("spyglass_application_entered")).toBe("1");
    first.unmount();

    const repeated = mount(App, { global: { plugins: [createPinia(), router] } });
    await flushPromises();
    expect(analytics.emitAnalytics).toHaveBeenCalledOnce();
    repeated.unmount();
  });

  it("offers logout in Settings and ends the current session", async () => {
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);
    await router.push("/app/settings");
    await router.isReady();
    const wrapper = mount(App, { global: { plugins: [createPinia(), router] } });
    const button = wrapper.get(".settings-sign-out button");
    expect(button.text()).toContain("Sign out");
    expect(button.text()).not.toContain("See you next time");
    await button.trigger("click");
    await flushPromises();
    expect(analytics.logout).toHaveBeenCalledOnce();
    expect(assign).toHaveBeenCalledWith("/login?status=signed_out");
    assign.mockRestore();
    wrapper.unmount();
  });

  it("uses a bounded taxonomy for direct application entry", () => {
    expect(applicationEntryPoint("checkout")).toBe("checkout");
    expect(applicationEntryPoint("your-turn")).toBe("your_turn");
    expect(applicationEntryPoint("your-turn-detail")).toBe("your_turn");
    expect(applicationEntryPoint("affiliate")).toBe("deep_link");
    expect(applicationEntryPoint(undefined)).toBe("deep_link");
  });

  it("switches the working view and chat without a modal navigation drawer", async () => {
    await router.push("/app/work"); await router.isReady();
    const wrapper = mount(App, { attachTo: document.body, global: { plugins: [createPinia(), router] } });
    await flushPromises();
    const toggle = wrapper.get(".workspace-chat-toggle");
    await toggle.trigger("click");
    expect(wrapper.get(".workspace-body").classes()).toContain("workspace-mobile-chat");
    await toggle.trigger("click");
    expect(wrapper.get(".workspace-body").classes()).not.toContain("workspace-mobile-chat");
    expect(wrapper.find(".sidebar").exists()).toBe(false);
    wrapper.unmount();
  });
});
