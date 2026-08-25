import type { AccountClosure, AccountClosures, AccountExport, AccountExportDownloadCapability, AccountExports, Problem } from "./generated/api-types";
import { APIProblem, requestJSON } from "./client";

const account = (accountID: string): string => `/api/v1/accounts/${encodeURIComponent(accountID)}`;
export function listAccountExports(accountID: string): Promise<AccountExports> { return requestJSON(`${account(accountID)}/exports?limit=100`); }
export function createAccountExport(accountID: string): Promise<AccountExport> { return requestJSON(`${account(accountID)}/exports`, { method: "POST" }); }
export function cancelAccountExport(accountID: string, value: AccountExport): Promise<AccountExport> {
  return requestJSON(`${account(accountID)}/exports/${encodeURIComponent(value.id)}`, { method: "DELETE", body: JSON.stringify({ expected_version: value.version }) });
}
export function createExportDownloadCapability(accountID: string, exportID: string): Promise<AccountExportDownloadCapability> {
  return requestJSON(`${account(accountID)}/exports/${encodeURIComponent(exportID)}/download-capabilities`, { method: "POST" });
}
export async function downloadAccountExport(exportID: string, token: string): Promise<Blob> {
  const response = await fetch(`/api/v1/account-exports/${encodeURIComponent(exportID)}/artifact`, { headers: { Authorization: `SPYGLASS-ACCOUNT-EXPORT ${token}` }, credentials: "omit" });
  if (!response.ok) {
    let problem: Problem | undefined;
    if (response.headers.get("content-type")?.includes("application/problem+json")) problem = (await response.json()) as Problem;
    throw new APIProblem(response.status, problem);
  }
  return response.blob();
}
export function listAccountClosures(): Promise<AccountClosures> { return requestJSON("/api/v1/account-closures"); }
export function requestAccountClosure(accountID: string, accountVersion: number, reason: string): Promise<AccountClosure> {
  return requestJSON(`${account(accountID)}/closure`, { method: "POST", body: JSON.stringify({ expected_account_version: accountVersion, reason }) });
}
export function cancelAccountClosure(value: AccountClosure, reason: string): Promise<AccountClosure> {
  return requestJSON(`${account(value.account_id)}/closure`, { method: "DELETE", body: JSON.stringify({ expected_account_version: value.account_version, reason }) });
}
