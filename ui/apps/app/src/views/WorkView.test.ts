// @vitest-environment happy-dom
import { flushPromises, mount, RouterLinkStub } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, WorkItem } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import WorkView from "./WorkView.vue";

const api = vi.hoisted(() => ({
  listWork: vi.fn(), getWorkSummary: vi.fn(), getWorkItem: vi.fn(), listWorkChildren: vi.fn(),
  createWork: vi.fn(), transitionWork: vi.fn(), assignWork: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => ({ ...await importOriginal<typeof import("@spyglass/api")>(), ...api }));

const account = {
  account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_version: 1,
  cell_id: "cell-us-east-01", display_name: "Northstar Studio", placement_generation: 1, role: "owner",
  slug: "northstar-studio", owner_enrollment_required: false,
  entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-24T20:00:00Z", version: 3,
    packages: [{ code: "work", version: 1, mode: "enabled", sources: ["subscription"] }] }
} satisfies AccountChoice;

const item = {
  id: "20000000-0000-4000-8000-000000000002", number: 17, depth: 0, kind: "ticket", title: "Confirm the launch checklist",
  description: "Verify the governed release boundary.", state: "in_progress", priority: "high", assignment: { responsibility: "shared" },
  provenance: { source: "manual", created_by: { kind: "user", id: "30000000-0000-4000-8000-000000000003" } }, version: 4,
  created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:05:00Z"
} satisfies WorkItem;

async function mountAt(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: "/app/work", component: WorkView }, { path: "/app/work/:itemID", component: WorkView }
  ] });
  await router.push(path); await router.isReady();
  const wrapper = mount(WorkView, { global: { plugins: [router], stubs: { RouterLink: RouterLinkStub } } });
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
  for (const mock of Object.values(api)) mock.mockReset();
  api.listWork.mockResolvedValue({ items: [item] });
  api.getWorkSummary.mockResolvedValue({ active: 1, in_progress: 1, waiting: 0, urgent: 0, done: 0 });
  api.getWorkItem.mockResolvedValue(item);
  api.listWorkChildren.mockResolvedValue({ items: [] });
  const session = useSessionStore();
  session.accounts = [account]; session.selectedID = account.account_id; session.userID = "30000000-0000-4000-8000-000000000003";
});

describe("Work surface", () => {
  it("renders the Account queue as mobile-first durable links", async () => {
    const wrapper = await mountAt("/app/work");
    expect(api.listWork).toHaveBeenCalledWith(account.account_id, {});
    expect(wrapper.text()).toContain("Confirm the launch checklist");
    expect(wrapper.text()).toContain("Shared responsibility");
    expect(wrapper.findAllComponents(RouterLinkStub).some((link) => link.props("to") === `/app/work/${item.id}`)).toBe(true);
  });

  it("loads current-version detail and exposes governed lifecycle commands", async () => {
    const wrapper = await mountAt(`/app/work/${item.id}`);
    expect(api.getWorkItem).toHaveBeenCalledWith(account.account_id, item.id);
    expect(wrapper.text()).toContain("Verify the governed release boundary");
    expect(wrapper.text()).toContain("Complete");
    expect(wrapper.text()).toContain("Edit responsibility");
  });
});
