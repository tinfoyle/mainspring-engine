import type {
  ActionRecoveryDetail,
  ActionRecoverySummary,
  AnswerInformationRequestInput,
  Approval,
  ApprovalSummary,
  DecideApprovalRequest,
  DecideWorkReviewRequest,
  InformationCompletion,
  InformationRequest,
  InformationRequestSummary,
  InformationRequirement,
  KnowledgeFactPage,
  KnowledgeFactSummary,
  RequestActionResolutionRequest,
  WorkReview,
  WorkReviewSummary
} from "./generated/api-types";
import { requestJSON } from "./client";

export type AttentionKind = "information" | "review" | "approval" | "action";

export type AttentionQueueItem =
  | (InformationRequestSummary & { readonly kind: "information" })
  | (WorkReviewSummary & { readonly kind: "review" })
  | (ApprovalSummary & { readonly kind: "approval" })
  | (ActionRecoverySummary & { readonly kind: "action" });

export type AttentionDetail =
  | (InformationRequest & { readonly kind: "information" })
  | (WorkReview & { readonly kind: "review" })
  | (Approval & { readonly kind: "approval" })
  | (ActionRecoveryDetail & { readonly kind: "action" });

export interface AttentionAccess {
  readonly work: boolean;
  readonly workWritable: boolean;
  readonly approvals: boolean;
  readonly approvalsWritable: boolean;
}

const pendingOperations = new Map<string, string>();

interface AttentionPage<T> {
  readonly items: ReadonlyArray<T>;
  readonly next_cursor?: string;
}

function attentionBase(accountID: string): string {
  return `/api/v1/accounts/${encodeURIComponent(accountID)}/attention`;
}

function collection(kind: AttentionKind): string {
  if (kind === "information") return "information-requests";
  if (kind === "review") return "work-reviews";
  if (kind === "action") return "actions";
  return "approvals";
}

function itemID(item: AttentionQueueItem | AttentionDetail): string {
  return item.kind === "action" ? item.operation_id : item.id;
}

function commandKey(path: string, body: string, version?: number): { fingerprint: string; operationID: string } {
  const fingerprint = `${path} ${version ?? "unversioned"} ${body}`;
  let operationID = pendingOperations.get(fingerprint);
  if (!operationID) {
    operationID = crypto.randomUUID();
    pendingOperations.set(fingerprint, operationID);
  }
  return { fingerprint, operationID };
}

async function command<T>(path: string, payload: unknown, version?: number): Promise<T> {
  const body = payload === undefined ? undefined : JSON.stringify(payload);
  const pending = commandKey(path, body ?? "no-body", version);
  const headers = new Headers({ "Idempotency-Key": pending.operationID });
  if (version !== undefined) headers.set("If-Match", `W/"${version}"`);
  const init: RequestInit = { method: "POST", headers };
  if (body !== undefined) init.body = body;
  const result = await requestJSON<T>(path, init);
  pendingOperations.delete(pending.fingerprint);
  return result;
}

async function readAllPages<T>(path: string): Promise<ReadonlyArray<T>> {
  const items: T[] = [];
  let nextPath: string | undefined = path;
  for (let pageNumber = 0; nextPath && pageNumber < 20; pageNumber += 1) {
    const page: AttentionPage<T> = await requestJSON<AttentionPage<T>>(nextPath);
    items.push(...page.items);
    if (!page.next_cursor) return items;
    const cursorURL: URL = new URL(nextPath, "https://spyglass.invalid");
    cursorURL.searchParams.set("cursor", page.next_cursor);
    nextPath = `${cursorURL.pathname}${cursorURL.search}`;
  }
  throw new Error("Your Turn returned too many pages to load safely.");
}

export async function listAttentionQueue(accountID: string, userID: string, access: AttentionAccess): Promise<ReadonlyArray<AttentionQueueItem>> {
  const base = attentionBase(accountID);
  const sources: Array<Promise<ReadonlyArray<AttentionQueueItem>>> = [];
  if (access.work) {
    sources.push(readAllPages<InformationRequestSummary>(`${base}/information-requests?state=open&limit=100`)
      .then((items) => items.map((item) => ({ ...item, kind: "information" as const }))));
    sources.push(readAllPages<WorkReviewSummary>(`${base}/work-reviews?state=open&reviewer_id=${encodeURIComponent(userID)}&limit=100`)
      .then((items) => items.map((item) => ({ ...item, kind: "review" as const }))));
  }
  if (access.approvals) {
    sources.push(readAllPages<ApprovalSummary>(`${base}/approvals?state=open&limit=100`)
      .then((items) => items.map((item) => ({ ...item, kind: "approval" as const }))));
    for (const state of ["unknown", "manual_resolution"] as const) {
      sources.push(readAllPages<ActionRecoverySummary>(`${base}/actions?state=${state}&limit=100`)
        .then((items) => items.map((item) => ({ ...item, kind: "action" as const }))));
    }
  }
  const pages = await Promise.all(sources);
  return pages.flat().sort((left, right) => right.updated_at.localeCompare(left.updated_at));
}

export async function getAttentionDetail(accountID: string, kind: AttentionKind, id: string): Promise<AttentionDetail> {
  const value = await requestJSON<InformationRequest | WorkReview | Approval | ActionRecoveryDetail>(
    `${attentionBase(accountID)}/${collection(kind)}/${encodeURIComponent(id)}`
  );
  return { ...value, kind } as AttentionDetail;
}

export async function listMatchingFacts(accountID: string, requirement: InformationRequirement): Promise<ReadonlyArray<KnowledgeFactSummary>> {
  const query = new URLSearchParams({ scope_kind: requirement.scope, key_prefix: requirement.key, limit: "100" });
  if (requirement.scope_id) query.set("scope_id", requirement.scope_id);
  const page = await requestJSON<KnowledgeFactPage>(`/api/v1/accounts/${encodeURIComponent(accountID)}/knowledge/facts?${query}`);
  return page.items.filter((fact) => fact.state === "active" && fact.key === requirement.key
    && fact.scope.kind === requirement.scope && (fact.scope.id ?? "") === (requirement.scope_id ?? ""));
}

export function attentionItemID(item: AttentionQueueItem | AttentionDetail): string {
  return itemID(item);
}

export function answerInformation(accountID: string, item: InformationRequest, input: AnswerInformationRequestInput): Promise<InformationCompletion> {
  return command(`${attentionBase(accountID)}/information-requests/${encodeURIComponent(item.id)}/answers`, input, item.version);
}

export function decideWorkReview(accountID: string, item: WorkReview, input: DecideWorkReviewRequest): Promise<WorkReview> {
  return command(`${attentionBase(accountID)}/work-reviews/${encodeURIComponent(item.id)}/decisions`, input, item.version);
}

export function decideApproval(accountID: string, item: Approval, input: DecideApprovalRequest): Promise<Approval> {
  return command(`${attentionBase(accountID)}/approvals/${encodeURIComponent(item.id)}/decisions`, input, item.version);
}

export function requestActionResolution(accountID: string, item: ActionRecoveryDetail, input: RequestActionResolutionRequest): Promise<ActionRecoveryDetail> {
  return command(`${attentionBase(accountID)}/actions/${encodeURIComponent(item.operation_id)}/resolution-requests`, input);
}

export function confirmActionResolution(accountID: string, item: ActionRecoveryDetail): Promise<ActionRecoveryDetail> {
  if (!item.resolution) throw new Error("This recovery item has no pending resolution.");
  return command(`${attentionBase(accountID)}/actions/${encodeURIComponent(item.operation_id)}/resolutions/${encodeURIComponent(item.resolution.id)}/confirmations`, undefined);
}
