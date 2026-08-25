import type {
  AnswerBaselineRequest,
  ApproveBaselinePlanRequest,
  BaselineAssessment,
  BaselineReassessment,
  BaselineSourceGrant,
  BaselineSourceGrantPage,
  ConfirmBaselineWorkEvidenceRequest,
  CreateBaselineSourceGrantRequest,
  DecideBaselineEvidenceRequest,
  DispositionBaselineRequirementRequest,
  RevokeBaselineSourceGrantRequest,
  WorkPage
} from "./generated/api-types";
import { requestJSON } from "./client";

const pendingOperations = new Map<string, string>();
const root = (accountID: string): string => `/api/v1/accounts/${encodeURIComponent(accountID)}/baseline-assessments`;
const assessment = (accountID: string, assessmentID: string): string => `${root(accountID)}/${encodeURIComponent(assessmentID)}`;

async function command<T>(path: string, input: unknown = {}, version?: number): Promise<T> {
  const body = JSON.stringify(input);
  const fingerprint = `${path} ${version ?? "unversioned"} ${body}`;
  let operationID = pendingOperations.get(fingerprint);
  if (!operationID) { operationID = crypto.randomUUID(); pendingOperations.set(fingerprint, operationID); }
  const headers = new Headers({ "Idempotency-Key": operationID });
  if (version !== undefined) headers.set("If-Match", `W/"${version}"`);
  const result = await requestJSON<T>(path, { method: "POST", headers, body });
  pendingOperations.delete(fingerprint);
  return result;
}

export function getCurrentBaseline(accountID: string): Promise<BaselineAssessment> { return requestJSON(`${root(accountID)}/current`); }
export function getBaseline(accountID: string, assessmentID: string): Promise<BaselineAssessment> { return requestJSON(assessment(accountID, assessmentID)); }
export function startBaseline(accountID: string): Promise<BaselineAssessment> { return command(root(accountID)); }
export function answerBaseline(accountID: string, value: BaselineAssessment, input: AnswerBaselineRequest): Promise<BaselineAssessment> { return command(`${assessment(accountID, value.id)}/answers`, input, value.version); }
export function beginBaselineInventory(accountID: string, value: BaselineAssessment): Promise<BaselineAssessment> { return command(`${assessment(accountID, value.id)}/inventory-starts`, {}, value.version); }
export function completeBaselineInventory(accountID: string, value: BaselineAssessment): Promise<BaselineAssessment> { return command(`${assessment(accountID, value.id)}/inventories`, {}, value.version); }
export function decideBaselineEvidence(accountID: string, value: BaselineAssessment, input: DecideBaselineEvidenceRequest): Promise<BaselineAssessment> { return command(`${assessment(accountID, value.id)}/evidence-decisions`, input, value.version); }
export function dispositionBaselineRequirement(accountID: string, value: BaselineAssessment, input: DispositionBaselineRequirementRequest): Promise<BaselineAssessment> { return command(`${assessment(accountID, value.id)}/dispositions`, input, value.version); }
export function submitBaselinePlan(accountID: string, value: BaselineAssessment): Promise<BaselineAssessment> { return command(`${assessment(accountID, value.id)}/plans`, {}, value.version); }
export function approveBaselinePlan(accountID: string, value: BaselineAssessment, input: ApproveBaselinePlanRequest): Promise<BaselineAssessment> { return command(`${assessment(accountID, value.id)}/plan-approvals`, input, value.version); }
export function materializeBaselinePlan(accountID: string, value: BaselineAssessment, input: ApproveBaselinePlanRequest): Promise<WorkPage> { return command(`${assessment(accountID, value.id)}/work-materializations`, input, value.version); }
export function confirmBaselineWorkEvidence(accountID: string, value: BaselineAssessment, input: ConfirmBaselineWorkEvidenceRequest): Promise<BaselineAssessment> { return command(`${assessment(accountID, value.id)}/work-evidence-confirmations`, input, value.version); }
export function materializeBaselineMaintenance(accountID: string, value: BaselineAssessment): Promise<WorkPage> { return command(`${assessment(accountID, value.id)}/maintenance-work-materializations`, {}, value.version); }
export function markBaselineReady(accountID: string, value: BaselineAssessment): Promise<BaselineAssessment> { return command(`${assessment(accountID, value.id)}/readiness`, {}, value.version); }
export function reassessBaseline(accountID: string, value: BaselineAssessment): Promise<BaselineReassessment> { return command(`${assessment(accountID, value.id)}/reassessments`, {}, value.version); }

export function listBaselineSourceGrants(accountID: string, assessmentID: string, cursor?: string): Promise<BaselineSourceGrantPage> {
  const query = new URLSearchParams({ limit: "100" }); if (cursor) query.set("cursor", cursor);
  return requestJSON(`${assessment(accountID, assessmentID)}/source-grants?${query}`);
}
export function createBaselineSourceGrant(accountID: string, assessmentID: string, input: CreateBaselineSourceGrantRequest): Promise<BaselineSourceGrant> { return command(`${assessment(accountID, assessmentID)}/source-grants`, input); }
export function revokeBaselineSourceGrant(accountID: string, assessmentID: string, value: BaselineSourceGrant, input: RevokeBaselineSourceGrantRequest): Promise<BaselineSourceGrant> { return command(`${assessment(accountID, assessmentID)}/source-grants/${encodeURIComponent(value.id)}/revocations`, input, value.version); }
