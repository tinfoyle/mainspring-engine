// @vitest-environment happy-dom
import { flushPromises, mount, RouterLinkStub } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { APIProblem, type AccountChoice, type AgentBoardroom, type AgentPersona, type Schedule } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";
import SchedulesView from "./SchedulesView.vue";

const api = vi.hoisted(() => ({
  listSchedules: vi.fn(), getSchedule: vi.fn(), getScheduleHistory: vi.fn(), createSchedule: vi.fn(), reviseSchedule: vi.fn(),
  pauseSchedule: vi.fn(), resumeSchedule: vi.fn(), deleteSchedule: vi.fn(), triggerSchedule: vi.fn(),
  listAgentBoardrooms: vi.fn(), listAgentPersonas: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => ({ ...await importOriginal<typeof import("@spyglass/api")>(), ...api }));

const account = {
  account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_state: "active", account_version: 1,
  cell_id: "cell-us-east-01", display_name: "Northstar Studio", placement_generation: 1, role: "owner",
  slug: "northstar-studio", owner_enrollment_required: false,
  entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-24T20:00:00Z", version: 3,
    packages: [{ code: "agents", version: 1, mode: "enabled", sources: ["subscription"] }] }
} satisfies AccountChoice;

const schedule = {
  id: "20000000-0000-4000-8000-000000000002", account_id: account.account_id, name: "Monday launch review",
  timezone: "America/New_York", recurrence: { frequency: "weekly", weekdays: [1], local_hour: 9, local_minute: 30, gap_policy: "next_valid", overlap_policy: "first" },
  missed_run_policy: "catch_up_one", template: { boardroom_id: "30000000-0000-4000-8000-000000000003", mode: "selected", persona_ids: ["40000000-0000-4000-8000-000000000004"], subject: "Launch review", prompt: "Review launch readiness.", work_item_ids: null, knowledge_fact_ids: null, knowledge_document_ids: null, baseline_assessment_ids: null },
  state: "active", next_run_at: "2026-08-31T13:30:00Z", version: 4,
  created_by: "50000000-0000-4000-8000-000000000005", created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:00:00Z"
} satisfies Schedule;
const personaID = "40000000-0000-4000-8000-000000000004";
const boardroom = { id: schedule.template.boardroom_id, manager_persona_id: personaID, name: "Operations Boardroom", purpose: "Coordinate the week.", state: "active", version: 2, created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:00:00Z" } satisfies AgentBoardroom;
const persona = { id: personaID, boardroom_id: boardroom.id, persona_version_id: "60000000-0000-4000-8000-000000000006", name: "Shop Coordinator", role: "Coordinator", description: "Keeps field work moving.", system_instructions: "Coordinate.", policy: { complexity: "balanced", maximum_input_tokens: 1000, maximum_output_tokens: 1000, maximum_tool_steps: 2, maximum_cost_micros: 1000, citation_policy: "best_effort", action_policy: "propose", tools: [], output_schema: {} }, content_digest: "a".repeat(64), latest_version: 1, state: "active", created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:00:00Z" } satisfies AgentPersona;

async function mountAt(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: "/app/schedules", component: SchedulesView }, { path: "/app/schedules/:scheduleID", component: SchedulesView }
  ] });
  await router.push(path); await router.isReady();
  const wrapper = mount(SchedulesView, { global: { plugins: [router], stubs: { RouterLink: RouterLinkStub } } });
  await flushPromises();
  await expectNoAxeViolations(wrapper.element);
  return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
  for (const mock of Object.values(api)) mock.mockReset();
  api.listSchedules.mockResolvedValue({ items: [schedule] });
  api.getSchedule.mockResolvedValue(schedule);
  api.getScheduleHistory.mockResolvedValue({ items: [] });
  api.listAgentBoardrooms.mockResolvedValue([boardroom]);
  api.listAgentPersonas.mockResolvedValue([persona]);
  const session = useSessionStore();
  session.accounts = [account]; session.selectedID = account.account_id; session.userID = "50000000-0000-4000-8000-000000000005";
});

describe("Schedules surface", () => {
  it("shows an uncertain email outcome without calling it delivered", async () => {
    api.getScheduleHistory.mockResolvedValue({ items: [{ id: "run", occurred_at: schedule.updated_at, outcome: "dispatched", run_state: "succeeded", email_state: "unknown", email_error: "delivery_outcome_unknown", conversation_id: "conversation", boardroom_id: boardroom.id }] });
    const wrapper = await mountAt(`/app/schedules/${schedule.id}`);
    expect(wrapper.text()).toContain("Delivery uncertain");
    expect(wrapper.text()).toContain("Open report");
    expect(wrapper.text()).not.toContain("Mail server accepted");
  });

  it("renders a mobile-first schedule list with durable detail links", async () => {
    const wrapper = await mountAt("/app/schedules");
    expect(api.listSchedules).toHaveBeenCalledWith(account.account_id, undefined);
    expect(wrapper.text()).toContain("Monday launch review");
    expect(wrapper.text()).toContain("Mon at 09:30");
    expect(wrapper.findAllComponents(RouterLinkStub).some((link) => link.props("to") === `/app/schedules/${schedule.id}`)).toBe(true);
  });

  it("uses named Boardroom and Persona choices instead of requiring UUID entry", async () => {
    const wrapper = await mountAt("/app/schedules");
    await wrapper.findAll("button").find((button) => button.text() === "New schedule")?.trigger("click");
    await flushPromises();
    expect(wrapper.text()).toContain("Operations Boardroom");
    expect(wrapper.text()).not.toContain("Boardroom ID");
    const boardroomField = wrapper.findAll("label").find((label) => label.text().startsWith("Agent team"));
    await boardroomField?.get("select").setValue(boardroom.id);
    await flushPromises();
    expect(api.listAgentPersonas).toHaveBeenCalledWith(account.account_id, boardroom.id);
    expect(wrapper.text()).toContain("Shop Coordinator");
    expect(wrapper.text()).not.toContain("Persona IDs");
  });

  it("reloads current durable state after a command conflict", async () => {
    api.pauseSchedule.mockRejectedValue(new APIProblem(409));
    const wrapper = await mountAt(`/app/schedules/${schedule.id}`);
    const pause = wrapper.findAll("button").find((button) => button.text() === "Pause");
    await pause?.trigger("click");
    await wrapper.get("[role=dialog] textarea").setValue("Pause during launch review.");
    await wrapper.get("form[role=dialog]").trigger("submit");
    await flushPromises();
    expect(api.pauseSchedule).toHaveBeenCalledWith(account.account_id, schedule, "Pause during launch review.");
    expect(api.getSchedule).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain("This schedule changed. Review the current version before trying again.");
  });
});
