// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils"; import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest"; import { APIProblem, type AccountChoice } from "@spyglass/api";
import { useSessionStore } from "../stores/session"; import ExportsView from "./ExportsView.vue";
import { expectNoAxeViolations } from "../test/accessibility";
const api = vi.hoisted(() => ({ listAccountExports: vi.fn(), createAccountExport: vi.fn(), cancelAccountExport: vi.fn(), createExportDownloadCapability: vi.fn(), downloadAccountExport: vi.fn() }));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));
const account = { account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_version: 2, cell_id: "cell-a", display_name: "Northstar", placement_generation: 1, role: "owner", slug: "northstar", owner_enrollment_required: false, entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 1, evaluated_at: "2026-08-24T00:00:00Z", version: 1, packages: [] } } satisfies AccountChoice;
const item = { id: "20000000-0000-4000-8000-000000000002", account_id: account.account_id, requested_by: "user", cell_id: "cell-a", placement_generation: 1, account_version: 2, state: "queued", version: 3, attempt_count: 0, requested_at: "2026-08-24T00:00:00Z", expires_at: "2026-08-31T00:00:00Z" } as const;
beforeEach(() => { setActivePinia(createPinia()); Object.values(api).forEach((mock) => mock.mockReset()); api.listAccountExports.mockResolvedValue({ exports: [item] }); const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; });
describe("Account exports surface", () => {
  it("renders governed history for an owner", async () => { const wrapper = mount(ExportsView); await flushPromises(); expect(wrapper.text()).toContain("Take your Account with you"); expect(wrapper.text()).toContain("Cancel request"); await expectNoAxeViolations(wrapper.element); });
  it("reloads state after a cancellation conflict", async () => { api.cancelAccountExport.mockRejectedValue(new APIProblem(409)); const wrapper = mount(ExportsView); await flushPromises(); await wrapper.findAll("button").find((button) => button.text() === "Cancel request")?.trigger("click"); await wrapper.get("[role=dialog] input").setValue("CANCEL"); await wrapper.get("[role=dialog]").trigger("submit"); await flushPromises(); expect(api.cancelAccountExport).toHaveBeenCalledWith(account.account_id, item); expect(api.listAccountExports).toHaveBeenCalledTimes(2); expect(wrapper.text()).toContain("This export changed"); });
});
