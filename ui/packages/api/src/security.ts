import type {
  ActiveSessions, ContactChangeAccepted, CurrentIdentity, MCPGrants, PasskeyCredential, Passkeys,
  RecoveryCodeRotation, RecoveryCodeStatus, SecurityEvents, SecurityPosture, SupportAccessHistory, WebAuthnAssertionCredential,
  WebAuthnCeremony, WebAuthnCreationCredential
} from "./generated/api-types";
import { requestJSON } from "./client";

export const getCurrentIdentity = (): Promise<CurrentIdentity> => requestJSON("/api/v1/identity");
export const getSecurityPosture = (): Promise<SecurityPosture> => requestJSON("/api/v1/security-posture");
export const getPasskeys = (): Promise<Passkeys> => requestJSON("/api/v1/passkeys");
export const getRecoveryCodeStatus = (): Promise<RecoveryCodeStatus> => requestJSON("/api/v1/recovery-codes");
export const getActiveSessions = (): Promise<ActiveSessions> => requestJSON("/api/v1/sessions");
export const getSecurityEvents = (): Promise<SecurityEvents> => requestJSON("/api/v1/security-events");
export const getSupportAccessHistory = (accountID: string): Promise<SupportAccessHistory> => requestJSON(`/api/v1/accounts/${encodeURIComponent(accountID)}/support-access-history`);
export const getMCPGrants = (): Promise<MCPGrants> => requestJSON("/api/v1/mcp-grants");

export function confirmPassword(password: string): Promise<void> {
  return requestJSON("/api/v1/session/reauthenticate", { method: "POST", body: JSON.stringify({ password }) });
}
export function beginPasskeyRegistration(): Promise<WebAuthnCeremony> { return requestJSON("/api/v1/passkey-registrations", { method: "POST" }); }
export function completePasskeyRegistration(ceremonyID: string, name: string, credential: WebAuthnCreationCredential): Promise<PasskeyCredential> {
  return requestJSON(`/api/v1/passkey-registrations/${encodeURIComponent(ceremonyID)}/complete`, { method: "POST", body: JSON.stringify({ name, credential }) });
}
export function beginPasskeyReauthentication(): Promise<WebAuthnCeremony> { return requestJSON("/api/v1/passkey-reauthentications", { method: "POST" }); }
export function completePasskeyReauthentication(ceremonyID: string, credential: WebAuthnAssertionCredential): Promise<void> {
  return requestJSON(`/api/v1/passkey-reauthentications/${encodeURIComponent(ceremonyID)}/complete`, { method: "POST", body: JSON.stringify({ credential }) });
}
export function renamePasskey(credentialID: string, name: string): Promise<void> { return requestJSON(`/api/v1/passkeys/${encodeURIComponent(credentialID)}`, { method: "PATCH", body: JSON.stringify({ name }) }); }
export function deletePasskey(credentialID: string): Promise<void> { return requestJSON(`/api/v1/passkeys/${encodeURIComponent(credentialID)}`, { method: "DELETE" }); }
export function compromisePasskey(credentialID: string): Promise<void> { return requestJSON(`/api/v1/passkeys/${encodeURIComponent(credentialID)}/compromise`, { method: "POST" }); }
export function rotateRecoveryCodes(): Promise<RecoveryCodeRotation> { return requestJSON("/api/v1/recovery-codes", { method: "POST" }); }
export function consumeRecoveryCode(code: string): Promise<void> { return requestJSON("/api/v1/recovery-codes/consume", { method: "POST", body: JSON.stringify({ code }) }); }
export function revokeSession(sessionID: string): Promise<void> { return requestJSON(`/api/v1/sessions/${encodeURIComponent(sessionID)}`, { method: "DELETE" }); }
export function revokeAllSessions(): Promise<void> { return requestJSON("/api/v1/sessions", { method: "DELETE" }); }
export function beginContactChange(newEmail: string): Promise<ContactChangeAccepted> { return requestJSON("/api/v1/contact-change-requests", { method: "POST", body: JSON.stringify({ new_email: newEmail }) }); }
export function revokeMCPGrant(grantID: string): Promise<void> { return requestJSON(`/api/v1/mcp-grants/${encodeURIComponent(grantID)}`, { method: "DELETE" }); }
