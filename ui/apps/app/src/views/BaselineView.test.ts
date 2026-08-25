// @vitest-environment happy-dom
import { flushPromises, mount, RouterLinkStub } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { APIProblem, type AccountChoice, type BaselineAssessment, type KnowledgeFactSummary } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";
import BaselineView from "./BaselineView.vue";

const api = vi.hoisted(() => ({
  getBaseline: vi.fn(), getCurrentBaseline: vi.fn(), startBaseline: vi.fn(), answerBaseline: vi.fn(),
  captureOwnerKnowledgeFact: vi.fn(),
  listKnowledgeFacts: vi.fn(), listWork: vi.fn(), listIntegrationConnections: vi.fn(), listBaselineSourceGrants: vi.fn(),
  approveBaselinePlan: vi.fn(), beginBaselineInventory: vi.fn(), completeBaselineInventory: vi.fn(),
  confirmBaselineWorkEvidence: vi.fn(), createBaselineSourceGrant: vi.fn(), decideBaselineEvidence: vi.fn(),
  dispositionBaselineRequirement: vi.fn(), getIntegrationConnection: vi.fn(), markBaselineReady: vi.fn(),
  materializeBaselineMaintenance: vi.fn(), materializeBaselinePlan: vi.fn(), reassessBaseline: vi.fn(),
  revokeBaselineSourceGrant: vi.fn(), submitBaselinePlan: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => ({ ...await importOriginal<typeof import("@spyglass/api")>(), ...api }));

const account = {
  account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_version: 1, cell_id: "cell-us-east-01", display_name: "Northstar Studio", placement_generation: 1, role: "owner", slug: "northstar", owner_enrollment_required: false,
  entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-24T20:00:00Z", version: 3, packages: [{ code: "knowledge", version: 1, mode: "enabled", sources: ["subscription"] }, { code: "integrations", version: 1, mode: "enabled", sources: ["subscription"] }] }
} satisfies AccountChoice;
const baseline = { id: "20000000-0000-4000-8000-000000000002", account_id: account.account_id, catalog_version: "baseline-evidence-2026-08-22", scope_policy_version: "baseline-scope-v1", state: "interview", answers: [], requirements: [], created_by_user_id: "30000000-0000-4000-8000-000000000003", version: 1, created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:00:00Z" } satisfies BaselineAssessment;
const fact = { id: "40000000-0000-4000-8000-000000000004", current_claim_id: "50000000-0000-4000-8000-000000000005", scope: { kind: "account" }, key: "organization.legal_name", sensitivity: "internal", state: "active", revision: 2, accepted_at: "2026-08-24T19:00:00Z", updated_at: "2026-08-24T19:00:00Z" } satisfies KnowledgeFactSummary;

async function mountAt(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/baseline", component: BaselineView }, { path: "/app/baseline/:assessmentID", component: BaselineView }, { path: "/app/knowledge", component: { template: "<div />" } }, { path: "/app/work/:itemID", component: { template: "<div />" } }] });
  await router.push(path); await router.isReady();
  const wrapper = mount(BaselineView, { global: { plugins: [router], stubs: { RouterLink: RouterLinkStub } } }); await flushPromises(); await expectNoAxeViolations(wrapper.element); return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia()); for (const mock of Object.values(api)) mock.mockReset();
  api.getBaseline.mockResolvedValue(baseline); api.getCurrentBaseline.mockResolvedValue(baseline); api.listKnowledgeFacts.mockResolvedValue([fact]); api.listWork.mockResolvedValue({ items: [] }); api.listIntegrationConnections.mockResolvedValue({ items: [] }); api.listBaselineSourceGrants.mockResolvedValue({ items: [] }); api.captureOwnerKnowledgeFact.mockResolvedValue(fact); api.answerBaseline.mockResolvedValue({ ...baseline, answers: [{ question_key: "organization.legal_name", kind: "fact", fact: { fact_id: fact.id, revision: fact.revision }, reason: "", answered_by_user_id: "30000000-0000-4000-8000-000000000003", answered_at: "2026-08-24T20:01:00Z" }], version: 2 });
  const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; session.userID = "30000000-0000-4000-8000-000000000003";
});

describe("Business Baseline surface", () => {
  it("resumes a durable interview and binds an exact confirmed fact", async () => {
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(api.getBaseline).toHaveBeenCalledWith(account.account_id, baseline.id); expect(wrapper.text()).toContain("What is the legal or registered name");
    await wrapper.get('input[value="fact"]').setValue(); await flushPromises();
    expect(wrapper.text()).toContain("revision 2");
    await wrapper.get("select").setValue(fact.id); await wrapper.get("form.baseline-form").trigger("submit"); await flushPromises();
    expect(api.answerBaseline).toHaveBeenCalledWith(account.account_id, baseline, { question_key: "organization.legal_name", kind: "fact", fact: { fact_id: fact.id, revision: 2 } });
    expect(wrapper.text()).toContain("What is the business website?");
  });

  it("offers an owner a clean start when no current assessment exists", async () => {
    api.getCurrentBaseline.mockRejectedValue(new APIProblem(404)); const wrapper = await mountAt("/app/baseline");
    expect(wrapper.text()).toContain("You can leave and resume on any device"); expect(wrapper.text()).toContain("Start my Baseline");
  });

  it("confirms a new owner statement through Knowledge before answering Baseline", async () => {
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`); await wrapper.get(".baseline-form > label input").setValue("Northstar Studio LLC"); await wrapper.get("form.baseline-form").trigger("submit"); await flushPromises();
    expect(api.captureOwnerKnowledgeFact).toHaveBeenCalledWith(account.account_id, "organization.legal_name", "Northstar Studio LLC", `baseline/${baseline.id}/organization.legal_name`);
    expect(api.answerBaseline).toHaveBeenCalledWith(account.account_id, baseline, { question_key: "organization.legal_name", kind: "fact", fact: { fact_id: fact.id, revision: 2 } });
  });
});
