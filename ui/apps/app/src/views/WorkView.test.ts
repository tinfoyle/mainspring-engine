// @vitest-environment happy-dom
import { flushPromises, mount, RouterLinkStub } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { APIProblem, type AccountChoice, type WorkItem } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";
import WorkView from "./WorkView.vue";

const api = vi.hoisted(() => ({
  listWork: vi.fn(), getWorkSummary: vi.fn(), getWorkItem: vi.fn(), listWorkChildren: vi.fn(),
  createWork: vi.fn(), transitionWork: vi.fn(), assignWork: vi.fn(), listAgentBoardrooms: vi.fn(), listAgentPersonas: vi.fn(), listAgentMessages: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => ({ ...await importOriginal<typeof import("@spyglass/api")>(), ...api }));

const account = {
  account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_state: "active", account_version: 1,
  cell_id: "cell-us-east-01", display_name: "Northstar Studio", placement_generation: 1, role: "owner",
  slug: "northstar-studio", owner_enrollment_required: false,
  entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-24T20:00:00Z", version: 3,
    packages: [{ code: "work", version: 1, mode: "enabled", sources: ["subscription"] }, { code: "agents", version: 1, mode: "enabled", sources: ["subscription"] }] }
} satisfies AccountChoice;

const room = { id: "40000000-0000-4000-8000-000000000004", account_id: account.account_id, name: "Business Setup", purpose: "Operations", state: "active", version: 2, manager_persona_id: "50000000-0000-4000-8000-000000000005", created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:00:00Z" } as const;
const persona = { id: room.manager_persona_id, boardroom_id: room.id, state: "active", latest_version: 1, persona_version_id: "60000000-0000-4000-8000-000000000006", name: "Operations Guide", role: "Main operations agent", description: "Handles Account work", system_instructions: "Handle Account work and ask the owner only when needed.", content_digest: "a".repeat(64), policy: { complexity: "balanced", maximum_input_tokens: 24000, maximum_output_tokens: 4096, maximum_cost_micros: 250000, maximum_tool_steps: 0, citation_policy: "none", action_policy: "none", tools: [], output_schema: {} }, created_at: room.created_at, updated_at: room.updated_at } as const;

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
  await expectNoAxeViolations(wrapper.element);
  return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
  for (const mock of Object.values(api)) mock.mockReset();
  api.listWork.mockResolvedValue({ items: [item] });
  api.getWorkSummary.mockResolvedValue({ active: 1, in_progress: 1, waiting: 0, urgent: 0, done: 0 });
  api.getWorkItem.mockResolvedValue(item);
  api.listWorkChildren.mockResolvedValue({ items: [] });
  api.listAgentBoardrooms.mockResolvedValue([room]);
  api.listAgentPersonas.mockResolvedValue([persona]);
  api.listAgentMessages.mockResolvedValue([]);
  const session = useSessionStore();
  session.accounts = [account]; session.selectedID = account.account_id; session.userID = "30000000-0000-4000-8000-000000000003";
});

describe("Work surface", () => {
  it("renders the Account queue as mobile-first durable links", async () => {
    const wrapper = await mountAt("/app/work");
    expect(api.listWork).toHaveBeenCalledWith(account.account_id, {});
    expect(wrapper.text()).toContain("Confirm the launch checklist");
    expect(wrapper.text()).toContain("Shared responsibility");
    expect(wrapper.get('[aria-label="Work summary"]').attributes("role")).toBe("group");
    expect(wrapper.findAllComponents(RouterLinkStub).some((link) => link.props("to") === `/app/work/${item.id}`)).toBe(true);
  });

  it("loads current-version detail and exposes governed lifecycle commands", async () => {
    const wrapper = await mountAt(`/app/work/${item.id}`);
    expect(api.getWorkItem).toHaveBeenCalledWith(account.account_id, item.id);
    expect(wrapper.text()).toContain("Verify the governed release boundary");
    expect(wrapper.text()).toContain("Complete");
    expect(wrapper.text()).toContain("Edit responsibility");
  });

  it("shows the agent's completed outcome on the Work item", async () => {
    const agentWork = { ...item, state: "done" as const, assignment: { responsibility: "persona" as const, persona_id: persona.id }, provenance: { ...item.provenance, conversation_id: "70000000-0000-4000-8000-000000000007", run_id: "80000000-0000-4000-8000-000000000008" } };
    api.getWorkItem.mockResolvedValue(agentWork);
    api.listAgentMessages.mockResolvedValue([{ id: "90000000-0000-4000-8000-000000000009", conversation_id: agentWork.provenance.conversation_id, sequence: 2, role: "persona", body: "The schedule review is complete.", created_at: item.updated_at,
      result: { contribution: "The schedule review is complete.", findings: ["Tuesday has an uncovered service call."], recommendations: ["Assign the north crew."], questions: [], citations: [], proposed_actions: [], delegations: [], confidence: "high" } }]);
    const wrapper = await mountAt(`/app/work/${item.id}`);
    expect(api.listAgentMessages).toHaveBeenCalledWith(account.account_id, agentWork.provenance.conversation_id);
    expect(wrapper.text()).toContain("Agent progress");
    expect(wrapper.text()).toContain("Tuesday has an uncovered service call.");
    expect(wrapper.text()).toContain("Assign the north crew.");
  });

  it("retains the safe-retry notice after reloading a conflicted transition", async () => {
    api.transitionWork.mockRejectedValue(new APIProblem(412));
    const wrapper = await mountAt(`/app/work/${item.id}`);
    await wrapper.findAll("button").find((button) => button.text() === "Complete")?.trigger("click");
    await wrapper.get('[role="dialog"] textarea').setValue("The governed launch checklist is complete.");
    await wrapper.get('form[role="dialog"]').trigger("submit");
    await flushPromises();
    expect(api.getWorkItem).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain("Spyglass loaded the current version; review it before trying again.");
    await expectNoAxeViolations(wrapper.element);
  });

  it("dispatches newly created Work to the operations agent by default", async () => {
    const created = { ...item, id: "21000000-0000-4000-8000-000000000002", state: "open" as const, assignment: { responsibility: "persona" as const, persona_id: persona.id }, version: 1 };
    api.createWork.mockResolvedValue(created);
    const wrapper = await mountAt("/app/work");
    await wrapper.findAll("button").find((button) => button.text() === "New work")?.trigger("click");
    await wrapper.get('.work-create input[placeholder="What needs to happen?"]').setValue("Review tomorrow's schedule");
    await wrapper.get("form.work-create").trigger("submit");
    await flushPromises();
    expect(api.createWork).toHaveBeenCalledWith(account.account_id, expect.objectContaining({
      assignment: { responsibility: "persona", persona_id: persona.id }
    }));
  });

  it("only keeps Work with the user when they explicitly choose it", async () => {
    const created = { ...item, id: "22000000-0000-4000-8000-000000000002", state: "open" as const, assignment: { responsibility: "user" as const, user_id: "30000000-0000-4000-8000-000000000003" }, version: 1 };
    api.createWork.mockResolvedValue(created);
    const wrapper = await mountAt("/app/work");
    await wrapper.findAll("button").find((button) => button.text() === "New work")?.trigger("click");
    await wrapper.get('.work-create input[placeholder="What needs to happen?"]').setValue("Call the supplier");
    await wrapper.findAll('.work-create select')[2]!.setValue("user");
    await wrapper.get("form.work-create").trigger("submit");
    await flushPromises();
    expect(api.createWork).toHaveBeenCalledWith(account.account_id, expect.objectContaining({ assignment: { responsibility: "user" } }));
  });
});
