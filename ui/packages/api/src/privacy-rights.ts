import type {
  PrivacyRightsRequest,
  PrivacyRightsRequestInput,
  PrivacyRightsRequestList
} from "./generated/api-types";
import { requestJSON } from "./client";

export const listPrivacyRightsRequests = (): Promise<PrivacyRightsRequestList> =>
  requestJSON("/api/v1/privacy/rights-requests");

export const submitPrivacyRightsRequest = (input: PrivacyRightsRequestInput): Promise<PrivacyRightsRequest> =>
  requestJSON("/api/v1/privacy/rights-requests", { method: "POST", body: JSON.stringify(input) });

export const cancelPrivacyRightsRequest = (requestID: string): Promise<PrivacyRightsRequest> =>
  requestJSON(`/api/v1/privacy/rights-requests/${encodeURIComponent(requestID)}`, { method: "DELETE" });
