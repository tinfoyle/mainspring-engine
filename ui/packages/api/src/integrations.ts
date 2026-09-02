import type {
  IntegrationAuthorization,
  IntegrationAuthorizationBegin,
  IntegrationCapability,
  IntegrationConnection,
  IntegrationConnectionDefinitionRequest,
  IntegrationConnectionDetail,
  IntegrationConnectionPage,
  IntegrationConnectionRevisionRequest,
  IntegrationConnectionState,
  IntegrationConnectorKind,
  IntegrationCredentialBindingRequest,
  IntegrationCredentialRevocation,
  IntegrationCredentialRotationRequest,
  IntegrationExecution,
  IntegrationExecutionDetail,
  IntegrationExecutionPage,
  IntegrationExecutionPrepareRequest,
  IntegrationExecutionResolutionRequest,
  IntegrationExecutionState,
  IntegrationHealthPage,
  IntegrationWebResearchReadRequest,
  IntegrationWebResearchReadResult,
  IntegrationWebResearchSearchRequest,
  IntegrationWebResearchSearchResult
} from "./generated/api-types";
import { requestJSON } from "./client";

const pendingOperations = new Map<string, string>();
const root = (accountID: string): string => `/api/v1/accounts/${encodeURIComponent(accountID)}/integrations`;
const connection = (accountID: string, connectionID: string): string => `${root(accountID)}/connections/${encodeURIComponent(connectionID)}`;

async function command<T>(method: "POST" | "PUT", path: string, input?: unknown, version?: number): Promise<T> {
  const body = input === undefined ? "" : JSON.stringify(input);
  const fingerprint = `${method} ${path} ${version ?? "unversioned"} ${body}`;
  let key = pendingOperations.get(fingerprint);
  if (!key) { key = crypto.randomUUID(); pendingOperations.set(fingerprint, key); }
  const headers = new Headers({ "Idempotency-Key": key });
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

export interface IntegrationConnectionFilters {
  state?: IntegrationConnectionState | undefined;
  kind?: IntegrationConnectorKind | undefined;
  cursor?: string | undefined;
  limit?: number | undefined;
}

export interface IntegrationExecutionFilters {
  connectionID?: string | undefined;
  state?: IntegrationExecutionState | undefined;
  capability?: IntegrationCapability | undefined;
  cursor?: string | undefined;
  limit?: number | undefined;
}

export async function listIntegrationConnections(accountID: string, filters: IntegrationConnectionFilters = {}): Promise<IntegrationConnectionPage> {
  const result = await requestJSON<IntegrationConnectionPage>(page(`${root(accountID)}/connections`, { state: filters.state, kind: filters.kind, cursor: filters.cursor, limit: filters.limit ?? 100 }));
  return { ...result, items: result.items ?? [] };
}
export function getIntegrationConnection(accountID: string, connectionID: string): Promise<IntegrationConnectionDetail> { return requestJSON(connection(accountID, connectionID)); }
export function createIntegrationConnection(accountID: string, input: IntegrationConnectionDefinitionRequest): Promise<IntegrationConnection> { return command("POST", `${root(accountID)}/connections`, input); }
export function reviseIntegrationConnection(accountID: string, value: IntegrationConnection, input: IntegrationConnectionRevisionRequest): Promise<IntegrationConnection> { return command("PUT", connection(accountID, value.id), input, value.version); }
export function activateIntegrationCredential(accountID: string, value: IntegrationConnection, input: IntegrationCredentialBindingRequest): Promise<IntegrationConnection> { return command("POST", `${connection(accountID, value.id)}/credential-bindings`, input, value.version); }
export function rotateIntegrationCredential(accountID: string, value: IntegrationConnection, input: IntegrationCredentialRotationRequest): Promise<IntegrationConnection> { return command("POST", `${connection(accountID, value.id)}/credential-rotations`, input, value.version); }
export function disableIntegrationConnection(accountID: string, value: IntegrationConnection): Promise<IntegrationConnection> { return command("POST", `${connection(accountID, value.id)}/disables`, undefined, value.version); }
export function enableIntegrationConnection(accountID: string, value: IntegrationConnection): Promise<IntegrationConnection> { return command("POST", `${connection(accountID, value.id)}/enables`, undefined, value.version); }
export function revokeIntegrationConnection(accountID: string, value: IntegrationConnection): Promise<IntegrationConnection> { return command("POST", `${connection(accountID, value.id)}/revocations`, undefined, value.version); }
export async function listIntegrationHealth(accountID: string, connectionID: string, cursor?: string): Promise<IntegrationHealthPage> {
  const result = await requestJSON<IntegrationHealthPage>(page(`${connection(accountID, connectionID)}/health`, { cursor, limit: 100 }));
  return { ...result, items: result.items ?? [] };
}

export function beginIntegrationAuthorization(accountID: string, connectionID: string, redirectURI: string): Promise<IntegrationAuthorizationBegin> { return command("POST", `${connection(accountID, connectionID)}/authorizations`, { redirect_uri: redirectURI }); }
export function getIntegrationAuthorization(accountID: string, authorizationID: string): Promise<IntegrationAuthorization> { return requestJSON(`${root(accountID)}/authorizations/${encodeURIComponent(authorizationID)}`); }
export function revokeIntegrationCredential(accountID: string, connectionID: string): Promise<IntegrationCredentialRevocation> { return command("POST", `${connection(accountID, connectionID)}/credential-revocations`); }

export async function listIntegrationExecutions(accountID: string, filters: IntegrationExecutionFilters = {}): Promise<IntegrationExecutionPage> {
  const result = await requestJSON<IntegrationExecutionPage>(page(`${root(accountID)}/executions`, { connection_id: filters.connectionID, state: filters.state, capability: filters.capability, cursor: filters.cursor, limit: filters.limit ?? 100 }));
  return { ...result, items: result.items ?? [] };
}
export function getIntegrationExecution(accountID: string, executionID: string): Promise<IntegrationExecutionDetail> { return requestJSON(`${root(accountID)}/executions/${encodeURIComponent(executionID)}`); }
export function prepareIntegrationExecution(accountID: string, input: IntegrationExecutionPrepareRequest): Promise<IntegrationExecution> { return command("POST", `${root(accountID)}/executions`, input); }
export function requestIntegrationResolution(accountID: string, executionID: string, input: IntegrationExecutionResolutionRequest): Promise<IntegrationExecutionDetail> { return command("POST", `${root(accountID)}/executions/${encodeURIComponent(executionID)}/resolution-requests`, input); }
export function confirmIntegrationResolution(accountID: string, executionID: string, resolutionID: string): Promise<IntegrationExecutionDetail> { return command("POST", `${root(accountID)}/executions/${encodeURIComponent(executionID)}/resolutions/${encodeURIComponent(resolutionID)}/confirmations`); }

export function searchIntegrationWeb(accountID: string, input: IntegrationWebResearchSearchRequest): Promise<IntegrationWebResearchSearchResult> { return requestJSON(`${root(accountID)}/web-research/search`, { method: "POST", body: JSON.stringify(input) }); }
export function readIntegrationWeb(accountID: string, input: IntegrationWebResearchReadRequest): Promise<IntegrationWebResearchReadResult> { return command("POST", `${root(accountID)}/web-research/read`, input); }
