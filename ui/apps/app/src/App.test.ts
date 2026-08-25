// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App.vue";
import { router } from "./router";

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
  it("makes Your Turn the default mobile destination", async () => {
    await router.push("/app");
    await router.isReady();
    const wrapper = mount(App, { global: { plugins: [createPinia(), router] } });
    expect(wrapper.get("h1").text()).toBe("Your Turn");
    expect(wrapper.get("nav").attributes("aria-label")).toBe("Main navigation");
    expect(wrapper.get("nav").text()).toContain("Schedules");
    expect(wrapper.get("nav").text()).toContain("Finance");
    expect(wrapper.get("nav").text()).toContain("Integrations");
    expect(wrapper.get("nav").text()).toContain("Account");
    expect(wrapper.get("nav").text()).toContain("Billing");
    expect(wrapper.get("nav").text()).toContain("Security");
    expect(wrapper.get("nav").text()).toContain("Exports");
    expect(wrapper.get("nav").text()).toContain("Lifecycle");
    expect(wrapper.find(".nav-link em").exists()).toBe(false);
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
    expect(wrapper.get("main").attributes("inert")).toBeUndefined();
    expect(document.activeElement).toBe(toggle.element);
    wrapper.unmount();
  });
});
