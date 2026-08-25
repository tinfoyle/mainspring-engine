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
    expect(wrapper.get("nav").text()).toContain("Account");
    expect(wrapper.get("nav").text()).toContain("Billing");
    expect(wrapper.get("nav").text()).toContain("Security");
    expect(wrapper.get("nav").text()).toContain("Exports");
    expect(wrapper.get("nav").text()).toContain("Lifecycle");
    expect(wrapper.find(".nav-link em").exists()).toBe(false);
  });
});
