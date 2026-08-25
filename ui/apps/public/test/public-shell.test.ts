// @vitest-environment happy-dom
import { mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "../app/app.vue";

const route = vi.hoisted(() => ({ fullPath: "/" }));
const nuxtState = vi.hoisted(() => new Map<string, { value: unknown }>());

vi.mock("#imports", () => ({
  useRoute: () => route,
  useRuntimeConfig: () => ({ public: { appOrigin: "https://app.infiniteocean.test" } }),
  useState: (key: string, initialize: () => unknown) => {
    if (!nuxtState.has(key)) nuxtState.set(key, { value: initialize() });
    return nuxtState.get(key);
  }
}));

beforeEach(() => {
  nuxtState.clear();
  document.body.innerHTML = "";
  document.body.className = "";
  vi.stubGlobal("matchMedia", vi.fn(() => ({
    matches: false,
    media: "(min-width: 48rem)",
    onchange: null,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn()
  })));
});

describe("public site shell", () => {
  it("contains mobile menu focus, isolates the page, and restores focus", async () => {
    const wrapper = mount(App, {
      attachTo: document.body,
      global: {
        stubs: {
          ConsentBanner: true,
          IoLogo: true,
          NuxtPage: { template: "<div>Page</div>" },
          NuxtLink: { props: ["to"], template: '<a :href="to"><slot /></a>' }
        }
      }
    });
    const toggle = wrapper.get<HTMLButtonElement>(".site-menu");
    await toggle.trigger("click");
    await wrapper.vm.$nextTick();

    const drawer = wrapper.get("#site-navigation");
    const close = wrapper.get<HTMLButtonElement>(".site-nav-close");
    expect(document.activeElement).toBe(close.element);
    expect(toggle.attributes("aria-expanded")).toBe("true");
    expect(drawer.attributes("role")).toBe("dialog");
    expect(drawer.attributes("aria-modal")).toBe("true");
    expect(drawer.attributes("aria-label")).toBe("Site menu");
    expect(wrapper.get(".site-page").attributes()).toHaveProperty("inert");
    expect(wrapper.get(".site-menu-scrim").attributes("tabindex")).toBe("-1");
    expect(wrapper.get(".site-menu-scrim").attributes("aria-hidden")).toBe("true");
    expect(document.body.classList.contains("site-menu-open")).toBe(true);

    const links = wrapper.findAll<HTMLAnchorElement>("#site-navigation nav a");
    const last = links.at(-1);
    last?.element.focus();
    await last?.trigger("keydown", { key: "Tab" });
    expect(document.activeElement).toBe(close.element);
    await close.trigger("keydown", { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last?.element);

    await last?.trigger("keydown", { key: "Escape" });
    await wrapper.vm.$nextTick();
    expect(toggle.attributes("aria-expanded")).toBe("false");
    expect(drawer.attributes("role")).toBeUndefined();
    expect(drawer.attributes("aria-label")).toBeUndefined();
    expect(wrapper.get(".site-page").attributes("inert")).toBeUndefined();
    expect(document.body.classList.contains("site-menu-open")).toBe(false);
    expect(document.activeElement).toBe(toggle.element);
    wrapper.unmount();
  });

  it("removes page scroll locking when the shell unmounts", async () => {
    const wrapper = mount(App, {
      attachTo: document.body,
      global: {
        stubs: { ConsentBanner: true, IoLogo: true, NuxtPage: true, NuxtLink: { template: "<a><slot /></a>" } }
      }
    });
    await wrapper.get(".site-menu").trigger("click");
    expect(document.body.classList.contains("site-menu-open")).toBe(true);
    wrapper.unmount();
    expect(document.body.classList.contains("site-menu-open")).toBe(false);
  });
});
