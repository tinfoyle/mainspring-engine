// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import axe from "axe-core";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App.vue";

const api = vi.hoisted(() => ({ operationsSession: vi.fn(), operationsLookup: vi.fn(), createOperationsSupportGrant: vi.fn(), openOperationsSupportView: vi.fn() }));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));

const staffSession = { authentication_method: "passkey", expires_at: "2026-08-28T12:00:00Z", staff: { user_id: "10000000-0000-4000-8000-000000000001", display_name: "Support Person", state: "active", roles: ["support", "analytics"] } };

beforeEach(() => {
  api.operationsSession.mockReset().mockResolvedValue(staffSession);
  api.operationsLookup.mockReset().mockResolvedValue({ results: [] });
});

describe("operations console", () => {
  it("shows role-appropriate staff navigation and guardrails", async () => {
    const wrapper = mount(App, { attachTo: document.body }); await flushPromises();
    expect(wrapper.get("h1").text()).toBe("Admin overview");
    expect(wrapper.get("nav").text()).toContain("Customer lookup");
    expect(wrapper.get("nav").text()).toContain("Analytics");
    expect(wrapper.text()).toContain("User and team lists are administrator-only.");
    const results = await axe.run(wrapper.element, { rules: { "color-contrast": { enabled: false } } });
    expect(results.violations).toEqual([]);
    wrapper.unmount();
  });

  it("requires an exact value, ticket, and reason before lookup", async () => {
    const wrapper = mount(App); await flushPromises();
    await wrapper.findAll("nav button")[1]?.trigger("click");
    const inputs = wrapper.findAll("input");
    expect(inputs).toHaveLength(3);
    expect(inputs.every((input) => input.attributes("required") !== undefined)).toBe(true);
    await inputs[0]?.setValue("person@example.com"); await inputs[1]?.setValue("SUP-1042"); await inputs[2]?.setValue("Customer requested billing help");
    await wrapper.get("form").trigger("submit"); await flushPromises();
    expect(api.operationsLookup).toHaveBeenCalledWith({ kind: "email", value: "person@example.com", ticket: "SUP-1042", reason: "Customer requested billing help" });
  });
});
