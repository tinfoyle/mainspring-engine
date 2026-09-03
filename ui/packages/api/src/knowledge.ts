import type {
  DecideKnowledgeClaimRequest,
  KnowledgeClaim,
  KnowledgeClaimDecisionResult,
  KnowledgeClaimPage,
  KnowledgeClaimSummary,
  KnowledgeEvidence,
  KnowledgeFact,
  KnowledgeFactPage,
  KnowledgeFactSummary,
  ProposeKnowledgeClaimRequest,
  RegisterKnowledgeEvidenceRequest
} from "./generated/api-types";
import { requestJSON } from "./client";

const pendingDecisions = new Map<string, string>();
interface OwnerFactWorkflow { readonly evidenceOperationID: string; readonly claimOperationID: string; readonly decisionOperationID: string; readonly capturedAt: string; evidence?: KnowledgeEvidence; claim?: KnowledgeClaim }
const ownerFactWorkflows = new Map<string, OwnerFactWorkflow>();
interface OwnerEvidenceWorkflow { readonly operationID: string; readonly capturedAt: string }
const ownerEvidenceWorkflows = new Map<string, OwnerEvidenceWorkflow>();

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

function hex(bytes: ArrayBuffer): string { return [...new Uint8Array(bytes)].map((value) => value.toString(16).padStart(2, "0")).join(""); }

export async function captureOwnerKnowledgeEvidence(accountID: string, value: string, sourceReference: string): Promise<KnowledgeEvidence> {
  const canonical = JSON.stringify(value.trim());
  const fingerprint = `${accountID} ${canonical} ${sourceReference}`;
  let workflow = ownerEvidenceWorkflows.get(fingerprint);
  if (!workflow) {
    workflow = { operationID: crypto.randomUUID(), capturedAt: new Date().toISOString() };
    ownerEvidenceWorkflows.set(fingerprint, workflow);
  }
  const input: RegisterKnowledgeEvidenceRequest = {
    source_kind: "owner_statement",
    source_reference: sourceReference,
    source_revision: `statement-${workflow.operationID}`,
    content_sha256: hex(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(canonical))),
    captured_at: workflow.capturedAt
  };
  const evidence = await requestJSON<KnowledgeEvidence>(`${base(accountID)}/evidence`, {
    method: "POST",
    headers: { "Idempotency-Key": workflow.operationID },
    body: JSON.stringify(input)
  });
  ownerEvidenceWorkflows.delete(fingerprint);
  return evidence;
}

export async function captureOwnerKnowledgeFact(accountID: string, key: string, value: string, sourceReference: string): Promise<KnowledgeFact> {
  const canonical = JSON.stringify(value.trim());
  const fingerprint = `${accountID} ${key} ${canonical} ${sourceReference}`;
  let workflow = ownerFactWorkflows.get(fingerprint);
  if (!workflow) {
    workflow = { evidenceOperationID: crypto.randomUUID(), claimOperationID: crypto.randomUUID(), decisionOperationID: crypto.randomUUID(), capturedAt: new Date().toISOString() };
    ownerFactWorkflows.set(fingerprint, workflow);
  }
  if (!workflow.evidence) {
    const input: RegisterKnowledgeEvidenceRequest = { source_kind: "owner_statement", source_reference: sourceReference, source_revision: `statement-${workflow.evidenceOperationID}`, content_sha256: hex(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(canonical))), captured_at: workflow.capturedAt };
    workflow.evidence = await requestJSON<KnowledgeEvidence>(`${base(accountID)}/evidence`, { method: "POST", headers: { "Idempotency-Key": workflow.evidenceOperationID }, body: JSON.stringify(input) });
  }
  if (!workflow.claim) {
    const input: ProposeKnowledgeClaimRequest = { scope: { kind: "account" }, key, value: value.trim(), confidence: 1000, sensitivity: "internal", citations: [{ evidence_id: workflow.evidence.id, evidence_kind: "owner_statement", relation: "supports", locator: "Owner-confirmed Business Baseline answer" }] };
    workflow.claim = await requestJSON<KnowledgeClaim>(`${base(accountID)}/claims`, { method: "POST", headers: { "Idempotency-Key": workflow.claimOperationID }, body: JSON.stringify(input) });
  }
  const result = await requestJSON<KnowledgeClaimDecisionResult>(`${base(accountID)}/claims/${encodeURIComponent(workflow.claim.id)}/decisions`, { method: "POST", headers: { "Idempotency-Key": workflow.decisionOperationID, "If-Match": `W/"${workflow.claim.version}"` }, body: JSON.stringify({ accept: true, reason: "Owner confirmed during Business Baseline onboarding" }) });
  if (!result.fact) throw new Error("The confirmed owner statement did not produce a Knowledge fact.");
  ownerFactWorkflows.delete(fingerprint);
  return result.fact;
}

export type { KnowledgeClaimPage, KnowledgeFactPage };
