// @vitest-environment happy-dom
import { flushPromises, mount, RouterLinkStub } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, AgentBoardroom, AgentConversation, AgentMessage, AgentPersona, AgentRun } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";
import AgentsView from "./AgentsView.vue";

const api = vi.hoisted(() => ({
  getAITokenBalance: vi.fn(), getPublicCatalog: vi.fn(),
  listAgentBoardrooms: vi.fn(), createAgentBoardroom: vi.fn(), listAgentPersonas: vi.fn(), configureAgentManager: vi.fn(),
  publishAgentPersona: vi.fn(),
  listAgentConversations: vi.fn(), getAgentConversation: vi.fn(), listAgentMessages: vi.fn(), startAgentRun: vi.fn(),
  getAgentRun: vi.fn(), resolveAgentRun: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => ({ ...await importOriginal<typeof import("@spyglass/api")>(), ...api }));

const account = {
  account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_version: 1, cell_id: "cell-us-east-01",
  display_name: "Northstar Studio", placement_generation: 1, role: "owner", slug: "northstar-studio", owner_enrollment_required: false,
  entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-24T20:00:00Z", version: 3,
    packages: [{ code: "agents", version: 1, mode: "enabled", sources: ["subscription"] }] }
} satisfies AccountChoice;
const room = { id: "20000000-0000-4000-8000-000000000002", manager_persona_id: "30000000-0000-4000-8000-000000000003", name: "Operating review", purpose: "Resolve launch constraints.", state: "active", version: 4, created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:00:00Z" } satisfies AgentBoardroom;
const persona = {
  id: room.manager_persona_id, boardroom_id: room.id, state: "active", latest_version: 2, persona_version_id: "40000000-0000-4000-8000-000000000004",
  name: "Operations Lead", role: "Synthesis manager", description: "Synthesizes evidence and open risks.", system_instructions: "Review the evidence and state bounded recommendations.", content_digest: "a".repeat(64),
  policy: { provider: "configurable", model: "balanced", fallback_models: [], maximum_input_tokens: 10000, maximum_output_tokens: 2000, maximum_cost_micros: 100000, maximum_tool_steps: 2, citation_policy: "required", action_policy: "propose", tools: [], output_schema: {} },
  created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:00:00Z"
} satisfies AgentPersona;
const conversation = { id: "50000000-0000-4000-8000-000000000005", boardroom_id: room.id, subject: "Launch readiness", state: "open", message_count: 2, created_by: "60000000-0000-4000-8000-000000000006", created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:05:00Z" } satisfies AgentConversation;
const message = {
  id: "70000000-0000-4000-8000-000000000007", conversation_id: conversation.id, sequence: 2, role: "persona", body: "Two readiness gaps remain.", run_id: "80000000-0000-4000-8000-000000000008", invocation_id: "90000000-0000-4000-8000-000000000009", persona_version_id: persona.persona_version_id, created_at: "2026-08-24T20:05:00Z",
  result: { contribution: "Two readiness gaps remain.", findings: ["Security review is open."], recommendations: ["Close the review."], questions: [], citations: [], delegations: [], confidence: "high", proposed_actions: [{ kind: "marketing.release.publish", reason: "Publish only after approval.", payload: { secret_internal_field: "must not render" }, evidence: ["release-checklist"] }] }
} satisfies AgentMessage;
const run = { id: message.run_id, boardroom_id: room.id, conversation_id: conversation.id, subject: conversation.subject, prompt: "Review launch readiness.", mode: "selected", state: "succeeded", context: [], context_digest: "b".repeat(64), entitlement_version: 3, plan_digest: "c".repeat(64), policy_version: 2, invocation_ids: [], invocations: [], turns: [], resolutions: [], user_message_id: "a0000000-0000-4000-8000-00000000000a", created_at: "2026-08-24T20:00:00Z" } as AgentRun;

async function mountAt(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: "/app/agents", component: AgentsView }, { path: "/app/agents/boardrooms/:roomID", component: AgentsView },
    { path: "/app/agents/boardrooms/:roomID/conversations/:conversationID", component: AgentsView }
  ] });
  await router.push(path); await router.isReady();
  const wrapper = mount(AgentsView, { global: { plugins: [router], stubs: { RouterLink: RouterLinkStub } } });
  await flushPromises(); await expectNoAxeViolations(wrapper.element); return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
  for (const mock of Object.values(api)) mock.mockReset();
  api.listAgentBoardrooms.mockResolvedValue([room]); api.listAgentPersonas.mockResolvedValue([persona]); api.listAgentConversations.mockResolvedValue([conversation]);
  api.getAITokenBalance.mockResolvedValue({ available: 9000, reserved: 1000, consumed: 500, included: 8000, purchased: 1000, promotion: 0 });
  api.getPublicCatalog.mockResolvedValue({ version: 3, published_at: "2026-08-26T00:00:00Z", packages: [], limits: [], plans: [], offers: [], ai_token_renewal_grant: { code: "team_renewal_v1", version: 1, quantity: 10000, disclosure: "Included" }, ai_token_bundles: [], ai_complexity_rates: [{ code: "balanced_v1", version: 1, complexity: "balanced", input_per_thousand: 4, cached_input_per_thousand: 1, output_per_thousand: 16, tool_invocation: 25, minimum_charge: 20, maximum_reservation: 2500, estimated_minimum: 20, estimated_maximum: 1000 }] });
  api.getAgentConversation.mockResolvedValue(conversation); api.listAgentMessages.mockResolvedValue([message]); api.getAgentRun.mockResolvedValue(run);
  const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; session.userID = "60000000-0000-4000-8000-000000000006";
});

describe("Agents surface", () => {
  it("renders durable Boardroom and Persona policy boundaries", async () => {
    const wrapper = await mountAt(`/app/agents/boardrooms/${room.id}`);
    expect(wrapper.text()).toContain("Operating review"); expect(wrapper.text()).toContain("Operations Lead");
    expect(wrapper.text()).toContain("Complexity"); expect(wrapper.text()).toContain("Balanced"); expect(wrapper.text()).not.toContain("configurable");
    expect(wrapper.text()).toContain("Propose only—human approval remains external"); expect(wrapper.text()).toContain("Convene Boardroom");
    expect(wrapper.findAllComponents(RouterLinkStub).some((link) => link.props("to") === `/app/agents/boardrooms/${room.id}/conversations/${conversation.id}`)).toBe(true);
  });

  it("renders conversation evidence while routing consequential proposals to Your Turn", async () => {
    const wrapper = await mountAt(`/app/agents/boardrooms/${room.id}/conversations/${conversation.id}`);
    expect(api.listAgentMessages).toHaveBeenCalledWith(account.account_id, conversation.id);
    expect(wrapper.text()).toContain("Two readiness gaps remain"); expect(wrapper.text()).toContain("Security review is open");
    expect(wrapper.text()).toContain("Review consequential proposals in Your Turn"); expect(wrapper.text()).not.toContain("secret_internal_field");
  });

  it("publishes a new immutable Persona with bounded proposal policy", async () => {
    api.publishAgentPersona.mockResolvedValue({ ...persona, id: "b0000000-0000-4000-8000-00000000000b", latest_version: 1 });
    const wrapper = await mountAt(`/app/agents/boardrooms/${room.id}`); await wrapper.findAll("button").find((item) => item.text() === "New Persona")?.trigger("click");
    const modal = wrapper.get(".persona-modal"); const inputs = modal.findAll("input:not([type=checkbox])"); await inputs[0]!.setValue("Finance Reviewer"); await inputs[1]!.setValue("Finance specialist"); const textareas = modal.findAll("textarea"); await textareas[0]!.setValue("Reviews operating evidence."); await textareas[1]!.setValue("Review current evidence and make bounded recommendations only.");
    const selects = modal.findAll("select"); await selects[1]!.setValue("propose"); await flushPromises(); const activation = modal.findAll("label").find((item) => item.text().includes("Marketing release activation")); expect(activation).toBeTruthy(); await activation!.find("input").setValue(true); await modal.trigger("submit"); await flushPromises();
    expect(api.publishAgentPersona).toHaveBeenCalledWith(account.account_id, room.id, expect.objectContaining({ expected_latest_version: 0, name: "Finance Reviewer", policy: expect.objectContaining({ provider: "configurable", model: "balanced", action_policy: "propose", action_capabilities: ["marketing.release.activate"], tools: [] }) }));
  });

  it("lets members run Boardrooms without exposing manager configuration", async () => {
    const session = useSessionStore(); session.accounts = [{ ...account, role: "member" }]; const wrapper = await mountAt(`/app/agents/boardrooms/${room.id}`);
    expect(wrapper.text()).toContain("Convene Boardroom"); expect(wrapper.text()).not.toContain("New Persona"); expect(wrapper.text()).not.toContain("Set manager"); expect(wrapper.text()).not.toContain("Publish new version");
  });
});
