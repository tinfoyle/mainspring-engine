import { requestJSON } from "./client";
import type {
  AffiliateProgram,
  AffiliateStatement,
  AffiliateSupportRequest,
  AffiliateSupportRequestCollection,
  EnrollAffiliateRequest,
  SubmitAffiliateSupportRequest
} from "./generated/api-types";

export const getAffiliateProgram = (): Promise<AffiliateProgram> => requestJSON("/api/v1/affiliate");

export const enrollAffiliate = (input: EnrollAffiliateRequest): Promise<AffiliateProgram> =>
  requestJSON("/api/v1/affiliate", { method: "POST", body: JSON.stringify(input) });

export const getAffiliateStatement = (): Promise<AffiliateStatement> => requestJSON("/api/v1/affiliate/statement");

export const getAffiliateSupportRequests = (): Promise<AffiliateSupportRequestCollection> =>
  requestJSON("/api/v1/affiliate/support-requests");

export const submitAffiliateSupportRequest = (input: SubmitAffiliateSupportRequest): Promise<AffiliateSupportRequest> =>
  requestJSON("/api/v1/affiliate/support-requests", { method: "POST", body: JSON.stringify(input) });

export const cancelAffiliateSupportRequest = (requestID: string): Promise<AffiliateSupportRequest> =>
  requestJSON(`/api/v1/affiliate/support-requests/${encodeURIComponent(requestID)}`, { method: "DELETE" });
