// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, Approval, AttentionDetail } from "@spyglass/api";
import YourTurnDetailView from "./YourTurnDetailView.vue";
import { router } from "../router";
import { useSessionStore } from "../stores/session";

const { decideApproval, getAttentionDetail } = vi.hoisted(() => ({ decideApproval: vi.fn(), getAttentionDetail: vi.fn() }));
vi.mock("@spyglass/api", async (importOriginal) => {
  const original = await importOriginal<typeof import("@spyglass/api")>();
  return {
    ...original,
    decideApproval,
    getAttentionDetail,
    getPrivacyConsent: vi.fn().mockResolvedValue({ decided: false, analytics: false, marketing: false, renewal_required: false }),
    emitAnalytics: vi.fn().mockResolvedValue(false)
  };
});

const account = {
  account_id: "10000000-0000-4000-8000-000000000001",
  account_type: "paid",
  account_version: 1,
  cell_id: "cell-us-east-01",
  display_name: "Northstar Studio",
  placement_generation: 1,
  role: "owner",
  slug: "northstar-studio",
  owner_enrollment_required: false,
  entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-24T20:00:00Z", version: 3, packages: [
    { code: "agents", version: 1, mode: "enabled", sources: ["subscription"] }
  ] }
} satisfies AccountChoice;

const approval = {
  kind: "approval",
  id: "30000000-0000-4000-8000-000000000003",
  capability: "marketing.release.publish",
  payload: { release_id: "redacted-release-reference" },
  evidence_sha256: "a".repeat(64),
  input_sha256: "b".repeat(64),
  hash_version: 1,
  invocation_id: "40000000-0000-4000-8000-000000000004",
  operation_id: "50000000-0000-4000-8000-000000000005",
  policy_version: 2,
  proposer: { kind: "workload", id: "campaign-agent" },
  require_independent_review: false,
  state: "open",
  version: 4,
  created_at: "2026-08-24T20:00:00Z",
  updated_at: "2026-08-24T20:01:00Z",
  expires_at: "2026-08-25T20:00:00Z"
} as Extract<AttentionDetail, { kind: "approval" }>;

beforeEach(async () => {
  setActivePinia(createPinia());
  getAttentionDetail.mockReset().mockResolvedValue(approval);
  decideApproval.mockReset().mockResolvedValue({ ...approval, state: "approved", version: 5 } as Approval);
  await router.push(`/app/your-turn/approval/${approval.id}`);
  await router.isReady();
});

describe("Your Turn approval detail", () => {
  it("requires explicit exact-payload confirmation before submitting", async () => {
    const session = useSessionStore();
    session.accounts = [account];
    session.selectedID = account.account_id;
    session.userID = "20000000-0000-4000-8000-000000000002";
    const wrapper = mount(YourTurnDetailView, { global: { plugins: [router] } });
    await flushPromises();

    expect(wrapper.text()).toContain("Exact proposed payload");
    await wrapper.get('input[value="approve"]').setValue(true);
    await wrapper.get("textarea").setValue("The governed release is ready.");
    expect(wrapper.get('button[type="submit"]').attributes()).toHaveProperty("disabled");
    await wrapper.get('input[type="checkbox"]').setValue(true);
    expect(wrapper.get('button[type="submit"]').attributes()).not.toHaveProperty("disabled");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(decideApproval).toHaveBeenCalledWith(account.account_id, expect.objectContaining({ version: 4 }), {
      decision: "approve", reason: "The governed release is ready."
    });
  });
});
