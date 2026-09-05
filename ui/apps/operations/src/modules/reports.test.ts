// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import TrafficPanel from "./TrafficPanel.vue";
import AnalyticsPanel from "./AnalyticsPanel.vue";
import { availableModules } from "./registry";

const api = vi.hoisted(() => ({ operationsTrafficReport: vi.fn(), operationsAnalyticsReport: vi.fn() }));
vi.mock("@spyglass/api", async original => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));
const report = { from: "2026-09-04T00:00:00Z", to: "2026-09-05T00:00:00Z", generated_at: "2026-09-05T00:00:00Z", available_from: null, available_to: null, requests: 7, unique_ips: 2, server_errors: 1, files_read: 3, invalid_records: 0, truncated: false, ip_list_truncated: false, ips: [{ ip: "192.0.2.1", requests: 6, first_seen: "2026-09-04T00:00:00Z", last_seen: "2026-09-04T01:00:00Z" }, { ip: "198.51.100.1", requests: 1, first_seen: "2026-09-04T00:00:00Z", last_seen: "2026-09-04T01:00:00Z" }], hosts: [], statuses: [], days: [], logs: [] };
beforeEach(() => { vi.resetAllMocks(); api.operationsTrafficReport.mockResolvedValue(report); api.operationsAnalyticsReport.mockResolvedValue({ rows: [], minimum_cohort: 5 }); });

describe("admin reports", () => {
 it("limits traffic navigation to administrators", () => {
  expect(availableModules(["analytics"]).map(module => module.id)).not.toContain("traffic");
  expect(availableModules(["operations_administrator"]).map(module => module.id)).toContain("traffic");
 });
 it("loads real traffic, filters IPs and removes stale results after failure", async () => {
  const wrapper = mount(TrafficPanel);
  expect(wrapper.find(".metric-grid").exists()).toBe(false);
  await wrapper.get("form").trigger("submit"); await flushPromises();
  expect(wrapper.get(".metric-grid").text()).toContain("Requests7");
  await wrapper.findAll(".report-tabs button")[1]!.trigger("click");
  expect(wrapper.findAll("tbody tr")).toHaveLength(2);
  await wrapper.get('input[type="search"]').setValue("198.51");
  expect(wrapper.findAll("tbody tr")).toHaveLength(1);
  api.operationsTrafficReport.mockRejectedValueOnce(new Error("Traffic logs could not be read"));
  await wrapper.get("form").trigger("submit"); await flushPromises();
  expect(wrapper.get('[role="alert"]').text()).toContain("could not be read");
  expect(wrapper.find(".metric-grid").exists()).toBe(false);
  wrapper.unmount();
 });
 it("clearly marks partial log scans", async () => {
  api.operationsTrafficReport.mockResolvedValueOnce({ ...report, truncated: true });
  const wrapper = mount(TrafficPanel);
  await wrapper.get("form").trigger("submit"); await flushPromises();
  expect(wrapper.get('[role="status"]').text()).toContain("incomplete"); wrapper.unmount();
 });
 it("distinguishes small consented cohorts from zero traffic", async () => {
  const wrapper = mount(AnalyticsPanel);
  await wrapper.get("form").trigger("submit"); await flushPromises();
  expect(api.operationsAnalyticsReport).toHaveBeenCalledWith(expect.objectContaining({ minimum_cohort: 5, dimension: "none" }));
  expect(wrapper.get('[role="status"]').text()).toContain("not a count of zero visitors");
  expect(wrapper.find(".metric-grid").exists()).toBe(false); wrapper.unmount();
 });
});
