import { requestJSON } from "./client";
import type {
  OperationsAnalyticsReport, OperationsAnalyticsRequest, OperationsAuditReason,
  OperationsCreateSupportGrant, OperationsLookupPage, OperationsLookupRequest,
  OperationsPasskeyCeremony, OperationsRevokeSupportGrant, OperationsSession,
  OperationsSupportGrantEnvelope, OperationsSupportViewEnvelope
} from "./generated/api-types";

const root = "/api/operations/v1";

export function operationsSession(): Promise<OperationsSession> {
  return requestJSON<OperationsSession>(`${root}/session`);
}

export function beginOperationsPasskeyLogin(): Promise<OperationsPasskeyCeremony> {
  return requestJSON<OperationsPasskeyCeremony>(`${root}/passkey-login/challenges`, { method: "POST", body: "{}" });
}

export function completeOperationsPasskeyLogin(ceremonyID: string, credential: Readonly<Record<string, unknown>>, clientLabel: string): Promise<OperationsSession> {
  return requestJSON<OperationsSession>(`${root}/passkey-login/challenges/${encodeURIComponent(ceremonyID)}/complete`, {
    method: "POST", body: JSON.stringify({ credential, client_label: clientLabel })
  });
}

export function logoutOperations(): Promise<void> {
  return requestJSON<void>(`${root}/session`, { method: "DELETE" });
}

export function operationsLookup(input: OperationsLookupRequest): Promise<OperationsLookupPage> {
  return requestJSON<OperationsLookupPage>(`${root}/lookups`, { method: "POST", body: JSON.stringify(input) });
}

export function createOperationsSupportGrant(input: OperationsCreateSupportGrant): Promise<OperationsSupportGrantEnvelope> {
  return requestJSON<OperationsSupportGrantEnvelope>(`${root}/support-grants`, { method: "POST", body: JSON.stringify(input) });
}

export function openOperationsSupportView(grantID: string, audit: OperationsAuditReason): Promise<OperationsSupportViewEnvelope> {
  return requestJSON<OperationsSupportViewEnvelope>(`${root}/support-grants/${encodeURIComponent(grantID)}/views`, { method: "POST", body: JSON.stringify(audit) });
}

export function revokeOperationsSupportGrant(grantID: string, input: OperationsRevokeSupportGrant): Promise<OperationsSupportGrantEnvelope> {
  return requestJSON<OperationsSupportGrantEnvelope>(`${root}/support-grants/${encodeURIComponent(grantID)}/revocations`, { method: "POST", body: JSON.stringify(input) });
}

export function operationsAnalyticsReport(input: OperationsAnalyticsRequest): Promise<OperationsAnalyticsReport> {
  return requestJSON<OperationsAnalyticsReport>(`${root}/analytics/reports`, { method: "POST", body: JSON.stringify(input) });
}

