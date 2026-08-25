import type {
  CloseFinancePeriodRequest,
  CreateFinanceEntryRequest,
  CreateFinanceLedgerRequest,
  CreateFinancePostingAccountRequest,
  CreateFinanceReconciliationRequest,
  FinanceEntryPage,
  FinanceEntryState,
  FinanceJournalEntry,
  FinanceLedger,
  FinanceLedgerPage,
  FinanceLedgerState,
  FinancePostingAccount,
  FinancePostingAccountPage,
  FinanceReconciliation,
  FinanceReconciliationPage,
  FinanceReconciliationState,
  FinanceReversal,
  ReverseFinanceEntryRequest,
  ReviseFinanceEntryRequest,
  ReviseFinanceLedgerRequest,
  ReviseFinancePostingAccountRequest
} from "./generated/api-types";
import { requestJSON } from "./client";

const pendingOperations = new Map<string, string>();
const root = (accountID: string): string => `/api/v1/accounts/${encodeURIComponent(accountID)}/finance`;
const ledger = (accountID: string, ledgerID: string): string => `${root(accountID)}/ledgers/${encodeURIComponent(ledgerID)}`;

async function command<T>(method: "POST" | "PUT" | "DELETE", path: string, input?: unknown, version?: number): Promise<T> {
  const body = input === undefined ? "" : JSON.stringify(input);
  const fingerprint = `${method} ${path} ${version ?? "unversioned"} ${body}`;
  let key = pendingOperations.get(fingerprint);
  if (!key) { key = crypto.randomUUID(); pendingOperations.set(fingerprint, key); }
  const headers = new Headers({ "Idempotency-Key": key });
  if (version !== undefined) headers.set("If-Match", `W/"${version}"`);
  const result = await requestJSON<T>(path, { method, headers, ...(body ? { body } : {}) });
  pendingOperations.delete(fingerprint);
  return result;
}

function page(path: string, state?: string, cursor?: string, limit = 100): string {
  const query = new URLSearchParams({ limit: String(limit) });
  if (state) query.set("state", state);
  if (cursor) query.set("cursor", cursor);
  return `${path}?${query}`;
}

export function listFinanceLedgers(accountID: string, state?: FinanceLedgerState, cursor?: string): Promise<FinanceLedgerPage> { return requestJSON(page(`${root(accountID)}/ledgers`, state, cursor)); }
export function getFinanceLedger(accountID: string, ledgerID: string): Promise<FinanceLedger> { return requestJSON(ledger(accountID, ledgerID)); }
export function createFinanceLedger(accountID: string, input: CreateFinanceLedgerRequest): Promise<FinanceLedger> { return command("POST", `${root(accountID)}/ledgers`, input); }
export function reviseFinanceLedger(accountID: string, value: FinanceLedger, input: ReviseFinanceLedgerRequest): Promise<FinanceLedger> { return command("PUT", ledger(accountID, value.id), input, value.version); }
export function closeFinancePeriod(accountID: string, value: FinanceLedger, input: CloseFinancePeriodRequest): Promise<FinanceLedger> { return command("POST", `${ledger(accountID, value.id)}/period-closes`, input, value.version); }
export function archiveFinanceLedger(accountID: string, value: FinanceLedger): Promise<FinanceLedger> { return command("DELETE", ledger(accountID, value.id), undefined, value.version); }

export function listFinanceAccounts(accountID: string, ledgerID: string, state?: FinanceLedgerState, cursor?: string): Promise<FinancePostingAccountPage> { return requestJSON(page(`${ledger(accountID, ledgerID)}/accounts`, state, cursor)); }
export function getFinanceAccount(accountID: string, postingAccountID: string): Promise<FinancePostingAccount> { return requestJSON(`${root(accountID)}/accounts/${encodeURIComponent(postingAccountID)}`); }
export function createFinanceAccount(accountID: string, ledgerID: string, input: CreateFinancePostingAccountRequest): Promise<FinancePostingAccount> { return command("POST", `${ledger(accountID, ledgerID)}/accounts`, input); }
export function reviseFinanceAccount(accountID: string, value: FinancePostingAccount, input: ReviseFinancePostingAccountRequest): Promise<FinancePostingAccount> { return command("PUT", `${root(accountID)}/accounts/${encodeURIComponent(value.id)}`, input, value.version); }
export function archiveFinanceAccount(accountID: string, value: FinancePostingAccount): Promise<FinancePostingAccount> { return command("DELETE", `${root(accountID)}/accounts/${encodeURIComponent(value.id)}`, undefined, value.version); }

export function listFinanceEntries(accountID: string, ledgerID: string, state?: FinanceEntryState, cursor?: string): Promise<FinanceEntryPage> { return requestJSON(page(`${ledger(accountID, ledgerID)}/entries`, state, cursor)); }
export function getFinanceEntry(accountID: string, entryID: string): Promise<FinanceJournalEntry> { return requestJSON(`${root(accountID)}/entries/${encodeURIComponent(entryID)}`); }
export function createFinanceEntry(accountID: string, ledgerID: string, input: CreateFinanceEntryRequest): Promise<FinanceJournalEntry> { return command("POST", `${ledger(accountID, ledgerID)}/entries`, input); }
export function reviseFinanceEntry(accountID: string, value: FinanceJournalEntry, input: ReviseFinanceEntryRequest): Promise<FinanceJournalEntry> { return command("PUT", `${root(accountID)}/entries/${encodeURIComponent(value.id)}`, input, value.version); }
export function postFinanceEntry(accountID: string, value: FinanceJournalEntry): Promise<FinanceJournalEntry> { return command("POST", `${root(accountID)}/entries/${encodeURIComponent(value.id)}/postings`, undefined, value.version); }
export function reverseFinanceEntry(accountID: string, value: FinanceJournalEntry, input: ReverseFinanceEntryRequest): Promise<FinanceReversal> { return command("POST", `${root(accountID)}/entries/${encodeURIComponent(value.id)}/reversals`, input, value.version); }

export function listFinanceReconciliations(accountID: string, ledgerID: string, state?: FinanceReconciliationState, cursor?: string): Promise<FinanceReconciliationPage> { return requestJSON(page(`${ledger(accountID, ledgerID)}/reconciliations`, state, cursor)); }
export function getFinanceReconciliation(accountID: string, reconciliationID: string): Promise<FinanceReconciliation> { return requestJSON(`${root(accountID)}/reconciliations/${encodeURIComponent(reconciliationID)}`); }
export function createFinanceReconciliation(accountID: string, ledgerID: string, input: CreateFinanceReconciliationRequest): Promise<FinanceReconciliation> { return command("POST", `${ledger(accountID, ledgerID)}/reconciliations`, input); }
export function confirmFinanceReconciliation(accountID: string, value: FinanceReconciliation): Promise<FinanceReconciliation> { return command("POST", `${root(accountID)}/reconciliations/${encodeURIComponent(value.id)}/confirmations`, undefined, value.version); }
