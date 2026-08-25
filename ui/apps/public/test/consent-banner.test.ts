// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => new Map<string, { value: unknown }>());
const api = vi.hoisted(() => ({ getPrivacyConsent: vi.fn(), setPrivacyConsent: vi.fn() }));

vi.mock("#imports", () => ({
  useState: <T>(key: string, initialize: () => T) => {
    if (!state.has(key)) state.set(key, { value: initialize() });
    return state.get(key) as { value: T };
  }
}));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));

import ConsentBanner from "../app/components/ConsentBanner.vue";

const undecided = {
  analytics: false,
  decided: false,
  effective_at: "2026-08-25T12:00:00Z",
  marketing: false,
  policy_version: 2,
  renewal_required: false,
  surface: "public"
};
const decided = { ...undecided, decided: true, effective_at: "2026-08-25T12:01:00Z" };

beforeEach(() => {
  document.body.innerHTML = "";
  state.clear();
  api.getPrivacyConsent.mockReset().mockResolvedValue(undecided);
  api.setPrivacyConsent.mockReset().mockResolvedValue(decided);
});

function button(wrapper: ReturnType<typeof mount>, label: string) {
  const match = wrapper.findAll<HTMLButtonElement>("button").find((item) => item.text() === label);
  if (!match) throw new Error(`missing ${label} button`);
  return match;
}

describe("public consent banner", () => {
  it("gives accept and reject equal prominence and restores focus after a choice", async () => {
    const wrapper = mount(ConsentBanner, { attachTo: document.body });
    await flushPromises();

    const accept = button(wrapper, "Accept analytics");
    const reject = button(wrapper, "Reject non-essential");
    expect(accept.classes()).toContain("io-button--secondary");
    expect(reject.classes()).toContain("io-button--secondary");

    reject.element.focus();
    await reject.trigger("click");
    await flushPromises();
    expect(api.setPrivacyConsent).toHaveBeenCalledWith({ analytics: false, marketing: false });
    const reopen = button(wrapper, "Privacy choices");
    expect(document.activeElement).toBe(reopen.element);
    wrapper.unmount();
  });

  it("moves focus into preferences and saves the explicit purposes", async () => {
    api.getPrivacyConsent.mockResolvedValue(decided);
    api.setPrivacyConsent.mockResolvedValue({ ...decided, analytics: true });
    const wrapper = mount(ConsentBanner, { attachTo: document.body });
    await flushPromises();

    await button(wrapper, "Privacy choices").trigger("click");
    await wrapper.vm.$nextTick();
    const options = wrapper.findAll<HTMLInputElement>(".consent__options input");
    expect(options).toHaveLength(2);
    expect(document.activeElement).toBe(options[0]?.element);
    await options[0]?.setValue(true);
    await button(wrapper, "Save preferences").trigger("click");
    await flushPromises();
    expect(api.setPrivacyConsent).toHaveBeenCalledWith({ analytics: true, marketing: false });
    expect(document.activeElement).toBe(button(wrapper, "Privacy choices").element);
    wrapper.unmount();
  });

  it("keeps optional tracking off and announces a failed first choice", async () => {
    api.setPrivacyConsent.mockRejectedValue(new Error("offline"));
    const wrapper = mount(ConsentBanner, { attachTo: document.body });
    await flushPromises();

    await button(wrapper, "Accept analytics").trigger("click");
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain("Optional tracking remains off");
    expect(button(wrapper, "Accept analytics").attributes("disabled")).toBeUndefined();
    wrapper.unmount();
  });
});
