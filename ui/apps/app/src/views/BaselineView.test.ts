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
  getBaseline: vi.fn(), getCurrentBaseline: vi.fn(), startBaseline: vi.fn(), assignWork: vi.fn(), captureOwnerKnowledgeFact: vi.fn(),
  configureAgentManager: vi.fn(), createAgentBoardroom: vi.fn(), createWorkFromAgentMessage: vi.fn(), getAgentRun: vi.fn(),
  linkWorkConversation: vi.fn(), listAgentBoardrooms: vi.fn(), listAgentConversations: vi.fn(), listAgentMessages: vi.fn(),
  listAgentPersonas: vi.fn(), listKnowledgeFacts: vi.fn(), listWork: vi.fn(), publishAgentPersona: vi.fn(), startAgentRun: vi.fn()
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
  });

  it("saves the answer under the question selected by the agent before continuing", async () => {
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`); const answer = "Calls come from Google and go onto a whiteboard.";
    await wrapper.get("#baseline-reply").setValue(answer); await wrapper.get("form.baseline-composer").trigger("submit"); await flushPromises();
    expect(api.captureOwnerKnowledgeFact).toHaveBeenCalledWith(account.account_id, "baseline.revenue_workflow", answer, `baseline/${baseline.id}/conversation/${run.conversation_id}/baseline.revenue_workflow`);
    expect(api.startAgentRun).toHaveBeenCalledWith(account.account_id, room.id, expect.objectContaining({ prompt: answer, conversation_id: run.conversation_id, persona_ids: [persona.id] }));
  });

  it("creates and conversation-links Work only after the agent records explicit approval", async () => {
    const approvedWork = { key: "schedule.daily_dispatch", title: "Set up a daily dispatch review", description: "Choose the schedule source and decide who handles exceptions.", priority: "high" as const };
    const approved = { ...result, contribution: "I added that setup job.", baseline: { ...result.baseline, approved_work: [approvedWork] } };
    const approvalMessage = { ...messages[1], id: "e0000000-0000-4000-8000-00000000000e", result: approved };
    const created = { id: approvalMessage.id, number: 1, depth: 0, kind: "todo", title: approvedWork.title, description: approvedWork.description, state: "open", priority: "high", assignment: { responsibility: "shared" }, provenance: { source: "manual", created_by: { kind: "user", id: account.account_id } }, version: 1, created_at: run.created_at, updated_at: run.created_at } satisfies WorkItem;
    const linked = { ...created, provenance: { ...created.provenance, source: "conversation" as const, conversation_id: run.conversation_id }, version: 2 };
    const assigned = { ...linked, assignment: { responsibility: "persona" as const, persona_id: persona.id }, version: 3 };
    api.listAgentMessages.mockResolvedValue([messages[0], approvalMessage]); api.createWorkFromAgentMessage.mockResolvedValue(created); api.linkWorkConversation.mockResolvedValue(linked); api.assignWork.mockResolvedValue(assigned);
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(api.createWorkFromAgentMessage).toHaveBeenCalledWith(account.account_id, approvalMessage.id, expect.objectContaining({ title: "Set up a daily dispatch review" }));
    expect(api.linkWorkConversation).toHaveBeenCalled();
    expect(api.assignWork).toHaveBeenCalledWith(account.account_id, linked, expect.objectContaining({ assignment: { responsibility: "persona", persona_id: persona.id } }));
    expect(wrapper.text()).toContain("Set up a daily dispatch review");
  });

  it("ends by handing the owner to Your Turn", async () => {
    const readyResult = { ...result, contribution: "I have enough to get started.", baseline: { ...result.baseline, captured_topics: ["Customers", "Work flow", "Scheduling", "Existing records"], ready: true, next_question_key: "", next_question: "", question_reason: "", readiness_reason: "I understand how work enters, gets scheduled, and gets billed.", missing_topics: [] } };
    api.listAgentMessages.mockResolvedValue([messages[0], { ...messages[1], result: readyResult }]); const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(wrapper.text()).toContain("Baseline established"); expect(wrapper.text()).toContain("Continue to Your Turn");
  });
});
