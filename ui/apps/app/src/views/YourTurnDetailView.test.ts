// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { APIProblem, type AccountChoice, type Approval, type AttentionDetail } from "@spyglass/api";
import YourTurnDetailView from "./YourTurnDetailView.vue";
import { router } from "../router";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";

const {
  answerInformation,
  confirmActionResolution,
  decideApproval,
  decideWorkReview,
  getAttentionDetail,
  listMatchingFacts,
  requestActionResolution
} = vi.hoisted(() => ({
  answerInformation: vi.fn(),
  confirmActionResolution: vi.fn(),
  decideApproval: vi.fn(),
  decideWorkReview: vi.fn(),
  getAttentionDetail: vi.fn(),
  listMatchingFacts: vi.fn(),
  requestActionResolution: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => {
  const original = await importOriginal<typeof import("@spyglass/api")>();
  return {
    ...original,
    answerInformation,
    confirmActionResolution,
    decideApproval,
    decideWorkReview,
    getAttentionDetail,
    listMatchingFacts,
    requestActionResolution,
    getPrivacyConsent: vi.fn().mockResolvedValue({ decided: false, analytics: false, marketing: false, renewal_required: false }),
    emitAnalytics: vi.fn().mockResolvedValue(false)
  };
});

const account = {
  account_id: "10000000-0000-4000-8000-000000000001",
  account_type: "paid", account_state: "active",
  account_version: 1,
  cell_id: "cell-us-east-01",
  display_name: "Northstar Studio",
  placement_generation: 1,
  role: "owner",
  slug: "northstar-studio",
  owner_enrollment_required: false,
  entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-24T20:00:00Z", version: 3, packages: [
    { code: "work", version: 1, mode: "enabled", sources: ["subscription"] },
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

const information = {
  kind: "information",
  id: "60000000-0000-4000-8000-000000000006",
  parent_work_item_id: "70000000-0000-4000-8000-000000000007",
  question: "Which approved refund policy applies?",
  requirement: { key: "billing.refund_window", scope: "account" },
  requested_by: { kind: "workload", id: "finance-agent" },
  state: "open",
  version: 2,
  created_at: "2026-08-24T20:00:00Z",
  updated_at: "2026-08-24T20:01:00Z"
} as Extract<AttentionDetail, { kind: "information" }>;

const review = {
  kind: "review",
  id: "80000000-0000-4000-8000-000000000008",
  work_item_id: "70000000-0000-4000-8000-000000000007",
  work_version: 5,
  proposal_sha256: "c".repeat(64),
  question: "Is this onboarding sequence ready?",
  requested_by: { kind: "workload", id: "marketing-agent" },
  reviewer_id: "20000000-0000-4000-8000-000000000002",
  state: "open",
  version: 3,
  created_at: "2026-08-24T20:00:00Z",
  updated_at: "2026-08-24T20:01:00Z"
} as Extract<AttentionDetail, { kind: "review" }>;

const recovery = {
  kind: "action",
  approval_id: approval.id,
  attempt_count: 2,
  capability: "marketing.release.publish",
  executor_id: "web-publisher",
  executor_version: 1,
  invocation_id: approval.invocation_id,
  operation_id: "90000000-0000-4000-8000-000000000009",
  policy_version: 2,
  started_at: "2026-08-24T20:00:00Z",
  state: "unknown",
  updated_at: "2026-08-24T20:01:00Z"
} as Extract<AttentionDetail, { kind: "action" }>;

beforeEach(async () => {
  sessionStorage.clear();
  setActivePinia(createPinia());
  getAttentionDetail.mockReset().mockResolvedValue(approval);
  decideApproval.mockReset().mockResolvedValue({ ...approval, state: "approved", version: 5 } as Approval);
  answerInformation.mockReset().mockResolvedValue({ answered: [], resumable_parent_ids: [] });
  decideWorkReview.mockReset().mockResolvedValue({ ...review, state: "changes_requested", version: 4 });
  requestActionResolution.mockReset().mockResolvedValue({ ...recovery, state: "manual_resolution" });
  confirmActionResolution.mockReset().mockResolvedValue({ ...recovery, state: "succeeded" });
  listMatchingFacts.mockReset().mockResolvedValue([{ id: "a0000000-0000-4000-8000-00000000000a", key: "billing.refund_window", revision: 6, scope: { kind: "account" }, state: "active", sensitivity: "internal" }]);
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
    await expectNoAxeViolations(wrapper.element);

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
    wrapper.unmount();
  });

  it("allows only one approval mutation while a decision is pending", async () => {
    let resolveDecision!: (value: Approval) => void;
    decideApproval.mockImplementationOnce(() => new Promise<Approval>((resolve) => { resolveDecision = resolve; }));
    const session = useSessionStore();
    session.accounts = [account];
    session.selectedID = account.account_id;
    session.userID = "20000000-0000-4000-8000-000000000002";
    const wrapper = mount(YourTurnDetailView, { global: { plugins: [router] } });
    await flushPromises();

    await wrapper.get('input[value="approve"]').setValue(true);
    await wrapper.get("textarea").setValue("The governed release is ready.");
    await wrapper.get('input[type="checkbox"]').setValue(true);
    const firstSubmit = wrapper.get("form").trigger("submit");
    const duplicateSubmit = wrapper.get("form").trigger("submit");
    await Promise.all([firstSubmit, duplicateSubmit]);

    expect(decideApproval).toHaveBeenCalledTimes(1);
    expect(wrapper.get('button[type="submit"]').attributes()).toHaveProperty("disabled");
    expect(wrapper.get('button[type="submit"]').text()).toBe("Saving…");

    resolveDecision({ ...approval, state: "approved", version: 5 } as Approval);
    await flushPromises();
    expect(decideApproval).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it("answers with an exact current Knowledge fact", async () => {
    getAttentionDetail.mockResolvedValue(information);
    await router.push(`/app/your-turn/information/${information.id}`);
    const session = useSessionStore();
    session.accounts = [account];
    session.selectedID = account.account_id;
    session.userID = "20000000-0000-4000-8000-000000000002";
    const wrapper = mount(YourTurnDetailView, { global: { plugins: [router] } });
    await flushPromises();

    await wrapper.get("select").setValue("a0000000-0000-4000-8000-00000000000a");
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(answerInformation).toHaveBeenCalledWith(account.account_id, expect.objectContaining({ version: 2 }), {
      fact_id: "a0000000-0000-4000-8000-00000000000a",
      fact_version: 6,
      requirement: information.requirement
    });
    wrapper.unmount();
  });

  it("records a version-bound Work change request", async () => {
    getAttentionDetail.mockResolvedValue(review);
    await router.push(`/app/your-turn/review/${review.id}`);
    const session = useSessionStore();
    session.accounts = [account];
    session.selectedID = account.account_id;
    session.userID = review.reviewer_id;
    const wrapper = mount(YourTurnDetailView, { global: { plugins: [router] } });
    await flushPromises();

    await wrapper.get('input[value="request_changes"]').setValue(true);
    await wrapper.get("textarea").setValue("Clarify the first customer message.");
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(decideWorkReview).toHaveBeenCalledWith(account.account_id, expect.objectContaining({ version: 3 }), {
      decision: "request_changes", reason: "Clarify the first customer message."
    });
    wrapper.unmount();
  });

  it("requests an evidence-based recovery outcome", async () => {
    getAttentionDetail.mockResolvedValue(recovery);
    await router.push(`/app/your-turn/action/${recovery.operation_id}`);
    const session = useSessionStore();
    session.accounts = [account];
    session.selectedID = account.account_id;
    session.userID = "20000000-0000-4000-8000-000000000002";
    const wrapper = mount(YourTurnDetailView, { global: { plugins: [router] } });
    await flushPromises();

    await wrapper.get('input[value="succeeded"]').setValue(true);
    await wrapper.get("textarea").setValue("The provider shows the release as published.");
    await wrapper.get('input[type="checkbox"]').setValue(true);
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(requestActionResolution).toHaveBeenCalledWith(account.account_id, expect.objectContaining({ operation_id: recovery.operation_id }), {
      outcome: "succeeded", reason: "The provider shows the release as published."
    });
    wrapper.unmount();
  });

  it("allows only a different eligible operator to confirm recovery", async () => {
    const pending = { ...recovery, state: "manual_resolution" as const, resolution: {
      id: "b0000000-0000-4000-8000-00000000000b",
      operation_id: recovery.operation_id,
      reason_sha256: "d".repeat(64),
      requested_at: "2026-08-24T20:02:00Z",
      requested_by_user_id: "c0000000-0000-4000-8000-00000000000c",
      requested_outcome: "succeeded" as const,
      state: "pending" as const
    } };
    getAttentionDetail.mockResolvedValue(pending);
    await router.push(`/app/your-turn/action/${recovery.operation_id}`);
    const session = useSessionStore();
    session.accounts = [account];
    session.selectedID = account.account_id;
    session.userID = "20000000-0000-4000-8000-000000000002";
    const wrapper = mount(YourTurnDetailView, { global: { plugins: [router] } });
    await flushPromises();

    await wrapper.get('input[type="checkbox"]').setValue(true);
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(confirmActionResolution).toHaveBeenCalledWith(account.account_id, expect.objectContaining({ resolution: expect.objectContaining({ state: "pending" }) }));
    wrapper.unmount();
  });

  it("reloads a conflicted decision while preserving only its tab-scoped draft", async () => {
    getAttentionDetail.mockReset()
      .mockResolvedValueOnce(approval)
      .mockResolvedValueOnce({ ...approval, version: 5, updated_at: "2026-08-24T20:03:00Z" });
    decideApproval.mockRejectedValueOnce(new APIProblem(412, {
      code: "attention_version_conflict",
      detail: "The approval changed before this decision was recorded.",
      status: 412,
      title: "Precondition Failed",
      type: "https://infiniteocean.net/problems/attention_version_conflict"
    }));
    const session = useSessionStore();
    session.accounts = [account];
    session.selectedID = account.account_id;
    session.userID = "20000000-0000-4000-8000-000000000002";
    const wrapper = mount(YourTurnDetailView, { global: { plugins: [router] } });
    await flushPromises();

    await wrapper.get('input[value="approve"]').setValue(true);
    await wrapper.get("textarea").setValue("The governed release is ready.");
    await wrapper.get('input[type="checkbox"]').setValue(true);
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(getAttentionDetail).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain("This item changed. The latest version is loading; your draft is preserved.");
    expect((wrapper.get('input[value="approve"]').element as HTMLInputElement).checked).toBe(true);
    expect((wrapper.get("textarea").element as HTMLTextAreaElement).value).toBe("The governed release is ready.");
    expect((wrapper.get('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(false);
    expect(wrapper.get('button[type="submit"]').attributes()).toHaveProperty("disabled");
    expect(sessionStorage.length).toBe(1);
    wrapper.unmount();
  });
});
