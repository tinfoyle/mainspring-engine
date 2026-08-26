// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ erasePrivacyData: vi.fn(), getPrivacyConsentHistory: vi.fn() }));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));

import PublicConsentHistory from "../app/components/PublicConsentHistory.vue";

const decision = {
  decision_id: "12000000-0000-4000-8000-000000000012",
  subject_id: "13000000-0000-4000-8000-000000000013",
  policy_version: 1,
  surface: "public" as const,
  analytics: true,
  marketing: false,
  effective_at: "2026-08-25T11:00:00Z"
};

beforeEach(() => {
  api.getPrivacyConsentHistory.mockReset().mockResolvedValue({ decisions: [decision] });
  api.erasePrivacyData.mockReset().mockResolvedValue(undefined);
});

function button(wrapper: ReturnType<typeof mount>, label: string) {
  const match = wrapper.findAll<HTMLButtonElement>("button").find((item) => item.text() === label);
  if (!match) throw new Error(`missing ${label} button`);
  return match;
}

describe("public consent history", () => {
  it("shows optional-purpose decisions without displaying internal identifiers", async () => {
    const wrapper = mount(PublicConsentHistory);
    await flushPromises();
    expect(wrapper.text()).toContain("Analytics accepted");
    expect(wrapper.text()).toContain("Marketing rejected");
    expect(wrapper.text()).not.toContain(decision.decision_id);
    expect(wrapper.text()).not.toContain(decision.subject_id);
  });

  it("requires confirmation, erases the public browser subject once, and announces the result", async () => {
    const erased = vi.fn();
    window.addEventListener("spyglass:privacy-data-erased", erased, { once: true });
    const wrapper = mount(PublicConsentHistory);
    await flushPromises();

    await button(wrapper, "Erase this browser's privacy data").trigger("click");
    expect(api.erasePrivacyData).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain("Your login, Accounts, billing and Affiliate records are not affected");
    await button(wrapper, "Confirm public-site data erasure").trigger("click");
    await flushPromises();

    expect(api.erasePrivacyData).toHaveBeenCalledOnce();
    expect(erased).toHaveBeenCalledOnce();
    expect(wrapper.text()).toContain("public-site consent receipts and raw analytics were erased");
    expect(wrapper.text()).toContain("No saved public-site consent decision exists");
  });
});
