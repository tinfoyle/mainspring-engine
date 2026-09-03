import type {
  ApproveMarketingReleaseRequest,
  CreateMarketingCampaignRequest,
  CreateMarketingReleaseRequest,
  MarketingAssetKind,
  MarketingAssetRevision,
  MarketingAssetRevisionPage,
  MarketingCampaign,
  MarketingCampaignPage,
  MarketingCampaignState,
  MarketingRelease,
  MarketingReleasePage,
  SubmitMarketingReleaseRequest
} from "./generated/api-types";
import { requestJSON } from "./client";

const pendingOperations = new Map<string, string>();
const root = (accountID: string): string => `/api/v1/accounts/${encodeURIComponent(accountID)}/marketing`;
const campaign = (accountID: string, campaignID: string): string => `${root(accountID)}/campaigns/${encodeURIComponent(campaignID)}`;
const release = (accountID: string, releaseID: string): string => `${root(accountID)}/releases/${encodeURIComponent(releaseID)}`;

async function command<T>(method: "POST" | "PUT" | "DELETE", path: string, input?: unknown, version?: number): Promise<T> {
  const body = input === undefined ? "" : JSON.stringify(input);
  const fingerprint = `${method} ${path} ${version ?? "unversioned"} ${body}`;
  let operationID = pendingOperations.get(fingerprint);
  if (!operationID) { operationID = crypto.randomUUID(); pendingOperations.set(fingerprint, operationID); }
  const headers = new Headers({ "Idempotency-Key": operationID });
  if (version !== undefined) headers.set("If-Match", `W/"${version}"`);
  const result = await requestJSON<T>(path, { method, headers, ...(body ? { body } : {}) });
  pendingOperations.delete(fingerprint);
  return result;
}

function page(path: string, values: Record<string, string | number | undefined>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) if (value !== undefined && value !== "") query.set(key, String(value));
  return query.size ? `${path}?${query}` : path;
}

export interface MarketingCampaignFilters {
  state?: MarketingCampaignState | undefined;
  cursor?: string | undefined;
  limit?: number | undefined;
}

export interface MarketingAssetRevisionFilters {
  assetID?: string | undefined;
  cursor?: string | undefined;
  limit?: number | undefined;
}

export interface MarketingAssetUpload {
  assetID: string;
  kind: MarketingAssetKind;
  title: string;
  mediaType: string;
  alternativeText?: string | undefined;
  file: File;
}

export async function listMarketingCampaigns(accountID: string, filters: MarketingCampaignFilters = {}): Promise<MarketingCampaignPage> {
  const result = await requestJSON<MarketingCampaignPage>(page(`${root(accountID)}/campaigns`, { state: filters.state, cursor: filters.cursor, limit: filters.limit ?? 100 }));
  return { ...result, items: result.items ?? [] };
}
export function getMarketingCampaign(accountID: string, campaignID: string): Promise<MarketingCampaign> { return requestJSON(campaign(accountID, campaignID)); }
export function createMarketingCampaign(accountID: string, input: CreateMarketingCampaignRequest): Promise<MarketingCampaign> { return command("POST", `${root(accountID)}/campaigns`, input); }
export function reviseMarketingCampaign(accountID: string, value: MarketingCampaign, input: CreateMarketingCampaignRequest): Promise<MarketingCampaign> { return command("PUT", campaign(accountID, value.id), input, value.version); }
export function archiveMarketingCampaign(accountID: string, value: MarketingCampaign): Promise<MarketingCampaign> { return command("DELETE", campaign(accountID, value.id), undefined, value.version); }

export async function listMarketingAssetRevisions(accountID: string, campaignID: string, filters: MarketingAssetRevisionFilters = {}): Promise<MarketingAssetRevisionPage> {
  const result = await requestJSON<MarketingAssetRevisionPage>(page(`${campaign(accountID, campaignID)}/asset-revisions`, { asset_id: filters.assetID, cursor: filters.cursor, limit: filters.limit ?? 100 }));
  return { ...result, items: result.items ?? [] };
}
export async function uploadMarketingAssetRevision(accountID: string, campaignID: string, input: MarketingAssetUpload): Promise<MarketingAssetRevision> {
  const path = `${campaign(accountID, campaignID)}/asset-revisions`;
  const fingerprint = `POST ${path} ${input.assetID} ${input.kind} ${input.title} ${input.mediaType} ${input.alternativeText ?? ""} ${input.file.name} ${input.file.size} ${input.file.type} ${input.file.lastModified}`;
  let operationID = pendingOperations.get(fingerprint);
  if (!operationID) { operationID = crypto.randomUUID(); pendingOperations.set(fingerprint, operationID); }
  const body = new FormData(); body.set("asset_id", input.assetID); body.set("kind", input.kind); body.set("title", input.title); body.set("media_type", input.mediaType);
  if (input.alternativeText) body.set("alternative_text", input.alternativeText); body.set("file", input.file);
  const result = await requestJSON<MarketingAssetRevision>(path, { method: "POST", headers: { "Idempotency-Key": operationID }, body });
  pendingOperations.delete(fingerprint);
  return result;
}

export async function listMarketingReleases(accountID: string, campaignID: string, cursor?: string): Promise<MarketingReleasePage> {
  const result = await requestJSON<MarketingReleasePage>(page(`${campaign(accountID, campaignID)}/releases`, { cursor, limit: 100 }));
  return { ...result, items: result.items ?? [] };
}
export function getMarketingRelease(accountID: string, releaseID: string): Promise<MarketingRelease> { return requestJSON(release(accountID, releaseID)); }
export function createMarketingRelease(accountID: string, campaignID: string, input: CreateMarketingReleaseRequest): Promise<MarketingRelease> { return command("POST", `${campaign(accountID, campaignID)}/releases`, input); }
export function submitMarketingRelease(accountID: string, value: MarketingRelease, input: SubmitMarketingReleaseRequest): Promise<MarketingRelease> { return command("POST", `${release(accountID, value.id)}/submissions`, input, value.version); }
export function approveMarketingRelease(accountID: string, value: MarketingRelease, input: ApproveMarketingReleaseRequest): Promise<MarketingRelease> { return command("POST", `${release(accountID, value.id)}/approvals`, input, value.version); }
export function cancelMarketingRelease(accountID: string, value: MarketingRelease): Promise<MarketingRelease> { return command("POST", `${release(accountID, value.id)}/cancellations`, undefined, value.version); }
export function activateMarketingCampaign(accountID: string, value: MarketingCampaign, releaseID: string): Promise<MarketingCampaign> { return command("POST", `${campaign(accountID, value.id)}/activations`, { release_id: releaseID }, value.version); }
export function pauseMarketingCampaign(accountID: string, value: MarketingCampaign): Promise<MarketingCampaign> { return command("POST", `${campaign(accountID, value.id)}/pauses`, undefined, value.version); }
export function completeMarketingCampaign(accountID: string, value: MarketingCampaign): Promise<MarketingCampaign> { return command("POST", `${campaign(accountID, value.id)}/completions`, undefined, value.version); }
