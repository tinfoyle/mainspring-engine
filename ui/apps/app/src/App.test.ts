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
  getPasskeys: vi.fn(), getRecoveryCodeStatus: vi.fn(), logout: vi.fn()
}));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...analytics }));
const fetcher = vi.fn();
vi.stubGlobal("fetch", fetcher);

beforeEach(() => {
  sessionStorage.clear();
  fetcher.mockReset().mockResolvedValue(new Response(JSON.stringify({ accounts: [] }), { status: 200 }));
  analytics.getPrivacyConsent.mockReset().mockResolvedValue({ decided: false, analytics: false, marketing: false, renewal_required: false });
  analytics.emitAnalytics.mockReset().mockResolvedValue(false);
  analytics.getSecurityPosture.mockReset().mockResolvedValue({ passkey_count: 0, recovery_codes_configured: false, recovery_codes_remaining: 0, mfa_method_count: 0, owner_ready: false });
  analytics.getPasskeys.mockReset().mockResolvedValue({ passkeys: [] });
  analytics.getRecoveryCodeStatus.mockReset().mockResolvedValue({ configured: false, remaining: 0 });
  analytics.logout.mockReset().mockResolvedValue(undefined);
});

describe("application shell", () => {
  it("makes Your Turn the default and keeps unavailable packages out of the mobile menu", async () => {
    await router.push("/app");
    await router.isReady();
    const pinia = createPinia();
    const session = useSessionStore(pinia);
    session.loaded = true;
    vi.spyOn(session, "load").mockResolvedValue();
    const wrapper = mount(App, { global: { plugins: [pinia, router] } });
    expect(wrapper.get("h1").text()).toBe("Your Turn");
    expect(wrapper.get("nav").attributes("aria-label")).toBe("Main navigation");
    expect(wrapper.get("nav").text()).toContain("Workspace");
    expect(wrapper.get("nav").text()).toContain("Your Turn");
    expect(wrapper.get("nav").text()).toContain("Baseline");
    expect(wrapper.get("nav").text()).toContain("Explore plans");
    expect(wrapper.get("nav").text()).not.toContain("Schedules");
    expect(wrapper.get("nav").text()).not.toContain("Finance");
    expect(wrapper.get("nav").text()).not.toContain("Integrations");
    expect(wrapper.get("nav").text()).toContain("Account");
    expect(wrapper.get("nav").text()).toContain("Billing");
    expect(wrapper.get("nav").text()).toContain("Security");
    expect(wrapper.get("nav").text()).toContain("Exports");
    expect(wrapper.get("nav").text()).toContain("Lifecycle");
    expect(wrapper.find(".nav-link em").exists()).toBe(false);
    await expectNoAxeViolations(wrapper.element);
    wrapper.unmount();
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
    const workspace = wrapper.get('[aria-labelledby="workspace-navigation-label"]');
    expect(workspace.text()).toContain("Work");
    expect(workspace.text()).toContain("Knowledge");
    expect(workspace.text()).toContain("Agents");
    expect(workspace.text()).toContain("Schedules");
    expect(workspace.text()).toContain("Finance");
    expect(workspace.text()).toContain("Integrations");
    expect(workspace.text()).not.toContain("Marketing");
    expect(workspace.text()).toContain("1 more areas");
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
	const navigation = wrapper.get("nav").text(); const links = wrapper.findAll(".nav-link").map((item) => item.text());
	expect(navigation).toContain("Billing"); expect(navigation).toContain("Security"); expect(navigation).toContain("Exports"); expect(navigation).toContain("Privacy");
	expect(links).not.toContain("Your Turn"); expect(links).not.toContain("Work"); expect(links).not.toContain("Affiliate"); expect(links).not.toContain("Lifecycle");
	expect(wrapper.get("option").text()).toContain("restricted");
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
    expect(wrapper.get(".app-shell").classes()).toContain("app-shell--setup");
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

  it("offers a friendly sidebar logout and ends the current session", async () => {
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);
    await router.push("/app/your-turn");
    await router.isReady();
    const wrapper = mount(App, { global: { plugins: [createPinia(), router] } });
    const button = wrapper.get(".sign-out-button");
    expect(button.text()).toContain("Sign out");
    expect(button.text()).toContain("See you next time");
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

  it("contains mobile drawer focus and restores it on Escape", async () => {
    await router.push("/app/your-turn");
    await router.isReady();
    const wrapper = mount(App, { attachTo: document.body, global: { plugins: [createPinia(), router] } });
    const toggle = wrapper.get(".menu-button");
    await toggle.trigger("click");
    await wrapper.vm.$nextTick();

    const close = wrapper.get<HTMLButtonElement>(".sidebar-close");
    expect(document.activeElement).toBe(close.element);
    expect(wrapper.get("#app-navigation").attributes("role")).toBe("dialog");
    expect(wrapper.get("#app-navigation").attributes("aria-modal")).toBe("true");
    expect(wrapper.get("#app-navigation").attributes("aria-label")).toBe("Application navigation");
    expect(wrapper.get("main").attributes()).toHaveProperty("inert");
    expect(wrapper.get(".mobile-header").attributes()).toHaveProperty("inert");
    expect(wrapper.get(".scrim").attributes("tabindex")).toBe("-1");
    expect(wrapper.get(".scrim").attributes("aria-hidden")).toBe("true");

    const last = wrapper.get<HTMLButtonElement>(".sign-out-button");
    const first = wrapper.get<HTMLAnchorElement>(".brand");
    expect(last.text()).toContain("Sign out");
    last.element.focus();
    await last.trigger("keydown", { key: "Tab" });
    expect(document.activeElement).toBe(first.element);
    await first.trigger("keydown", { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last.element);

    await last.trigger("keydown", { key: "Escape" });
    await wrapper.vm.$nextTick();
    expect(toggle.attributes("aria-expanded")).toBe("false");
    expect(wrapper.get("#app-navigation").attributes("role")).toBeUndefined();
    expect(wrapper.get("#app-navigation").attributes("aria-label")).toBeUndefined();
    expect(wrapper.get("main").attributes("inert")).toBeUndefined();
    expect(document.activeElement).toBe(toggle.element);
    wrapper.unmount();
  });
});
