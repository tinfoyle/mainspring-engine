import type { PrivacyConsent, PrivacyConsentHistory, PrivacyConsentSelection } from "./generated/api-types";
import { requestJSON } from "./client";

export const getPrivacyConsent = (): Promise<PrivacyConsent> => requestJSON("/api/v1/privacy/consent");

export const setPrivacyConsent = (selection: PrivacyConsentSelection): Promise<PrivacyConsent> =>
  requestJSON("/api/v1/privacy/consent", { method: "PUT", body: JSON.stringify(selection) });

export const getPrivacyConsentHistory = (): Promise<PrivacyConsentHistory> =>
  requestJSON("/api/v1/privacy/consent/history");

export const erasePrivacyData = (): Promise<void> => requestJSON("/api/v1/privacy/data", { method: "DELETE" });
