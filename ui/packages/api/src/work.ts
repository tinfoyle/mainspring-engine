import type {
  AssignWorkRequest,
  CreateWorkRequest,
  TransitionWorkRequest,
  WorkItem,
  WorkItems,
  WorkKind,
  WorkPage,
  WorkState,
  WorkSummary
} from "./generated/api-types";
import { requestJSON } from "./client";

export interface WorkFilters {
  readonly search?: string;
  readonly state?: WorkState;
  readonly kind?: WorkKind;
  readonly cursor?: string;
  readonly limit?: number;
}

const pendingOperations = new Map<string, string>();

function base(accountID: string): string {
  return `/api/v1/accounts/${encodeURIComponent(accountID)}/work-items`;
}

function operation(method: string, path: string, body: string, version?: number): { fingerprint: string; key: string } {
  const fingerprint = `${method} ${path} ${version ?? "unversioned"} ${body}`;
  let key = pendingOperations.get(fingerprint);
  if (!key) {
    key = crypto.randomUUID();
    pendingOperations.set(fingerprint, key);
  }
  return { fingerprint, key };
}

async function command<T>(method: "POST" | "PATCH", path: string, payload: unknown, version?: number): Promise<T> {
  const body = JSON.stringify(payload);
  const pending = operation(method, path, body, version);
  const headers = new Headers({ "Idempotency-Key": pending.key });
  if (version !== undefined) headers.set("If-Match", `W/"${version}"`);
  const result = await requestJSON<T>(path, { method, headers, body });
  pendingOperations.delete(pending.fingerprint);
  return result;
}

export function listWork(accountID: string, filters: WorkFilters = {}): Promise<WorkPage> {
  const query = new URLSearchParams({ limit: String(filters.limit ?? 30) });
  if (filters.search) query.set("q", filters.search);
  if (filters.state) query.set("state", filters.state);
  if (filters.kind) query.set("kind", filters.kind);
  if (filters.cursor) query.set("cursor", filters.cursor);
  return requestJSON<WorkPage>(`${base(accountID)}?${query}`);
}

export function getWorkSummary(accountID: string): Promise<WorkSummary> {
  return requestJSON<WorkSummary>(`${base(accountID)}/summary`);
}

export function getWorkItem(accountID: string, itemID: string): Promise<WorkItem> {
  return requestJSON<WorkItem>(`${base(accountID)}/${encodeURIComponent(itemID)}`);
}

export function listWorkChildren(accountID: string, itemID: string): Promise<WorkItems> {
  return requestJSON<WorkItems>(`${base(accountID)}/${encodeURIComponent(itemID)}/children?limit=100`);
}

export function createWork(accountID: string, input: CreateWorkRequest): Promise<WorkItem> {
  return command("POST", base(accountID), input);
}

export function transitionWork(accountID: string, item: WorkItem, input: TransitionWorkRequest): Promise<WorkItem> {
  return command("POST", `${base(accountID)}/${encodeURIComponent(item.id)}/transitions`, input, item.version);
}

export function assignWork(accountID: string, item: WorkItem, input: AssignWorkRequest): Promise<WorkItem> {
  return command("PATCH", `${base(accountID)}/${encodeURIComponent(item.id)}/assignment`, input, item.version);
}
