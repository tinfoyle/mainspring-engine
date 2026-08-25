import type {
  DecideKnowledgeClaimRequest,
  KnowledgeClaim,
  KnowledgeClaimDecisionResult,
  KnowledgeClaimPage,
  KnowledgeClaimSummary,
  KnowledgeFactPage,
  KnowledgeFactSummary
} from "./generated/api-types";
import { requestJSON } from "./client";

const pendingDecisions = new Map<string, string>();

function base(accountID: string): string {
  return `/api/v1/accounts/${encodeURIComponent(accountID)}/knowledge`;
}

async function allPages<T>(path: string): Promise<ReadonlyArray<T>> {
  const values: T[] = [];
  let next: string | undefined = path;
  for (let pageNumber = 0; next && pageNumber < 20; pageNumber += 1) {
    const page = await requestJSON<{ readonly items: ReadonlyArray<T>; readonly next_cursor?: string }>(next);
    values.push(...page.items);
    if (!page.next_cursor) return values;
    const cursorURL: URL = new URL(next, "https://spyglass.invalid");
    cursorURL.searchParams.set("cursor", page.next_cursor);
    next = `${cursorURL.pathname}${cursorURL.search}`;
  }
  throw new Error("Knowledge returned too many pages to load safely.");
}

export function listProposedKnowledgeClaims(accountID: string): Promise<ReadonlyArray<KnowledgeClaimSummary>> {
  return allPages<KnowledgeClaimSummary>(`${base(accountID)}/claims?state=proposed&limit=100`);
}

export function listKnowledgeFacts(accountID: string): Promise<ReadonlyArray<KnowledgeFactSummary>> {
  return allPages<KnowledgeFactSummary>(`${base(accountID)}/facts?limit=100`);
}

export function getKnowledgeClaim(accountID: string, claimID: string): Promise<KnowledgeClaim> {
  return requestJSON<KnowledgeClaim>(`${base(accountID)}/claims/${encodeURIComponent(claimID)}`);
}

export async function decideKnowledgeClaim(accountID: string, claim: KnowledgeClaim, input: DecideKnowledgeClaimRequest): Promise<KnowledgeClaimDecisionResult> {
  const path = `${base(accountID)}/claims/${encodeURIComponent(claim.id)}/decisions`;
  const body = JSON.stringify(input);
  const fingerprint = `${path} ${claim.version} ${body}`;
  let operationID = pendingDecisions.get(fingerprint);
  if (!operationID) {
    operationID = crypto.randomUUID();
    pendingDecisions.set(fingerprint, operationID);
  }
  const result = await requestJSON<KnowledgeClaimDecisionResult>(path, {
    method: "POST", headers: { "Idempotency-Key": operationID, "If-Match": `W/"${claim.version}"` }, body
  });
  pendingDecisions.delete(fingerprint);
  return result;
}

export type { KnowledgeClaimPage, KnowledgeFactPage };
