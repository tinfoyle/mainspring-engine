import { requestJSON } from "./client";
import type {
  AdminLoginStatus, AdminSetup, AdminVerification, OperationsAnalyticsReport, OperationsAnalyticsRequest, OperationsAuditReason,
  OperationsAffiliateEnrollmentEnvelope, OperationsAffiliateRiskEnvelope, OperationsAffiliateTransitionRequest,
  OperationsBillingActionRequest, OperationsBillingFailuresReport, OperationsBillingFailuresRequest, OperationsBillingRecordEnvelope,
  OperationsDirectoryRequest, OperationsDirectoryPage,
  OperationsCreateSupportGrant, OperationsLookupPage, OperationsLookupRequest,
  OperationsPasskeyCeremony, OperationsRevokeSupportGrant, OperationsSession,
  OperationsPrivacyOpenRequest, OperationsPrivacyQueue, OperationsPrivacyRequestEnvelope,
  OperationsPrivacyResolutionRequest, OperationsPrivacyReviewRequest,
  OperationsSupportGrantEnvelope, OperationsSupportViewEnvelope,
  OperationsTrafficRequest, OperationsTrafficReport
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

export function operationsDirectory(input: OperationsDirectoryRequest): Promise<OperationsDirectoryPage> {
 return requestJSON<OperationsDirectoryPage>(`${root}/directory`, { method: "POST", body: JSON.stringify(input) });
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

export function operationsTrafficReport(input: OperationsTrafficRequest): Promise<OperationsTrafficReport> {
  return requestJSON<OperationsTrafficReport>(`${root}/traffic/reports`, { method: "POST", body: JSON.stringify(input) });
}

export function operationsBillingFailures(input: OperationsBillingFailuresRequest): Promise<OperationsBillingFailuresReport> {
  return requestJSON<OperationsBillingFailuresReport>(`${root}/billing/failures/reports`, { method: "POST", body: JSON.stringify(input) });
}

export function operationsReplayBillingEvent(eventID: string, input: OperationsBillingActionRequest): Promise<OperationsBillingRecordEnvelope> {
  return requestJSON<OperationsBillingRecordEnvelope>(`${root}/billing/events/${encodeURIComponent(eventID)}/replays`, { method: "POST", body: JSON.stringify(input) });
}

export function operationsRefreshBillingSubscription(subscriptionID: string, input: OperationsBillingActionRequest): Promise<OperationsBillingRecordEnvelope> {
  return requestJSON<OperationsBillingRecordEnvelope>(`${root}/billing/subscriptions/${encodeURIComponent(subscriptionID)}/refreshes`, { method: "POST", body: JSON.stringify(input) });
}

export function operationsOpenPrivacyRights(input: OperationsPrivacyOpenRequest): Promise<OperationsPrivacyQueue> {
  return requestJSON<OperationsPrivacyQueue>(`${root}/privacy-rights/reports/open`, { method: "POST", body: JSON.stringify(input) });
}

export function operationsInspectPrivacyRight(requestID: string, input: OperationsAuditReason): Promise<OperationsPrivacyRequestEnvelope> {
  return requestJSON<OperationsPrivacyRequestEnvelope>(`${root}/privacy-rights/${encodeURIComponent(requestID)}/inspections`, { method: "POST", body: JSON.stringify(input) });
}

export function operationsStartPrivacyReview(requestID: string, input: OperationsPrivacyReviewRequest): Promise<OperationsPrivacyRequestEnvelope> {
  return requestJSON<OperationsPrivacyRequestEnvelope>(`${root}/privacy-rights/${encodeURIComponent(requestID)}/review-starts`, { method: "POST", body: JSON.stringify(input) });
}

export function operationsResolvePrivacyRight(requestID: string, input: OperationsPrivacyResolutionRequest): Promise<OperationsPrivacyRequestEnvelope> {
  return requestJSON<OperationsPrivacyRequestEnvelope>(`${root}/privacy-rights/${encodeURIComponent(requestID)}/resolutions`, { method: "POST", body: JSON.stringify(input) });
}

export function operationsInspectAffiliate(affiliateID: string, input: OperationsAuditReason): Promise<OperationsAffiliateEnrollmentEnvelope> {
  return requestJSON<OperationsAffiliateEnrollmentEnvelope>(`${root}/affiliates/${encodeURIComponent(affiliateID)}/inspections`, { method: "POST", body: JSON.stringify(input) });
}

export function operationsInspectAffiliateRisk(affiliateID: string, input: OperationsAuditReason): Promise<OperationsAffiliateRiskEnvelope> {
  return requestJSON<OperationsAffiliateRiskEnvelope>(`${root}/affiliates/${encodeURIComponent(affiliateID)}/risk-inspections`, { method: "POST", body: JSON.stringify(input) });
}

export function operationsTransitionAffiliate(affiliateID: string, input: OperationsAffiliateTransitionRequest): Promise<OperationsAffiliateEnrollmentEnvelope> {
  return requestJSON<OperationsAffiliateEnrollmentEnvelope>(`${root}/affiliates/${encodeURIComponent(affiliateID)}/transitions`, { method: "POST", body: JSON.stringify(input) });
}

export function beginAdminLogin(): Promise<{ url: string }> { return requestJSON(`${root}/auth/start`, { method: "POST", body: "{}" }); }
export function adminLoginStatus(): Promise<AdminLoginStatus> { return requestJSON(`${root}/auth`); }
export function setupAdminAuthenticator(): Promise<AdminSetup> { return requestJSON(`${root}/auth/enrollment`, { method: "POST", body: "{}" }); }
export function verifyAdminAuthenticator(code: string, recovery = false): Promise<AdminVerification> { return requestJSON(`${root}/auth/verify`, { method: "POST", body: JSON.stringify({ code, recovery }) }); }
export function reauthenticateAdmin(code: string): Promise<void> { return requestJSON(`${root}/auth/reauthenticate`, { method: "POST", body: JSON.stringify({ code }) }); }
