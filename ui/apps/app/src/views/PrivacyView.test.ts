// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import PrivacyView from "./PrivacyView.vue";

const api = vi.hoisted(() => ({
  cancelPrivacyRightsRequest: vi.fn(),
  erasePrivacyData: vi.fn(),
  getPrivacyConsent: vi.fn(),
  listPrivacyRightsRequests: vi.fn(),
  setPrivacyConsent: vi.fn(),
  submitPrivacyRightsRequest: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@spyglass/api")>(),
  ...api
}));

const consent = {
  analytics: true,
  decided: true,
  marketing: true,
  policy_version: 1,
  renewal_required: false,
  surface: "private" as const
};

async function mountView() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/privacy", component: PrivacyView }] });
  await router.push("/app/privacy");
  await router.isReady();
  const wrapper = mount(PrivacyView, { global: { plugins: [router] } });
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  api.getPrivacyConsent.mockReset().mockResolvedValue(consent);
  api.listPrivacyRightsRequests.mockReset().mockResolvedValue({ requests: [] });
  api.setPrivacyConsent.mockReset().mockResolvedValue({ ...consent, analytics: false, marketing: false });
  api.submitPrivacyRightsRequest.mockReset();
  api.cancelPrivacyRightsRequest.mockReset();
  api.erasePrivacyData.mockReset().mockResolvedValue(undefined);
});

describe("privacy controls", () => {
  it("offers equal rejection and persists both optional purposes as disabled", async () => {
    const wrapper = await mountView();
    const toggles = wrapper.findAll<HTMLInputElement>('input[type="checkbox"]');
    expect(toggles.map((toggle) => toggle.element.checked)).toEqual([true, true]);

    await wrapper.findAll("button").find((button) => button.text() === "Reject non-essential")?.trigger("click");
    expect(toggles.map((toggle) => toggle.element.checked)).toEqual([false, false]);
    await wrapper.get("form.preference-panel").trigger("submit");
    await flushPromises();

    expect(api.setPrivacyConsent).toHaveBeenCalledWith({ analytics: false, marketing: false });
    expect(wrapper.text()).toContain("Your privacy preferences were saved.");
  });

  it("requires a second action before erasing this browser's consent and analytics subject", async () => {
    const wrapper = await mountView();
    const erase = () => wrapper.findAll("button").find((button) => button.text().includes("browser privacy data") || button.text().includes("browser-data erasure"));

    await erase()?.trigger("click");
    expect(api.erasePrivacyData).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain("Optional tracking remains off until you choose again.");
    await erase()?.trigger("click");
    await flushPromises();

    expect(api.erasePrivacyData).toHaveBeenCalledOnce();
    expect(wrapper.text()).toContain("privacy receipt and raw analytics were erased");
  });
});
