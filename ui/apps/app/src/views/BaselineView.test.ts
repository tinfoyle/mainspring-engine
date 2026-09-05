// @vitest-environment happy-dom
import { flushPromises, mount, RouterLinkStub } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, AgentMessage, AgentPersona, AgentRun, BaselineAssessment, KnowledgeFact, WorkItem } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";
import { baselinePersonaDescription, baselinePersonaInstructions, baselineRoomPurpose } from "../baseline-interviewer";
import BaselineView from "./BaselineView.vue";

const api = vi.hoisted(() => ({
  getBaseline: vi.fn(), getCurrentBaseline: vi.fn(), startBaseline: vi.fn(), captureOwnerKnowledgeFact: vi.fn(),
  configureAgentManager: vi.fn(), createAgentBoardroom: vi.fn(), createWorkFromAgentMessage: vi.fn(), getAgentRun: vi.fn(), getWorkItem: vi.fn(),
  listAgentBoardrooms: vi.fn(), listAgentConversations: vi.fn(), listAgentMessages: vi.fn(),
  listAgentPersonas: vi.fn(), listKnowledgeFacts: vi.fn(), listWork: vi.fn(), publishAgentPersona: vi.fn(), resolveAgentRun: vi.fn(), startAgentRun: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => ({ ...await importOriginal<typeof import("@spyglass/api")>(), ...api }));

const account = {
  account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_state: "active", account_version: 1,
  cell_id: "cell-us-east-01", display_name: "Northstar Studio", placement_generation: 1, role: "owner", slug: "northstar", owner_enrollment_required: false,
  entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-24T20:00:00Z", version: 3,
    packages: [
      { code: "knowledge", version: 1, mode: "enabled", sources: ["subscription"] },
      { code: "agents", version: 1, mode: "enabled", sources: ["subscription"] },
      { code: "work", version: 1, mode: "enabled", sources: ["subscription"] }
    ] }
} satisfies AccountChoice;
const baseline = { id: "20000000-0000-4000-8000-000000000002", account_id: account.account_id, catalog_version: "baseline-evidence-2026-08-22", scope_policy_version: "baseline-scope-v1", state: "interview", answers: [], requirements: [], created_by_user_id: "30000000-0000-4000-8000-000000000003", version: 1, created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:00:00Z" } satisfies BaselineAssessment;
const room = { id: "40000000-0000-4000-8000-000000000004", account_id: account.account_id, name: "Business Setup", purpose: baselineRoomPurpose, state: "active", version: 2, manager_persona_id: "50000000-0000-4000-8000-000000000005", created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:00:00Z" } as const;
const persona = { id: "50000000-0000-4000-8000-000000000005", boardroom_id: room.id, state: "active", latest_version: 1, persona_version_id: "60000000-0000-4000-8000-000000000006", name: "Operations Guide", role: "Main operations agent", description: baselinePersonaDescription, system_instructions: baselinePersonaInstructions, content_digest: "a".repeat(64), policy: { complexity: "balanced", maximum_input_tokens: 24000, maximum_output_tokens: 4096, maximum_cost_micros: 250000, maximum_tool_steps: 0, citation_policy: "none", action_policy: "none", tools: [], output_schema: {} }, created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:00:00Z" } satisfies AgentPersona;
const run = { id: "70000000-0000-4000-8000-000000000007", boardroom_id: room.id, conversation_id: "80000000-0000-4000-8000-000000000008", state: "succeeded", mode: "selected", subject: `Business Baseline · ${baseline.id}`, prompt: "We repair commercial HVAC systems.", user_message_id: "90000000-0000-4000-8000-000000000009", turns: [], invocation_ids: [], invocations: [], resolutions: [], context: [], context_digest: "b".repeat(64), entitlement_version: 3, policy_version: 1, plan_digest: "c".repeat(64), created_at: "2026-08-24T20:00:00Z" } satisfies AgentRun;
const result = { contribution: "Got it. How does a new service call reach you today?", findings: [], recommendations: [], questions: [], citations: [], proposed_actions: [], delegations: [], confidence: "medium" as const, baseline: { business_type: "Commercial HVAC service company", business_type_confidence: "medium" as const, captured_topics: ["Commercial HVAC repair"], next_question_key: "baseline.revenue_workflow", next_question: "How does a new service call reach you today?", question_reason: "This shows how demand becomes scheduled work.", automation_offers: [], approved_work: [], ready: false, readiness_reason: "Still learning how work moves.", missing_topics: ["Scheduling", "Billing"] } };
const messages: ReadonlyArray<AgentMessage> = [
  { id: "90000000-0000-4000-8000-000000000009", conversation_id: run.conversation_id, sequence: 1, role: "user", body: "We repair commercial HVAC systems.", created_by: account.account_id, created_at: "2026-08-24T20:01:00Z" },
  { id: "a0000000-0000-4000-8000-00000000000a", conversation_id: run.conversation_id, sequence: 2, role: "persona", body: result.contribution, run_id: run.id, invocation_id: "b0000000-0000-4000-8000-00000000000b", persona_version_id: persona.persona_version_id, result, created_at: "2026-08-24T20:01:05Z" }
];
const fact = { id: "c0000000-0000-4000-8000-00000000000c", account_id: account.account_id, current_claim_id: "d0000000-0000-4000-8000-00000000000d", scope: { kind: "account" }, key: "baseline.revenue_workflow", sensitivity: "internal", state: "active", revision: 1, accepted_by_user_id: "30000000-0000-4000-8000-000000000003", accepted_at: "2026-08-24T20:02:00Z", created_at: "2026-08-24T20:02:00Z", updated_at: "2026-08-24T20:02:00Z" } satisfies KnowledgeFact;

async function mountAt(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/baseline", component: BaselineView }, { path: "/app/baseline/:assessmentID", component: BaselineView }, { path: "/app/your-turn", component: { template: "<div />" } }, { path: "/app/work/:itemID", component: { template: "<div />" } }] });
  await router.push(path); await router.isReady(); const wrapper = mount(BaselineView, { global: { plugins: [router], stubs: { RouterLink: RouterLinkStub } } }); await flushPromises(); await expectNoAxeViolations(wrapper.element); return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia()); for (const mock of Object.values(api)) mock.mockReset();
  api.getBaseline.mockResolvedValue(baseline); api.getCurrentBaseline.mockResolvedValue(baseline); api.startBaseline.mockResolvedValue(baseline);
  api.listKnowledgeFacts.mockResolvedValue([]); api.listWork.mockResolvedValue({ items: [] }); api.listAgentBoardrooms.mockResolvedValue([room]); api.listAgentPersonas.mockResolvedValue([persona]);
  api.listAgentConversations.mockResolvedValue([{ id: run.conversation_id, boardroom_id: room.id, subject: run.subject, state: "open", message_count: 2, created_by: account.account_id, created_at: run.created_at, updated_at: run.created_at }]);
  api.listAgentMessages.mockResolvedValue(messages); api.getAgentRun.mockResolvedValue(run); api.startAgentRun.mockResolvedValue(run); api.captureOwnerKnowledgeFact.mockResolvedValue(fact);
  const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; session.userID = "30000000-0000-4000-8000-000000000003";
});

describe("conversational Business Baseline", () => {
  it("resumes the real operations-agent conversation", async () => {
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(api.getBaseline).toHaveBeenCalledWith(account.account_id, baseline.id); expect(wrapper.text()).toContain("How does a new service call reach you today?");
    expect(wrapper.text()).toContain("Commercial HVAC service company"); expect(wrapper.text()).not.toContain("Question 3 of 7");
    expect(wrapper.get("#baseline-reply").attributes("placeholder")).toBe("Type your answer here…");
  });

  it("puts a structured next question in the agent chat rather than the human input", async () => {
    const introduction = "I understand the kind of work you do. Let’s look at how customers reach you.";
    api.listAgentMessages.mockResolvedValue([messages[0], { ...messages[1], body: introduction, result: { ...result, contribution: introduction } }]);
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(wrapper.get(".baseline-message--persona .baseline-message-body").text()).toContain("How does a new service call reach you today?");
    expect(wrapper.get("#baseline-reply").attributes("placeholder")).toBe("Type your answer here…");
    expect(wrapper.get("#baseline-reply").element).toHaveProperty("value", "");
  });

  it("renders persisted Agent results whose empty collections were encoded as null", async () => {
    const nullableResult = { ...result, baseline: { ...result.baseline, captured_topics: null, automation_offers: null, approved_work: null, missing_topics: null } } as unknown as AgentMessage["result"];
    api.listAgentMessages.mockResolvedValue([messages[0], { ...messages[1], result: nullableResult }]);
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(wrapper.text()).toContain("How does a new service call reach you today?");
    expect(wrapper.text()).toContain("Commercial HVAC service company");
    expect(wrapper.find(".baseline-offers").exists()).toBe(false);
  });

  it("saves the answer under the question selected by the agent before continuing", async () => {
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`); const answer = "Calls come from Google and go onto a whiteboard.";
    await wrapper.get("#baseline-reply").setValue(answer); await wrapper.get("form.baseline-composer").trigger("submit"); await flushPromises();
    expect(api.captureOwnerKnowledgeFact).toHaveBeenCalledWith(account.account_id, "baseline.revenue_workflow", answer, `baseline/${baseline.id}/conversation/${run.conversation_id}/baseline.revenue_workflow`);
    expect(api.startAgentRun).toHaveBeenCalledWith(account.account_id, room.id, expect.objectContaining({ prompt: answer, conversation_id: run.conversation_id, persona_ids: [persona.id] }));
  });

  it("sends with Enter and keeps Shift+Enter for a new line", async () => {
    api.startAgentRun.mockResolvedValue({ ...run, id: "71000000-0000-4000-8000-000000000007", user_message_id: "91000000-0000-4000-8000-000000000009", state: "running" });
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`); const textarea = wrapper.get("#baseline-reply");
    await textarea.setValue("First line"); await textarea.trigger("keydown", { key: "Enter", shiftKey: true }); await flushPromises();
    expect(api.startAgentRun).not.toHaveBeenCalled(); expect(textarea.element).toHaveProperty("value", "First line");
    await textarea.setValue("First line\nSecond line"); await textarea.trigger("keydown", { key: "Enter" }); await flushPromises();
    expect(api.startAgentRun).toHaveBeenCalledWith(account.account_id, room.id, expect.objectContaining({ prompt: "First line\nSecond line" }));
    expect(textarea.element).toHaveProperty("value", "");
    wrapper.unmount();
  });

  it("shows a reply immediately while the operations agent is still working", async () => {
    api.startAgentRun.mockResolvedValue({ ...run, id: "71000000-0000-4000-8000-000000000007", user_message_id: "91000000-0000-4000-8000-000000000009", state: "running" });
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`); const answer = "Most calls come in by phone and text.";
    await wrapper.get("#baseline-reply").setValue(answer); await wrapper.get("form.baseline-composer").trigger("submit"); await flushPromises();
    expect(wrapper.text()).toContain(answer);
    expect(wrapper.get("#baseline-reply").element).toHaveProperty("value", "");
    expect(wrapper.text()).toContain("Thinking");
    wrapper.unmount();
  });

  it("records an automation approval as a compact chat event without filling the reply box", async () => {
    const offer = { key: "schedule.daily_dispatch", title: "Set up a daily dispatch review", description: "Review tomorrow's schedule and flag gaps.", priority: "high" as const };
    api.listAgentMessages.mockResolvedValue([messages[0], { ...messages[1], result: { ...result, baseline: { ...result.baseline, automation_offers: [offer] } } }]);
    api.startAgentRun.mockResolvedValue({ ...run, id: "71000000-0000-4000-8000-000000000007", user_message_id: "91000000-0000-4000-8000-000000000009", state: "running" });
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    await wrapper.get(".baseline-offers button").trigger("click"); await flushPromises();
    expect(api.startAgentRun).toHaveBeenCalledWith(account.account_id, room.id, expect.objectContaining({ prompt: `Yes, add “${offer.title}” to the work we will set up.` }));
    expect(wrapper.text()).toContain(`Approved setup work: ${offer.title}`);
    expect(wrapper.text()).not.toContain(`Yes, add “${offer.title}”`);
    expect(wrapper.find(".baseline-offers").exists()).toBe(false);
    expect(wrapper.get("#baseline-reply").element).toHaveProperty("value", "");
    wrapper.unmount();
  });

  it("creates approved Work directly for the operations agent without consuming the interview conversation", async () => {
    const approvedWork = { key: "schedule.daily_dispatch", title: "Set up a daily dispatch review", description: "Choose the schedule source and decide who handles exceptions.", priority: "high" as const };
    const approved = { ...result, contribution: "I added that setup job.", baseline: { ...result.baseline, approved_work: [approvedWork] } };
    const approvalMessage = { ...messages[1], id: "e0000000-0000-4000-8000-00000000000e", result: approved };
    const created = { id: approvalMessage.id, number: 1, depth: 0, kind: "todo", title: approvedWork.title, description: approvedWork.description, state: "open", priority: "high", assignment: { responsibility: "persona", persona_id: persona.id }, provenance: { source: "manual", created_by: { kind: "user", id: account.account_id } }, version: 1, created_at: run.created_at, updated_at: run.created_at } satisfies WorkItem;
    api.listAgentMessages.mockResolvedValue([messages[0], approvalMessage]); api.getWorkItem.mockRejectedValueOnce({ name: "APIProblem", status: 404 }); api.createWorkFromAgentMessage.mockResolvedValue(created);
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(api.createWorkFromAgentMessage).toHaveBeenCalledWith(account.account_id, approvalMessage.id, expect.objectContaining({
      title: "Set up a daily dispatch review", assignment: { responsibility: "persona", persona_id: persona.id }
    }));
    expect(wrapper.text()).toContain("Set up a daily dispatch review");
  });

  it("does not relink or reassign approved Work that was already repaired", async () => {
    const approvedWork = { key: "schedule.daily_dispatch", title: "Set up a daily dispatch review", description: "Choose the schedule source.", priority: "high" as const };
    const approvalMessage = { ...messages[1], id: "e0000000-0000-4000-8000-00000000000e", result: { ...result, baseline: { ...result.baseline, approved_work: [approvedWork] } } };
    const complete = { id: approvalMessage.id, number: 1, depth: 0, kind: "todo", title: approvedWork.title, description: approvedWork.description, state: "open", priority: "high", assignment: { responsibility: "persona", persona_id: persona.id }, provenance: { source: "conversation", created_by: { kind: "user", id: account.account_id }, conversation_id: run.conversation_id }, version: 3, created_at: run.created_at, updated_at: run.created_at } satisfies WorkItem;
    api.listAgentMessages.mockResolvedValue([messages[0], approvalMessage]); api.listWork.mockResolvedValue({ items: [complete] });
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(api.getWorkItem).not.toHaveBeenCalled(); expect(api.createWorkFromAgentMessage).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain(complete.title);
  });

  it("loads the existing Work when creation races with an earlier repair", async () => {
    const approvedWork = { key: "schedule.daily_dispatch", title: "Set up a daily dispatch review", description: "Choose the schedule source.", priority: "high" as const };
    const approvalMessage = { ...messages[1], id: "e0000000-0000-4000-8000-00000000000e", result: { ...result, baseline: { ...result.baseline, approved_work: [approvedWork] } } };
    const complete = { id: approvalMessage.id, number: 1, depth: 0, kind: "todo", title: approvedWork.title, description: approvedWork.description, state: "open", priority: "high", assignment: { responsibility: "persona", persona_id: persona.id }, provenance: { source: "conversation", created_by: { kind: "user", id: account.account_id }, conversation_id: run.conversation_id }, version: 3, created_at: run.created_at, updated_at: run.created_at } satisfies WorkItem;
    api.listAgentMessages.mockResolvedValue([messages[0], approvalMessage]);
    api.getWorkItem.mockRejectedValueOnce({ name: "APIProblem", status: 404 }).mockResolvedValueOnce(complete);
    api.createWorkFromAgentMessage.mockRejectedValue({ name: "APIProblem", status: 412 });
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(api.createWorkFromAgentMessage).toHaveBeenCalledOnce(); expect(api.getWorkItem).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain(complete.title);
  });

  it("reconciles a durable reply before an unrelated setup Work repair fails", async () => {
    const approvedWork = { key: "schedule.daily_dispatch", title: "Set up a daily dispatch review", description: "Choose the schedule source.", priority: "high" as const };
    const approvalMessage = { ...messages[1], id: "e0000000-0000-4000-8000-00000000000e", result: { ...result, baseline: { ...result.baseline, approved_work: [approvedWork] } } };
    const answer = "Calls are scheduled from a shared inbox.";
    const followupRun = { ...run, id: "71000000-0000-4000-8000-000000000007", user_message_id: "91000000-0000-4000-8000-000000000009" };
    const durableAnswer = { ...messages[0], id: followupRun.user_message_id, sequence: 3, body: answer, created_at: "2026-08-24T20:03:00Z" };
    api.listAgentMessages.mockResolvedValueOnce(messages).mockResolvedValueOnce([messages[0], approvalMessage, durableAnswer]);
    api.startAgentRun.mockResolvedValue(followupRun); api.getWorkItem.mockRejectedValue(new Error("the Work item changed; reload it before retrying"));
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    await wrapper.get("#baseline-reply").setValue(answer); await wrapper.get("form.baseline-composer").trigger("submit"); await flushPromises();
    expect(wrapper.findAll(".baseline-message--user").filter((item) => item.text().includes(answer))).toHaveLength(1);
    expect(wrapper.text()).toContain("the Work item changed; reload it before retrying");
  });

  it("automatically retries malformed Agent output without duplicating the owner's answer", async () => {
    const failedRun = { ...run, id: "71000000-0000-4000-8000-000000000007", state: "failed" as const, user_message_id: "91000000-0000-4000-8000-000000000009", invocations: [{ id: "b1000000-0000-4000-8000-00000000000b", turn: 1, persona_version_id: persona.persona_version_id, status: "failed" as const, failure_code: "model_output_invalid", completed_at: "2026-08-24T20:04:00Z" }] };
    const retryRun = { ...run, id: "72000000-0000-4000-8000-000000000007", user_message_id: failedRun.user_message_id };
    const failedAnswer = { ...messages[0], id: failedRun.user_message_id, sequence: 3, body: "We want to launch as soon as it is ready.", run_id: failedRun.id, created_at: "2026-08-24T20:03:00Z" };
    api.listAgentMessages.mockResolvedValue([messages[0], messages[1], failedAnswer]);
    api.getAgentRun.mockImplementation((_: string, runID: string) => Promise.resolve(runID === failedRun.id ? failedRun : retryRun));
    api.resolveAgentRun.mockResolvedValue({ id: "73000000-0000-4000-8000-000000000007", run_id: failedRun.id, retry_run_id: retryRun.id, action: "retry_failed", note: "Automatically retry malformed Business Baseline output.", actor_id: account.account_id, created_at: "2026-08-24T20:04:01Z" });
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`); await flushPromises();
    expect(api.resolveAgentRun).toHaveBeenCalledWith(account.account_id, failedRun.id, { action: "retry_failed", note: "Automatically retry malformed Business Baseline output." });
    expect(api.startAgentRun).not.toHaveBeenCalled();
    expect(wrapper.findAll(".baseline-message--user").filter((item) => item.text().includes(failedAnswer.body))).toHaveLength(1);
    wrapper.unmount();
  });

  it("shows a plain-language retry action after a terminal failure", async () => {
    const failedRun = { ...run, id: "71000000-0000-4000-8000-000000000007", state: "failed" as const, user_message_id: "91000000-0000-4000-8000-000000000009", invocations: [{ id: "b1000000-0000-4000-8000-00000000000b", turn: 1, persona_version_id: persona.persona_version_id, status: "failed" as const, failure_code: "token_limit_exceeded", completed_at: "2026-08-24T20:04:00Z" }] };
    const retryRun = { ...run, id: "72000000-0000-4000-8000-000000000007", state: "running" as const, user_message_id: failedRun.user_message_id };
    const failedAnswer = { ...messages[0], id: failedRun.user_message_id, sequence: 3, body: "We want to launch as soon as it is ready.", run_id: failedRun.id, created_at: "2026-08-24T20:03:00Z" };
    api.listAgentMessages.mockResolvedValue([messages[0], messages[1], failedAnswer]);
    api.getAgentRun.mockImplementation((_: string, runID: string) => Promise.resolve(runID === failedRun.id ? failedRun : retryRun));
    api.resolveAgentRun.mockResolvedValue({ id: "73000000-0000-4000-8000-000000000007", run_id: failedRun.id, retry_run_id: retryRun.id, action: "retry_failed", note: "Retry the Business Baseline reply without duplicating the owner's answer.", actor_id: account.account_id, created_at: "2026-08-24T20:04:01Z" });
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(wrapper.text()).toContain("Your agent could not finish this reply."); expect(wrapper.text()).toContain("Nothing needs to be retyped");
    expect(wrapper.text()).not.toContain("Business setup complete");
    expect(wrapper.find("#baseline-reply").exists()).toBe(false);
    await wrapper.get(".baseline-run-recovery button").trigger("click"); await flushPromises();
    expect(api.resolveAgentRun).toHaveBeenCalledWith(account.account_id, failedRun.id, { action: "retry_failed", note: "Retry the Business Baseline reply without duplicating the owner's answer." });
    wrapper.unmount();
  });

  it("ends by handing the owner to Your Turn", async () => {
    const readyResult = { ...result, contribution: "I have enough to get started.", baseline: { ...result.baseline, captured_topics: ["Customers", "Work flow", "Scheduling", "Existing records"], ready: true, next_question_key: "", next_question: "", question_reason: "", readiness_reason: "I understand how work enters, gets scheduled, and gets billed.", missing_topics: [] } };
    api.listAgentMessages.mockResolvedValue([messages[0], { ...messages[1], result: readyResult }]); const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(wrapper.text()).toContain("Business setup complete"); expect(wrapper.text()).toContain("Continue to Your Turn");
  });
});
