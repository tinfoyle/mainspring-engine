// @vitest-environment happy-dom
import { mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { describe, expect, it, vi } from "vitest";
import App from "./App.vue";
import { router } from "./router";

vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ accounts: [] }), { status: 200 })));

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
