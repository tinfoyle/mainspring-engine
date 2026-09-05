// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import DirectoryPanel from "./DirectoryPanel.vue";
import { availableModules } from "./registry";

const api = vi.hoisted(() => ({ operationsDirectory: vi.fn() }));
vi.mock("@spyglass/api", async original => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));
const user = { id: "10000000-0000-4000-8000-000000000001", display_name: "<script>Person</script>", email: "person@example.com", state: "active", email_verified: true, created_at: "2026-09-05T12:00:00Z", team_count: 0 };
const page = { kind: "users", page: 1, page_size: 25, total: 26, users: [user], teams: [] };
beforeEach(() => { vi.resetAllMocks(); api.operationsDirectory.mockResolvedValue(page); });
const button = (wrapper: ReturnType<typeof mount>, text: string) => wrapper.findAll("button").find(item => item.text() === text)!;

describe("user and team directory", () => {
 it("is available only to administrators", () => {
  expect(availableModules(["support", "analytics"]).some(item => item.id === "directory")).toBe(false);
  expect(availableModules(["operations_administrator"]).some(item => item.id === "directory")).toBe(true);
 });
 it("loads audited server pages and resets page size and list changes", async () => {
  const wrapper = mount(DirectoryPanel); await flushPromises();
  expect(api.operationsDirectory).toHaveBeenLastCalledWith(expect.objectContaining({ kind: "users", page: 1, page_size: 25, ticket: "DIRECTORY-REVIEW" }));
  expect(wrapper.get("tbody").text()).toContain(user.display_name);
  expect(wrapper.find("tbody script").exists()).toBe(false);
  api.operationsDirectory.mockResolvedValueOnce({ ...page, page: 2 });
  await button(wrapper, "Next").trigger("click"); await flushPromises();
  expect(api.operationsDirectory).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 }));
  expect(button(wrapper, "Next").attributes("disabled")).toBeDefined();
  await wrapper.get("select").setValue("50"); await flushPromises();
  expect(api.operationsDirectory).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, page_size: 50 }));
  api.operationsDirectory.mockResolvedValueOnce({ ...page, kind: "teams", total: 0, users: [] });
  await button(wrapper, "Teams").trigger("click"); await flushPromises();
  expect(api.operationsDirectory).toHaveBeenLastCalledWith(expect.objectContaining({ kind: "teams", page: 1 }));
  expect(wrapper.text()).toContain("No teams yet.");
  wrapper.unmount();
 });
 it("passes exact identity and audit context to customer lookup", async () => {
  const wrapper = mount(DirectoryPanel); await flushPromises();
  await button(wrapper, "View user").trigger("click");
  expect(wrapper.emitted("lookup")?.[0]?.[0]).toEqual(expect.objectContaining({ kind: "user_id", value: user.id, ticket: "DIRECTORY-REVIEW" }));
  wrapper.unmount();
 });
 it("clears stale data after a failed page request and can retry", async () => {
  const wrapper = mount(DirectoryPanel); await flushPromises();
  api.operationsDirectory.mockRejectedValueOnce(new Error("The list is unavailable."));
  await button(wrapper, "Next").trigger("click"); await flushPromises();
  expect(wrapper.find("table").exists()).toBe(false);
  expect(wrapper.get('[role="alert"]').text()).toContain("unavailable");
  await wrapper.get("form").trigger("submit"); await flushPromises();
  expect(wrapper.find("table").exists()).toBe(true);
  wrapper.unmount();
 });
 it("returns to a valid page when the last page disappears", async () => {
  const wrapper = mount(DirectoryPanel); await flushPromises();
  api.operationsDirectory.mockResolvedValueOnce({ ...page, page: 2, total: 20, users: [] }).mockResolvedValueOnce({ ...page, total: 20 });
  await button(wrapper, "Next").trigger("click"); await flushPromises();
  expect(api.operationsDirectory).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1 }));
  expect(wrapper.text()).toContain("Page 1 of 1");
  wrapper.unmount();
 });
});
