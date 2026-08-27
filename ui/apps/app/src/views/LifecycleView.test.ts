// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils"; import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest"; import type { AccountChoice } from "@spyglass/api";
import { createMemoryHistory, createRouter } from "vue-router";
import { useSessionStore } from "../stores/session"; import LifecycleView from "./LifecycleView.vue";
import { expectNoAxeViolations } from "../test/accessibility";
const api = vi.hoisted(() => ({ listAccountClosures: vi.fn(), requestAccountClosure: vi.fn(), cancelAccountClosure: vi.fn() }));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));
const account = { account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_state: "active", account_version: 7, cell_id: "cell-a", display_name: "Northstar", placement_generation: 1, role: "owner", slug: "northstar", owner_enrollment_required: false, entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 1, evaluated_at: "2026-08-24T00:00:00Z", version: 1, packages: [] } } satisfies AccountChoice;
async function mountView() { const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/account-closures", component: LifecycleView }] }); await router.push("/app/account-closures"); await router.isReady(); return mount(LifecycleView, { global: { plugins: [router] } }); }
beforeEach(() => { setActivePinia(createPinia()); Object.values(api).forEach((mock) => mock.mockReset()); api.listAccountClosures.mockResolvedValue({ account_closures: [] }); api.requestAccountClosure.mockResolvedValue({}); const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; session.load = vi.fn().mockResolvedValue(undefined); });
describe("Account lifecycle surface", () => {
  it("binds closure to the selected Account version and explicit reason", async () => { const wrapper = await mountView(); await flushPromises(); await expectNoAxeViolations(wrapper.element); await wrapper.findAll("button").find((button) => button.text() === "Request closure")?.trigger("click"); await wrapper.get("[role=dialog] textarea").setValue("Operations concluded."); await wrapper.get("[role=dialog] input").setValue("CLOSE"); await wrapper.get("[role=dialog]").trigger("submit"); await flushPromises(); expect(api.requestAccountClosure).toHaveBeenCalledWith(account.account_id, 7, "Operations concluded."); expect(wrapper.text()).toContain("No closure history"); });
});
