// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { describe, expect, it, vi } from "vitest";
import PersonaEditor from "./PersonaEditor.vue";
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), getPublicCatalog: vi.fn().mockResolvedValue({ ai_complexity_rates: [] }) }));

describe("Agent editor tool limits", () => {
  it("sets zero tool steps without tools and restores an allowance when a tool is selected", async () => {
    const wrapper = mount(PersonaEditor, { props: { saving: false, error: "" } }); await flushPromises();
    await wrapper.get("form").trigger("submit");
    expect(wrapper.emitted("publish")![0]![0]).toEqual(expect.objectContaining({ policy: expect.objectContaining({ tools: [], maximum_tool_steps: 0 }) }));
    await wrapper.get('.persona-choice-grid input[type="checkbox"]').setValue(true);
    await wrapper.get("form").trigger("submit");
    expect(wrapper.emitted("publish")![1]![0]).toEqual(expect.objectContaining({ policy: expect.objectContaining({ tools: [expect.objectContaining({ capability: "work.summary.read" })], maximum_tool_steps: 5 }) }));
    await wrapper.get('.persona-choice-grid input[type="checkbox"]').setValue(false);
    await wrapper.get("form").trigger("submit");
    expect(wrapper.emitted("publish")![2]![0]).toEqual(expect.objectContaining({ policy: expect.objectContaining({ tools: [], maximum_tool_steps: 0 }) }));
  });
});
