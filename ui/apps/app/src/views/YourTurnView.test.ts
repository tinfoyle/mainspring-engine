// @vitest-environment happy-dom
import { flushPromises, mount, RouterLinkStub } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, AttentionQueueItem } from "@spyglass/api";
import YourTurnView from "./YourTurnView.vue";
import { useSessionStore } from "../stores/session";

const { listAttentionQueue } = vi.hoisted(() => ({ listAttentionQueue: vi.fn() }));
vi.mock("@spyglass/api", async (importOriginal) => {
  const original = await importOriginal<typeof import("@spyglass/api")>();
  return {
    ...original,
    getPrivacyConsent: vi.fn().mockResolvedValue({ decided: false, analytics: false, marketing: false, renewal_required: false }),
    emitAnalytics: vi.fn().mockResolvedValue(false),
    listAttentionQueue
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
    { code: "work", version: 1, mode: "enabled", sources: ["subscription"] },
    { code: "agents", version: 1, mode: "enabled", sources: ["subscription"] }
  ] }
} satisfies AccountChoice;

const approval = {
  kind: "approval",
  id: "30000000-0000-4000-8000-000000000003",
  capability: "marketing.release.publish",
  invocation_id: "40000000-0000-4000-8000-000000000004",
  operation_id: "50000000-0000-4000-8000-000000000005",
  policy_version: 2,
  proposer: { kind: "workload", id: "campaign-agent" },
  state: "open",
  version: 4,
  created_at: "2026-08-24T20:00:00Z",
  updated_at: "2026-08-24T20:01:00Z",
  expires_at: "2026-08-25T20:00:00Z"
} as Extract<AttentionQueueItem, { kind: "approval" }>;

beforeEach(() => {
  setActivePinia(createPinia());
  listAttentionQueue.mockReset().mockResolvedValue([approval]);
});

describe("Your Turn queue", () => {
  it("renders the live Account-scoped queue and durable detail route", async () => {
    const session = useSessionStore();
    session.accounts = [account];
    session.selectedID = account.account_id;
    session.userID = "20000000-0000-4000-8000-000000000002";

    const wrapper = mount(YourTurnView, { global: { stubs: { RouterLink: RouterLinkStub } } });
    await flushPromises();

    expect(listAttentionQueue).toHaveBeenCalledWith(account.account_id, session.userID, expect.objectContaining({ approvals: true, approvalsWritable: true }));
    expect(wrapper.text()).toContain("marketing.release.publish");
    expect(wrapper.text()).toContain("Northstar Studio");
    expect(wrapper.getComponent(RouterLinkStub).props("to")).toBe(`/app/your-turn/approval/${approval.id}`);
    expect(wrapper.text()).not.toContain("Approve the August campaign launch");
  });
});
