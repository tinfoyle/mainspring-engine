// @vitest-environment happy-dom
import { flushPromises, mount, RouterLinkStub } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, MarketingAssetRevision, MarketingCampaign, MarketingRelease } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";
import MarketingView from "./MarketingView.vue";

const api = vi.hoisted(() => ({
  listMarketingCampaigns: vi.fn(), getMarketingCampaign: vi.fn(), createMarketingCampaign: vi.fn(), reviseMarketingCampaign: vi.fn(), archiveMarketingCampaign: vi.fn(),
  listMarketingAssetRevisions: vi.fn(), uploadMarketingAssetRevision: vi.fn(), listMarketingReleases: vi.fn(), getMarketingRelease: vi.fn(), createMarketingRelease: vi.fn(),
  submitMarketingRelease: vi.fn(), cancelMarketingRelease: vi.fn(), activateMarketingCampaign: vi.fn(), pauseMarketingCampaign: vi.fn(), completeMarketingCampaign: vi.fn()
}));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));

const accountID = "10000000-0000-4000-8000-000000000001"; const userID = "20000000-0000-4000-8000-000000000002";
const campaign = { id: "30000000-0000-4000-8000-000000000003", account_id: accountID, name: "Launch", objective: "Explain the operating model", audience: "Business owners", channels: ["email", "web"], state: "draft", version: 4, created_by: { kind: "user", id: userID }, provenance: { origin: "human" }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T01:00:00Z" } satisfies MarketingCampaign;
const asset = { id: "40000000-0000-4000-8000-000000000004", account_id: accountID, campaign_id: campaign.id, asset_id: "50000000-0000-4000-8000-000000000005", revision: 2, kind: "copy", title: "Launch copy", media_type: "text/plain", content_reference: "opaque", content_sha256: "a".repeat(64), content_bytes: 120, created_by: { kind: "user", id: userID }, provenance: { origin: "human" }, created_at: "2026-08-24T00:30:00Z" } satisfies MarketingAssetRevision;
const release = { id: "60000000-0000-4000-8000-000000000006", account_id: accountID, campaign_id: campaign.id, campaign_version: 4, name: "Launch release", channels: ["email", "web"], asset_revision_ids: [asset.id], state: "submitted", version: 2, created_by: { kind: "user", id: userID }, provenance: { origin: "human" }, submitted_by: { kind: "user", id: userID }, created_at: "2026-08-24T00:40:00Z", updated_at: "2026-08-24T00:50:00Z" } satisfies MarketingRelease;
const account = { account_id: accountID, account_type: "paid", account_state: "active", account_version: 2, cell_id: "cell-a", display_name: "Northstar", placement_generation: 1, role: "owner", slug: "northstar", owner_enrollment_required: false, entitlements: { account_id: accountID, catalog_version: 1, evaluated_at: "2026-08-24T00:00:00Z", version: 1, packages: [{ code: "marketing", mode: "enabled", sources: ["subscription"], version: 1 }] } } satisfies AccountChoice;

beforeEach(() => {
  setActivePinia(createPinia()); for (const mock of Object.values(api)) mock.mockReset();
  api.listMarketingCampaigns.mockResolvedValue({ items: [campaign] }); api.getMarketingCampaign.mockResolvedValue(campaign); api.listMarketingAssetRevisions.mockResolvedValue({ items: [asset] }); api.listMarketingReleases.mockResolvedValue({ items: [release] }); api.getMarketingRelease.mockResolvedValue(release);
  const session = useSessionStore(); session.accounts = [account]; session.selectedID = accountID; session.userID = userID;
});
async function mountAt(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/marketing", component: MarketingView }, { path: "/app/marketing/campaigns/:campaignID", component: MarketingView }, { path: "/app/marketing/releases/:releaseID", component: MarketingView }, { path: "/app/your-turn", component: { template: "<div />" } }, { path: "/app/integrations", component: { template: "<div />" } }] });
  await router.push(path); await router.isReady(); const wrapper = mount(MarketingView, { global: { plugins: [router], stubs: { RouterLink: RouterLinkStub } } }); await flushPromises(); await expectNoAxeViolations(wrapper.element); return wrapper;
}

describe("Marketing workspace", () => {
  it("loads a durable submitted release without exposing an approval-identity shortcut", async () => {
    const wrapper = await mountAt(`/app/marketing/releases/${release.id}`);
    expect(api.getMarketingRelease).toHaveBeenCalledWith(accountID, release.id); expect(wrapper.text()).toContain("Launch release"); expect(wrapper.text()).toContain("Request approval"); expect(wrapper.text()).toContain("Open Your Turn"); expect(wrapper.find('input[name="approval_id"]').exists()).toBe(false);
    expect(wrapper.get('a[download]').attributes("href")).toBe(`/api/v1/accounts/${accountID}/marketing/campaigns/${campaign.id}/asset-revisions/${asset.id}/content`);
  });

  it("keeps inspection but removes every mutation in package read-only mode", async () => {
    const session = useSessionStore(); session.accounts = [{ ...account, entitlements: { ...account.entitlements, packages: [{ ...account.entitlements.packages[0]!, mode: "read_only" }] } }];
    const wrapper = await mountAt(`/app/marketing/campaigns/${campaign.id}`);
    expect(wrapper.text()).toContain("Read-only access"); expect(wrapper.text()).toContain("Explain the operating model"); expect(wrapper.text()).not.toContain("New campaign"); expect(wrapper.text()).not.toContain("Edit campaign");
  });

  it("lets a participant freeze the exact campaign and creative revisions", async () => {
    api.createMarketingRelease.mockResolvedValue({ ...release, state: "draft", version: 1 }); const wrapper = await mountAt(`/app/marketing/campaigns/${campaign.id}`);
    await wrapper.findAll("button").find((item) => item.text() === "Releases")?.trigger("click"); await wrapper.findAll("button").find((item) => item.text() === "Create release")?.trigger("click"); await wrapper.get('[role="dialog"] input').setValue("Launch snapshot"); await wrapper.get('[role="dialog"]').trigger("submit"); await flushPromises();
    expect(api.createMarketingRelease).toHaveBeenCalledWith(accountID, campaign.id, { campaign_version: 4, name: "Launch snapshot", channels: ["email", "web"], asset_revision_ids: [asset.id] });
  });

  it("keeps governance controls manager-only while members can draft", async () => {
    const session = useSessionStore(); session.accounts = [{ ...account, role: "member" }]; const wrapper = await mountAt(`/app/marketing/campaigns/${campaign.id}`);
    expect(wrapper.text()).toContain("New campaign"); expect(wrapper.text()).toContain("Edit campaign"); expect(wrapper.findAll("button").some((item) => item.text() === "Archive")).toBe(false);
  });
});
