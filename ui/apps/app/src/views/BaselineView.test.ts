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
  captureOwnerKnowledgeEvidence: vi.fn(), captureOwnerKnowledgeFact: vi.fn(),
  listKnowledgeFacts: vi.fn(), listWork: vi.fn(), listIntegrationConnections: vi.fn(), listBaselineSourceGrants: vi.fn(),
  approveBaselinePlan: vi.fn(), beginBaselineInventory: vi.fn(), completeBaselineInventory: vi.fn(),
  confirmBaselineWorkEvidence: vi.fn(), createBaselineSourceGrant: vi.fn(), decideBaselineEvidence: vi.fn(),
  dispositionBaselineRequirement: vi.fn(), getIntegrationConnection: vi.fn(), markBaselineReady: vi.fn(),
  materializeBaselineMaintenance: vi.fn(), materializeBaselinePlan: vi.fn(), reassessBaseline: vi.fn(),
  revokeBaselineSourceGrant: vi.fn(), submitBaselinePlan: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => ({ ...await importOriginal<typeof import("@spyglass/api")>(), ...api }));

const account = {
  account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_state: "active", account_version: 1, cell_id: "cell-us-east-01", display_name: "Northstar Studio", placement_generation: 1, role: "owner", slug: "northstar", owner_enrollment_required: false,
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
  api.getBaseline.mockResolvedValue(baseline); api.getCurrentBaseline.mockResolvedValue(baseline); api.listKnowledgeFacts.mockResolvedValue([fact]); api.listWork.mockResolvedValue({ items: [] }); api.listIntegrationConnections.mockResolvedValue({ items: [] }); api.listBaselineSourceGrants.mockResolvedValue({ items: [] }); api.captureOwnerKnowledgeEvidence.mockResolvedValue({ id: "60000000-0000-4000-8000-000000000006" }); api.captureOwnerKnowledgeFact.mockResolvedValue(fact); api.answerBaseline.mockResolvedValue({ ...baseline, answers: [{ question_key: "organization.legal_name", kind: "fact", fact: { fact_id: fact.id, revision: fact.revision }, reason: "", answered_by_user_id: "30000000-0000-4000-8000-000000000003", answered_at: "2026-08-24T20:01:00Z" }], version: 2 });
  const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; session.userID = "30000000-0000-4000-8000-000000000003";
});

describe("Business Baseline surface", () => {
  it("resumes a durable interview and binds an exact confirmed fact", async () => {
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(api.getBaseline).toHaveBeenCalledWith(account.account_id, baseline.id); expect(wrapper.text()).toContain("What is the legal or registered name");
    await wrapper.get('input[value="fact"]').setValue(); await flushPromises();
    expect(wrapper.text()).toContain("Saved answer");
    await wrapper.get("select").setValue(fact.id); await wrapper.get("form.baseline-form").trigger("submit"); await flushPromises();
    expect(api.answerBaseline).toHaveBeenCalledWith(account.account_id, baseline, { question_key: "organization.legal_name", kind: "fact", fact: { fact_id: fact.id, revision: 2 } });
    expect(wrapper.text()).toContain("Does your business have a website?");
  });

  it("offers an owner a clean start when no current assessment exists", async () => {
    const crossBundleProblem = Object.assign(new Error("Request failed with status 404"), { name: "APIProblem", status: 404 });
    expect(crossBundleProblem).not.toBeInstanceOf(APIProblem);
    api.getCurrentBaseline.mockRejectedValue(crossBundleProblem); const wrapper = await mountAt("/app/baseline");
    expect(wrapper.text()).toContain("stop and come back"); expect(wrapper.text()).toContain("Let’s get started");
  });

  it("confirms a new owner statement through Knowledge before answering Baseline", async () => {
    const wrapper = await mountAt(`/app/baseline/${baseline.id}`); await wrapper.get(".baseline-form > label textarea").setValue("Northstar Studio LLC"); await wrapper.get("form.baseline-form").trigger("submit"); await flushPromises();
    expect(api.captureOwnerKnowledgeFact).toHaveBeenCalledWith(account.account_id, "organization.legal_name", "Northstar Studio LLC", `baseline/${baseline.id}/organization.legal_name`);
    expect(api.answerBaseline).toHaveBeenCalledWith(account.account_id, baseline, { question_key: "organization.legal_name", kind: "fact", fact: { fact_id: fact.id, revision: 2 } });
  });

  it("turns gap review into a plain-language conversation without exposing evidence IDs", async () => {
    const requirement = { id: "70000000-0000-4000-8000-000000000007", code: "customer_feedback", title: "Customer issue and feedback record", responsibility: { kind: "user" as const }, renew_after_days: 90, catalog_version: baseline.catalog_version, scope_policy_version: baseline.scope_policy_version, disposition: "pending" as const, reason: "", evidence: [] };
    const reviewBaseline = { ...baseline, state: "gap_review" as const, requirements: [requirement], version: 8 };
    api.getBaseline.mockResolvedValue(reviewBaseline);
    api.decideBaselineEvidence
      .mockRejectedValueOnce(new Error("The decision could not be saved."))
      .mockResolvedValueOnce({ ...reviewBaseline, requirements: [{ ...requirement, disposition: "satisfied" }], version: 9 });

    const wrapper = await mountAt(`/app/baseline/${baseline.id}`);
    expect(wrapper.text()).toContain("Do you have one place to track customer problems, complaints, and feedback?");
    expect(wrapper.text()).not.toContain("Knowledge evidence ID");
    await wrapper.get('input[value="accepted"]').setValue();
    await wrapper.get(".baseline-reply > label textarea").setValue("We log customer complaints in Jobber and review them every Friday.");
    await wrapper.get("form.baseline-reply").trigger("submit");
    await flushPromises();

    expect(api.captureOwnerKnowledgeEvidence).toHaveBeenCalledWith(account.account_id, "We log customer complaints in Jobber and review them every Friday.", `baseline/${baseline.id}/requirements/customer_feedback/owner-confirmation`);
    expect(wrapper.text()).toContain("The decision could not be saved.");
    await wrapper.get("form.baseline-reply").trigger("submit");
    await flushPromises();
    expect(api.captureOwnerKnowledgeEvidence).toHaveBeenCalledTimes(1);
    expect(api.decideBaselineEvidence).toHaveBeenCalledTimes(2);
    expect(api.decideBaselineEvidence).toHaveBeenCalledWith(account.account_id, reviewBaseline, { requirement_id: requirement.id, evidence_id: "60000000-0000-4000-8000-000000000006", decision: "accepted", reason: "We log customer complaints in Jobber and review them every Friday." });
  });
});
