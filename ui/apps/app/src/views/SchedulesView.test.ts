// @vitest-environment happy-dom
import { flushPromises, mount, RouterLinkStub } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { APIProblem, type AccountChoice, type Schedule } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import SchedulesView from "./SchedulesView.vue";

const api = vi.hoisted(() => ({
  listSchedules: vi.fn(), getSchedule: vi.fn(), createSchedule: vi.fn(), reviseSchedule: vi.fn(),
  pauseSchedule: vi.fn(), resumeSchedule: vi.fn(), deleteSchedule: vi.fn(), triggerSchedule: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => ({ ...await importOriginal<typeof import("@spyglass/api")>(), ...api }));

const account = {
  account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_version: 1,
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

async function mountAt(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: "/app/schedules", component: SchedulesView }, { path: "/app/schedules/:scheduleID", component: SchedulesView }
  ] });
  await router.push(path); await router.isReady();
  const wrapper = mount(SchedulesView, { global: { plugins: [router], stubs: { RouterLink: RouterLinkStub } } });
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
  for (const mock of Object.values(api)) mock.mockReset();
  api.listSchedules.mockResolvedValue({ items: [schedule] });
  api.getSchedule.mockResolvedValue(schedule);
  const session = useSessionStore();
  session.accounts = [account]; session.selectedID = account.account_id; session.userID = "50000000-0000-4000-8000-000000000005";
});

describe("Schedules surface", () => {
  it("renders a mobile-first schedule list with durable detail links", async () => {
    const wrapper = await mountAt("/app/schedules");
    expect(api.listSchedules).toHaveBeenCalledWith(account.account_id, undefined);
    expect(wrapper.text()).toContain("Monday launch review");
    expect(wrapper.text()).toContain("Mon at 09:30");
    expect(wrapper.findAllComponents(RouterLinkStub).some((link) => link.props("to") === `/app/schedules/${schedule.id}`)).toBe(true);
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
