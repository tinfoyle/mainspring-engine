// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { expectNoAxeViolations } from "../test/accessibility";
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
const rightsRequest = {
  request_id: "11000000-0000-4000-8000-000000000011",
  kind: "erasure" as const,
  scope: "affiliate" as const,
  state: "submitted" as const,
  requested_at: "2026-08-25T12:00:00Z",
  response_due_at: "2026-09-24T12:00:00Z",
  updated_at: "2026-08-25T12:00:00Z",
  verified_at: "2026-08-25T12:00:00Z"
};

async function mountView() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/privacy", component: PrivacyView }] });
  await router.push("/app/privacy");
  await router.isReady();
  const wrapper = mount(PrivacyView, { global: { plugins: [router] } });
  await flushPromises();
  await expectNoAxeViolations(wrapper.element);
  return wrapper;
}

beforeEach(() => {
  api.getPrivacyConsent.mockReset().mockResolvedValue(consent);
  api.listPrivacyRightsRequests.mockReset().mockResolvedValue({ requests: [] });
  api.setPrivacyConsent.mockReset().mockResolvedValue({ ...consent, analytics: false, marketing: false });
  api.submitPrivacyRightsRequest.mockReset();
  api.cancelPrivacyRightsRequest.mockReset().mockResolvedValue({ ...rightsRequest, state: "canceled" });
  api.erasePrivacyData.mockReset().mockResolvedValue(undefined);
});

describe("privacy controls", () => {
  it("offers equal rejection and persists both optional purposes as disabled", async () => {
    const wrapper = await mountView();
    const toggles = wrapper.findAll<HTMLInputElement>('input[type="checkbox"]');
    expect(toggles.map((toggle) => toggle.element.checked)).toEqual([true, true]);

    await wrapper.findAll("button").find((button) => button.text() === "Reject non-essential")?.trigger("click");
    await flushPromises();
    expect(toggles.map((toggle) => toggle.element.checked)).toEqual([false, false]);

    expect(api.setPrivacyConsent).toHaveBeenCalledWith({ analytics: false, marketing: false });
    expect(api.setPrivacyConsent).toHaveBeenCalledOnce();
    expect(wrapper.text()).toContain("Your privacy preferences were saved.");
  });

  it("restores the effective choice when a preference change is not saved", async () => {
    api.setPrivacyConsent.mockRejectedValue(new Error("unavailable"));
    const wrapper = await mountView();
    const toggles = wrapper.findAll<HTMLInputElement>('input[type="checkbox"]');

    await wrapper.findAll("button").find((button) => button.text() === "Reject non-essential")?.trigger("click");
    await flushPromises();

    expect(toggles.map((toggle) => toggle.element.checked)).toEqual([true, true]);
    expect(wrapper.text()).toContain("Privacy preferences could not be saved.");
    expect(wrapper.text()).not.toContain("Your privacy preferences were saved.");
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

  it("treats browser-data erasure as one pending consequential request", async () => {
    let resolveErase: (() => void) | undefined;
    api.erasePrivacyData.mockReturnValue(new Promise<void>((resolve) => { resolveErase = resolve; }));
    const wrapper = await mountView();
    const initial = wrapper.findAll("button").find((button) => button.text() === "Erase browser privacy data");
    await initial?.trigger("click");
    const confirm = wrapper.findAll("button").find((button) => button.text() === "Confirm browser-data erasure");
    await confirm?.trigger("click");

    const pending = wrapper.findAll("button").find((button) => button.text() === "Erasing browser data…");
    expect(pending?.attributes("disabled")).toBeDefined();
    await pending?.trigger("click");
    expect(api.erasePrivacyData).toHaveBeenCalledOnce();

    resolveErase?.();
    await flushPromises();
    expect(wrapper.text()).toContain("privacy receipt and raw analytics were erased");
  });

  it("tracks one verified Affiliate erasure request and permits cancellation", async () => {
    api.submitPrivacyRightsRequest.mockResolvedValue(rightsRequest);
    const wrapper = await mountView();
    await wrapper.get("#rights-kind").setValue("erasure");
    await wrapper.get("#rights-scope").setValue("affiliate");
    await wrapper.get(".rights-form").trigger("submit");
    await flushPromises();

    expect(api.submitPrivacyRightsRequest).toHaveBeenCalledWith({ kind: "erasure", scope: "affiliate" });
    expect(wrapper.text()).toContain("Your Erasure request for Affiliate data was received.");
    expect(wrapper.text()).toContain("Erasure · Affiliate");
    expect(wrapper.get<HTMLButtonElement>('.rights-form button[type="submit"]').element.disabled).toBe(true);

    await wrapper.findAll("button").find((button) => button.text() === "Cancel")?.trigger("click");
    await flushPromises();
    expect(api.cancelPrivacyRightsRequest).toHaveBeenCalledWith(rightsRequest.request_id);
    expect(wrapper.text()).toContain("The submitted request was canceled.");
    expect(wrapper.get<HTMLButtonElement>('.rights-form button[type="submit"]').element.disabled).toBe(false);
  });
});
