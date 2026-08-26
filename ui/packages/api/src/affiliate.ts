import { APIProblem, requestJSON } from "./client";
import type {
  AffiliateProgram,
  AffiliateStatement,
  AffiliateSupportRequest,
  AffiliateSupportRequestCollection,
  EnrollAffiliateRequest,
  Problem,
  ReplaceAffiliateCodeRequest,
  SubmitAffiliateSupportRequest
} from "./generated/api-types";

export const getAffiliateProgram = (): Promise<AffiliateProgram> => requestJSON("/api/v1/affiliate");

export const enrollAffiliate = (input: EnrollAffiliateRequest): Promise<AffiliateProgram> =>
  requestJSON("/api/v1/affiliate", { method: "POST", body: JSON.stringify(input) });

export const replaceAffiliateCode = (input: ReplaceAffiliateCodeRequest): Promise<AffiliateProgram> =>
  requestJSON("/api/v1/affiliate/code-replacements", { method: "POST", body: JSON.stringify(input) });

export const getAffiliateStatement = (): Promise<AffiliateStatement> => requestJSON("/api/v1/affiliate/statement");

export async function downloadAffiliateDataExport(): Promise<Blob> {
  const response = await fetch("/api/v1/affiliate/data-export", { credentials: "same-origin" });
  if (!response.ok) {
    let problem: Problem | undefined;
    if (response.headers.get("content-type")?.includes("application/problem+json")) problem = (await response.json()) as Problem;
    throw new APIProblem(response.status, problem);
  }
  return response.blob();
}

export const getAffiliateSupportRequests = (): Promise<AffiliateSupportRequestCollection> =>
  requestJSON("/api/v1/affiliate/support-requests");

export const submitAffiliateSupportRequest = (input: SubmitAffiliateSupportRequest): Promise<AffiliateSupportRequest> =>
  requestJSON("/api/v1/affiliate/support-requests", { method: "POST", body: JSON.stringify(input) });

export const cancelAffiliateSupportRequest = (requestID: string): Promise<AffiliateSupportRequest> =>
  requestJSON(`/api/v1/affiliate/support-requests/${encodeURIComponent(requestID)}`, { method: "DELETE" });
