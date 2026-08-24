import { requestJSON } from "./client";
import type { AffiliateProgram, AffiliateStatement, EnrollAffiliateRequest } from "./generated/api-types";

export const getAffiliateProgram = (): Promise<AffiliateProgram> => requestJSON("/api/v1/affiliate");

export const enrollAffiliate = (input: EnrollAffiliateRequest): Promise<AffiliateProgram> =>
  requestJSON("/api/v1/affiliate", { method: "POST", body: JSON.stringify(input) });

export const getAffiliateStatement = (): Promise<AffiliateStatement> => requestJSON("/api/v1/affiliate/statement");
