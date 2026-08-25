// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App.vue";
import { router } from "./router";
import { useSessionStore } from "./stores/session";

const analytics = vi.hoisted(() => ({ emitAnalytics: vi.fn(), getPrivacyConsent: vi.fn() }));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...analytics }));
const fetcher = vi.fn();
vi.stubGlobal("fetch", fetcher);

beforeEach(() => {
  sessionStorage.clear();
  fetcher.mockReset().mockResolvedValue(new Response(JSON.stringify({ accounts: [] }), { status: 200 }));
  analytics.getPrivacyConsent.mockReset().mockResolvedValue({ decided: false, analytics: false, marketing: false, renewal_required: false });
  analytics.emitAnalytics.mockReset().mockResolvedValue(false);
});

describe("application shell", () => {
  it("makes Your Turn the default and keeps unavailable packages out of the mobile menu", async () => {
    await router.push("/app");
    await router.isReady();
    const wrapper = mount(App, { global: { plugins: [createPinia(), router] } });
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
    wrapper.unmount();
  });

  it("adds enabled and read-only package areas to the Workspace group", async () => {
    await router.push("/app/your-turn");
    await router.isReady();
    const pinia = createPinia();
    const session = useSessionStore(pinia);
    session.accounts = [{
      account_id: "10000000-0000-4000-8000-000000000001",
      account_type: "paid",
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

    const links = wrapper.findAll<HTMLAnchorElement>(".nav-link");
    const last = links.at(-1);
    const first = wrapper.get<HTMLAnchorElement>(".brand");
    expect(last?.text()).toBe("Privacy");
    last?.element.focus();
    await last?.trigger("keydown", { key: "Tab" });
    expect(document.activeElement).toBe(first.element);
    await first.trigger("keydown", { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last?.element);

    await last?.trigger("keydown", { key: "Escape" });
    await wrapper.vm.$nextTick();
    expect(toggle.attributes("aria-expanded")).toBe("false");
    expect(wrapper.get("#app-navigation").attributes("role")).toBeUndefined();
    expect(wrapper.get("#app-navigation").attributes("aria-label")).toBeUndefined();
    expect(wrapper.get("main").attributes("inert")).toBeUndefined();
    expect(document.activeElement).toBe(toggle.element);
    wrapper.unmount();
  });
});
