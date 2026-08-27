// @vitest-environment happy-dom
import { flushPromises, mount, RouterLinkStub } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, KnowledgeClaim, KnowledgeClaimSummary, KnowledgeFactSummary } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";
import KnowledgeView from "./KnowledgeView.vue";

const api = vi.hoisted(() => ({ listProposedKnowledgeClaims: vi.fn(), listKnowledgeFacts: vi.fn(), getKnowledgeClaim: vi.fn(), decideKnowledgeClaim: vi.fn() }));
vi.mock("@spyglass/api", async (importOriginal) => ({ ...await importOriginal<typeof import("@spyglass/api")>(), ...api }));

const account = {
  account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_state: "active", account_version: 1, cell_id: "cell-us-east-01",
  display_name: "Northstar Studio", placement_generation: 1, role: "owner", slug: "northstar-studio", owner_enrollment_required: false,
  entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-24T20:00:00Z", version: 3,
    packages: [{ code: "knowledge", version: 1, mode: "enabled", sources: ["subscription"] }] }
} satisfies AccountChoice;
const summary = {
  id: "20000000-0000-4000-8000-000000000002", scope: { kind: "account" }, key: "launch.release_window", confidence: 920,
  sensitivity: "internal", state: "proposed", proposed_by: { kind: "workload", id: "planning-agent" }, version: 3,
  created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:05:00Z"
} satisfies KnowledgeClaimSummary;
const claim = {
  ...summary, account_id: account.account_id, value: "August 31", value_sha256: "a".repeat(64), hash_version: 1,
  citations: [{ evidence_id: "30000000-0000-4000-8000-000000000003", evidence_kind: "document_revision", relation: "supports", locator: "Launch plan, page 4" }]
} satisfies KnowledgeClaim;
const fact = {
  id: "40000000-0000-4000-8000-000000000004", current_claim_id: claim.id, scope: { kind: "account" }, key: "launch.owner",
  sensitivity: "internal", state: "active", revision: 2, accepted_at: "2026-08-23T20:00:00Z", updated_at: "2026-08-23T20:00:00Z"
} satisfies KnowledgeFactSummary;

async function mountAt(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: "/app/knowledge", component: KnowledgeView }, { path: "/app/knowledge/claims/:claimID", component: KnowledgeView }
  ] });
  await router.push(path); await router.isReady();
  const wrapper = mount(KnowledgeView, { global: { plugins: [router], stubs: { RouterLink: RouterLinkStub } } });
  await flushPromises(); await expectNoAxeViolations(wrapper.element); return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
  for (const mock of Object.values(api)) mock.mockReset();
  api.listProposedKnowledgeClaims.mockResolvedValue([summary]); api.listKnowledgeFacts.mockResolvedValue([fact]); api.getKnowledgeClaim.mockResolvedValue(claim);
  const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; session.userID = "50000000-0000-4000-8000-000000000005";
});

describe("Knowledge surface", () => {
  it("separates proposed review from the accepted fact projection", async () => {
    const wrapper = await mountAt("/app/knowledge");
    expect(wrapper.text()).toContain("launch.release_window"); expect(wrapper.text()).toContain("92% confidence");
    expect(wrapper.text()).toContain("launch.owner"); expect(wrapper.text()).toContain("revision 2");
    expect(wrapper.findAllComponents(RouterLinkStub).some((link) => link.props("to") === `/app/knowledge/claims/${claim.id}`)).toBe(true);
  });

  it("shows exact value, citation and explicit human decision on a durable route", async () => {
    const wrapper = await mountAt(`/app/knowledge/claims/${claim.id}`);
    expect(api.getKnowledgeClaim).toHaveBeenCalledWith(account.account_id, claim.id);
    expect(wrapper.text()).toContain("August 31"); expect(wrapper.text()).toContain("Launch plan, page 4");
    expect(wrapper.text()).toContain("Agent output is never authoritative"); expect(wrapper.text()).toContain("Accept claim");
  });
});
