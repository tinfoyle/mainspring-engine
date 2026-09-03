import { afterEach, describe, expect, it, vi } from "vitest";
import type { MarketingCampaign, MarketingRelease } from "./generated/api-types";
import {
  activateMarketingCampaign, archiveMarketingCampaign, createMarketingCampaign, createMarketingRelease,
  listMarketingAssetRevisions, listMarketingCampaigns, listMarketingReleases, reviseMarketingCampaign,
  submitMarketingRelease, uploadMarketingAssetRevision
} from "./marketing";

afterEach(() => vi.unstubAllGlobals());
const campaign = { id: "campaign/id", account_id: "account", name: "Launch", objective: "Explain the product", audience: "Operators", channels: ["email", "web"], state: "draft", version: 4, created_by: { kind: "user", id: "user" }, provenance: { origin: "human" }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" } satisfies MarketingCampaign;
const release = { id: "release/id", account_id: "account", campaign_id: campaign.id, campaign_version: 4, name: "Launch v1", channels: ["email", "web"], asset_revision_ids: ["asset-revision"], state: "draft", version: 2, created_by: { kind: "user", id: "user" }, provenance: { origin: "human" }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" } satisfies MarketingRelease;

describe("Marketing client", () => {
  it("preserves opaque pages and binds every versioned lifecycle command", async () => {
    const fetcher = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(campaign), { status: 200, headers: { "content-type": "application/json" } }))); vi.stubGlobal("fetch", fetcher);
    await listMarketingCampaigns("account", { state: "paused", cursor: "opaque+/=" });
    await reviseMarketingCampaign("account", campaign, { name: campaign.name, objective: campaign.objective, audience: campaign.audience, channels: campaign.channels });
    await archiveMarketingCampaign("account", campaign); await activateMarketingCampaign("account", campaign, "release/id");
    await listMarketingAssetRevisions("account", "campaign/id", { assetID: "asset/id", cursor: "asset+/=" }); await listMarketingReleases("account", "campaign/id", "release+/=");
    expect(String(fetcher.mock.calls[0]?.[0])).toContain("cursor=opaque%2B%2F%3D");
    for (const call of fetcher.mock.calls.slice(1, 4)) { expect(new Headers(call[1]?.headers).get("If-Match")).toBe('W/"4"'); expect(new Headers(call[1]?.headers).get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/); }
    expect(String(fetcher.mock.calls[4]?.[0])).toContain("asset_id=asset%2Fid"); expect(String(fetcher.mock.calls[5]?.[0])).toContain("cursor=release%2B%2F%3D");
  });

  it("normalizes legacy null collection pages to empty arrays", async () => {
    const fetcher = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ items: null }), { status: 200, headers: { "content-type": "application/json" } }))); vi.stubGlobal("fetch", fetcher);
    await expect(listMarketingCampaigns("account")).resolves.toMatchObject({ items: [] });
    await expect(listMarketingAssetRevisions("account", "campaign")).resolves.toMatchObject({ items: [] });
    await expect(listMarketingReleases("account", "campaign")).resolves.toMatchObject({ items: [] });
  });

  it("uploads a real multipart file without setting storage metadata", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: "revision" }), { status: 201, headers: { "content-type": "application/json" } })); vi.stubGlobal("fetch", fetcher);
    const file = new File(["Launch copy"], "launch.txt", { type: "text/plain", lastModified: 1 });
    await uploadMarketingAssetRevision("account", "campaign", { assetID: "asset", kind: "copy", title: "Launch copy", mediaType: "text/plain", file });
    const request = fetcher.mock.calls[0]?.[1]; const body = request?.body as FormData;
    expect(body.get("file")).toBe(file); expect(body.get("asset_id")).toBe("asset"); expect(body.has("content_reference")).toBe(false); expect(body.has("content_sha256")).toBe(false);
    expect(new Headers(request?.headers).get("content-type")).toBeNull(); expect(new Headers(request?.headers).get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("keeps one operation identity for an exact failed retry", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ title: "Unavailable" }), { status: 503, headers: { "content-type": "application/problem+json" } })); vi.stubGlobal("fetch", fetcher);
    const input = { name: "Launch", objective: "Explain", audience: "Operators", channels: ["web"] } as const;
    await expect(createMarketingCampaign("account", input)).rejects.toThrow(); await expect(createMarketingCampaign("account", input)).rejects.toThrow();
    expect(new Headers(fetcher.mock.calls[0]?.[1]?.headers).get("Idempotency-Key")).toBe(new Headers(fetcher.mock.calls[1]?.[1]?.headers).get("Idempotency-Key"));
  });

  it("freezes and submits the exact campaign version", async () => {
    const fetcher = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(release), { status: 200, headers: { "content-type": "application/json" } }))); vi.stubGlobal("fetch", fetcher);
    await createMarketingRelease("account", campaign.id, { campaign_version: campaign.version, name: release.name, channels: campaign.channels, asset_revision_ids: release.asset_revision_ids });
    await submitMarketingRelease("account", release, { campaign_version: campaign.version });
    expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body))).toMatchObject({ campaign_version: 4, asset_revision_ids: ["asset-revision"] });
    expect(new Headers(fetcher.mock.calls[1]?.[1]?.headers).get("If-Match")).toBe('W/"2"');
  });
});
