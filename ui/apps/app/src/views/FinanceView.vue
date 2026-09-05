<script setup lang="ts">
import {
  captureOwnerKnowledgeEvidence,
  APIProblem, archiveFinanceAccount, archiveFinanceLedger, closeFinancePeriod, confirmFinanceReconciliation, createFinanceAccount, createFinanceEntry, createFinanceLedger, createFinanceReconciliation, getFinanceAccount, getFinanceEntry, getFinanceLedger, getFinanceReconciliation, listFinanceAccounts, listFinanceEntries, listFinanceLedgers, listFinanceReconciliations, postFinanceEntry, reverseFinanceEntry, reviseFinanceAccount, reviseFinanceEntry, reviseFinanceLedger,
  type FinanceAccountType, type FinanceEntryState, type FinanceEntrySummary, type FinanceJournalEntry, type FinanceJournalLine, type FinanceLedger, type FinanceLedgerSummary, type FinancePostingAccount, type FinancePostingAccountSummary, type FinanceReconciliation, type FinanceReconciliationSummary
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";

type Tab = "accounts" | "journal" | "reconciliation";
type Action = "ledger-create" | "ledger-edit" | "ledger-close" | "ledger-archive" | "account-create" | "account-edit" | "account-archive" | "entry-create" | "entry-edit" | "entry-post" | "entry-reverse" | "reconciliation-create" | "reconciliation-confirm";
interface EditableLine { account_id: string; memo: string; debit: string; credit: string }
const session = useSessionStore(); const route = useRoute(); const router = useRouter();
const ledgers = ref<ReadonlyArray<FinanceLedgerSummary>>([]); const activeLedger = ref<FinanceLedger>(); const accounts = ref<ReadonlyArray<FinancePostingAccountSummary>>([]); const entries = ref<ReadonlyArray<FinanceEntrySummary>>([]); const reconciliations = ref<ReadonlyArray<FinanceReconciliationSummary>>([]);
const selectedAccount = ref<FinancePostingAccount>(); const selectedEntry = ref<FinanceJournalEntry>(); const selectedReconciliation = ref<FinanceReconciliation>();
const tab = ref<Tab>("accounts"); const entryState = ref<"" | FinanceEntryState>(""); const loading = ref(false); const saving = ref(false); const error = ref(""); const announcement = ref(""); const navigationNotice = ref(""); let sequence = 0;
const ledgerCursor = ref<string>(); const accountCursor = ref<string>(); const entryCursor = ref<string>(); const reconciliationCursor = ref<string>(); const loadingMore = ref(false);
const modalOpen = ref(false); const action = ref<Action>("ledger-create"); const confirmation = ref("");
const name = ref(""); const code = ref(""); const description = ref(""); const currency = ref("USD"); const dateValue = ref(""); const reference = ref(""); const evidence = ref("");
const accountType = ref<FinanceAccountType>("asset"); const parentID = ref(""); const allowPosting = ref(true); const lines = ref<EditableLine[]>([]); const postingAccountID = ref(""); const statementBalance = ref("");
const supportingNote = ref("");

const capturedNotes = new Map<string, string>();
const acceptsEvidence = computed(() => ["ledger-close", "entry-create", "entry-edit", "entry-post", "entry-reverse", "reconciliation-create"].includes(action.value));
const savedEvidenceCount = computed(() => action.value === "entry-post" ? selectedEntry.value?.evidence?.length ?? 0 : ids(evidence.value).length);
const needsSupportingNote = computed(() => acceptsEvidence.value && !["entry-create", "entry-edit"].includes(action.value) && savedEvidenceCount.value === 0);
const financePackage = computed(() => session.selected?.entitlements.packages.find((item) => item.code === "finance"));
const available = computed(() => Boolean(financePackage.value && financePackage.value.mode !== "suspended"));
const writable = computed(() => financePackage.value?.mode === "enabled" && ["owner", "administrator", "member"].includes(session.selected?.role ?? ""));
const manageable = computed(() => financePackage.value?.mode === "enabled" && ["owner", "administrator"].includes(session.selected?.role ?? ""));
const postingAccounts = computed(() => accounts.value.filter((item) => item.account.state === "active" && item.account.allow_posting));
const summary = computed(() => ledgers.value.find((item) => item.id === activeLedger.value?.id));
const lineTotals = computed(() => lines.value.reduce((total, line) => ({ debit: total.debit + toMinor(line.debit), credit: total.credit + toMinor(line.credit) }), { debit: 0, credit: 0 }));
const balanced = computed(() => lines.value.length >= 2 && lines.value.every((line) => line.account_id && ((toMinor(line.debit) > 0) !== (toMinor(line.credit) > 0))) && lineTotals.value.debit > 0 && lineTotals.value.debit === lineTotals.value.credit);
const { allowNextNavigation } = useSafeNavigation({
  dirty: modalOpen,
  pending: saving,
  message: "Leave Finance? Your open ledger command will be lost.",
  onBlocked: (blockedReason) => {
    navigationNotice.value = blockedReason === "pending"
      ? "This Finance change is still being saved. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your Finance command remains open.";
  }
});

function today(): string {
  const value = new Date();
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, "0")}-${String(value.getDate()).padStart(2, "0")}`;
}
function toISO(value: string): string { return new Date(`${value}T00:00:00.000Z`).toISOString(); }
function toMinor(value: string): number { const number = Number(value); return Number.isFinite(number) ? Math.round(number * 100) : 0; }
function major(value: number): string { return (value / 100).toFixed(2); }
function ids(value: string): string[] { return value.split(",").map((item) => item.trim()).filter(Boolean); }
function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function date(value?: string): string { return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeZone: "UTC" }).format(new Date(value)) : "Not set"; }
function money(minor = 0, value = activeLedger.value?.currency ?? "USD"): string { try { return new Intl.NumberFormat(undefined, { style: "currency", currency: value }).format(minor / 100); } catch { return `${value} ${(minor / 100).toFixed(2)}`; } }
function accountName(id: string): string { const match = accounts.value.find((item) => item.account.id === id); return match ? `${match.account.code} · ${match.account.name}` : id; }
function resetDetails(): void { selectedAccount.value = undefined; selectedEntry.value = undefined; selectedReconciliation.value = undefined; }
function target(): { kind?: "ledger" | "account" | "entry" | "reconciliation"; id?: string } {
  if (typeof route.params.postingAccountID === "string") return { kind: "account", id: route.params.postingAccountID };
  if (typeof route.params.entryID === "string") return { kind: "entry", id: route.params.entryID };
  if (typeof route.params.reconciliationID === "string") return { kind: "reconciliation", id: route.params.reconciliationID };
  if (typeof route.params.ledgerID === "string") return { kind: "ledger", id: route.params.ledgerID };
  return {};
}
async function load(): Promise<void> {
  const accountID = session.selectedID; const current = ++sequence; ledgers.value = []; activeLedger.value = undefined; accounts.value = []; entries.value = []; reconciliations.value = []; ledgerCursor.value = undefined; accountCursor.value = undefined; entryCursor.value = undefined; reconciliationCursor.value = undefined; resetDetails(); error.value = "";
  if (!accountID || !available.value) return; loading.value = true;
  try {
    const routeTarget = target(); let ledgerID = routeTarget.kind === "ledger" ? routeTarget.id : undefined;
    if (routeTarget.kind === "account" && routeTarget.id) { selectedAccount.value = await getFinanceAccount(accountID, routeTarget.id); ledgerID = selectedAccount.value.ledger_id; tab.value = "accounts"; }
    if (routeTarget.kind === "entry" && routeTarget.id) { selectedEntry.value = await getFinanceEntry(accountID, routeTarget.id); ledgerID = selectedEntry.value.ledger_id; tab.value = "journal"; }
    if (routeTarget.kind === "reconciliation" && routeTarget.id) { selectedReconciliation.value = await getFinanceReconciliation(accountID, routeTarget.id); ledgerID = selectedReconciliation.value.ledger_id; tab.value = "reconciliation"; }
    const page = await listFinanceLedgers(accountID); if (current !== sequence) return; ledgers.value = page.items; ledgerCursor.value = page.next_cursor; ledgerID ??= page.items[0]?.id;
    if (!ledgerID) return; activeLedger.value = await getFinanceLedger(accountID, ledgerID);
    const [accountPage, entryPage, reconciliationPage] = await Promise.all([listFinanceAccounts(accountID, ledgerID), listFinanceEntries(accountID, ledgerID, entryState.value || undefined), listFinanceReconciliations(accountID, ledgerID)]);
    if (current === sequence) { accounts.value = accountPage.items; accountCursor.value = accountPage.next_cursor; entries.value = entryPage.items; entryCursor.value = entryPage.next_cursor; reconciliations.value = reconciliationPage.items; reconciliationCursor.value = reconciliationPage.next_cursor; }
  } catch (cause) { if (current === sequence) error.value = cause instanceof APIProblem ? cause.message : "Finance is temporarily unavailable."; }
  finally { if (current === sequence) loading.value = false; }
}
async function loadMore(kind: "ledgers" | Tab): Promise<void> {
  const accountID = session.selectedID; const ledgerID = activeLedger.value?.id; if (!accountID || loadingMore.value) return; loadingMore.value = true;
  try {
    if (kind === "ledgers" && ledgerCursor.value) { const page = await listFinanceLedgers(accountID, undefined, ledgerCursor.value); ledgers.value = [...ledgers.value, ...page.items]; ledgerCursor.value = page.next_cursor; }
    if (kind === "accounts" && ledgerID && accountCursor.value) { const page = await listFinanceAccounts(accountID, ledgerID, undefined, accountCursor.value); accounts.value = [...accounts.value, ...page.items]; accountCursor.value = page.next_cursor; }
    if (kind === "journal" && ledgerID && entryCursor.value) { const page = await listFinanceEntries(accountID, ledgerID, entryState.value || undefined, entryCursor.value); entries.value = [...entries.value, ...page.items]; entryCursor.value = page.next_cursor; }
    if (kind === "reconciliation" && ledgerID && reconciliationCursor.value) { const page = await listFinanceReconciliations(accountID, ledgerID, undefined, reconciliationCursor.value); reconciliations.value = [...reconciliations.value, ...page.items]; reconciliationCursor.value = page.next_cursor; }
  } catch (cause) { error.value = cause instanceof APIProblem ? cause.message : "More Finance records could not be loaded."; }
  finally { loadingMore.value = false; }
}
async function navigate(path: string): Promise<void> { if (route.path === path) await load(); else await router.push(path); }
function selectLedger(event: Event): void { const value = (event.target as HTMLSelectElement).value; if (value) void navigate(`/app/finance/ledgers/${encodeURIComponent(value)}`); }
function selectTab(value: Tab): void {
  tab.value = value; resetDetails();
  if (activeLedger.value && target().kind && target().kind !== "ledger") void router.replace(`/app/finance/ledgers/${activeLedger.value.id}`);
}
function addLine(): void { lines.value.push({ account_id: "", memo: "", debit: "", credit: "" }); }
function removeLine(index: number): void { if (lines.value.length > 2) lines.value.splice(index, 1); }
function blankLines(): EditableLine[] { return [{ account_id: "", memo: "", debit: "", credit: "" }, { account_id: "", memo: "", debit: "", credit: "" }]; }
function begin(value: Action): void {
  supportingNote.value = ""; capturedNotes.clear();
  action.value = value; confirmation.value = ""; error.value = ""; name.value = ""; code.value = ""; description.value = ""; currency.value = "USD"; dateValue.value = today(); reference.value = ""; evidence.value = ""; accountType.value = "asset"; parentID.value = ""; allowPosting.value = true; lines.value = blankLines(); postingAccountID.value = ""; statementBalance.value = "";
  if (value === "ledger-edit" && activeLedger.value) { name.value = activeLedger.value.name; code.value = activeLedger.value.code; description.value = activeLedger.value.description; currency.value = activeLedger.value.currency; }
  if (value === "account-edit" && selectedAccount.value) { name.value = selectedAccount.value.name; code.value = selectedAccount.value.code; description.value = selectedAccount.value.description; accountType.value = selectedAccount.value.type; parentID.value = selectedAccount.value.parent_account_id ?? ""; allowPosting.value = selectedAccount.value.allow_posting; }
  if (value === "entry-edit" && selectedEntry.value) { dateValue.value = selectedEntry.value.entry_date.slice(0, 10); description.value = selectedEntry.value.description; reference.value = selectedEntry.value.reference; evidence.value = (selectedEntry.value.evidence ?? []).join(", "); lines.value = selectedEntry.value.lines.map((line) => ({ account_id: line.account_id, memo: line.memo, debit: line.debit_minor ? major(line.debit_minor) : "", credit: line.credit_minor ? major(line.credit_minor) : "" })); }
  if (value === "entry-reverse" && selectedEntry.value) { description.value = `Reversal of #${selectedEntry.value.number}: ${selectedEntry.value.description}`; reference.value = `REV-${selectedEntry.value.reference || selectedEntry.value.number}`; evidence.value = (selectedEntry.value.evidence ?? []).join(", "); }
  modalOpen.value = true;
}
function phrase(): string { const values: Partial<Record<Action, string>> = { "ledger-close": "CLOSE", "ledger-archive": "ARCHIVE", "account-archive": "ARCHIVE", "entry-post": "POST", "entry-reverse": "REVERSE", "reconciliation-confirm": "CONFIRM" }; return values[action.value] ?? ""; }
function requiresPhrase(): boolean { return phrase() !== ""; }
function title(): string { return ({ "ledger-create": "Create ledger", "ledger-edit": "Edit ledger", "ledger-close": "Close ledger period", "ledger-archive": "Archive ledger", "account-create": "Add account", "account-edit": "Edit account", "account-archive": "Archive chart account", "entry-create": "Save draft", "entry-edit": "Save draft changes", "entry-post": "Post entry", "entry-reverse": "Reverse posted entry", "reconciliation-create": "Compare statement balance", "reconciliation-confirm": "Confirm reconciliation" } satisfies Record<Action, string>)[action.value]; }
function journalInput(): { entry_date: string; description: string; reference: string; lines: FinanceJournalLine[]; evidence: string[] } {
  return { entry_date: toISO(dateValue.value), description: description.value.trim(), reference: reference.value.trim(), lines: lines.value.map((line) => ({ account_id: line.account_id, memo: line.memo.trim(), debit_minor: toMinor(line.debit), credit_minor: toMinor(line.credit) })), evidence: ids(evidence.value) };
}
async function submit(): Promise<void> {
  const accountID = session.selectedID; const ledgerValue = activeLedger.value; if (!accountID || saving.value || (requiresPhrase() && confirmation.value !== phrase())) return; saving.value = true; error.value = ""; navigationNotice.value = "";
  try {
    if (needsSupportingNote.value && supportingNote.value.trim().length < 3) {
      error.value = "Add a supporting note before continuing.";
      return;
    }
    if (acceptsEvidence.value && supportingNote.value.trim()) {
      const note = supportingNote.value.trim();
      let evidenceID = capturedNotes.get(note);
      if (!evidenceID) {
        const captured = await captureOwnerKnowledgeEvidence(accountID, note, `Finance: ${note}`);
        evidenceID = captured.id;
        capturedNotes.set(note, evidenceID);
      }
      evidence.value = [...new Set([...ids(evidence.value), evidenceID])].join(",");
    }
    let path = route.path;
    if (action.value === "ledger-create") { const value = await createFinanceLedger(accountID, { name: name.value.trim(), code: code.value.trim(), description: description.value.trim(), currency: currency.value.toUpperCase() }); path = `/app/finance/ledgers/${value.id}`; }
    else if (action.value === "ledger-edit" && ledgerValue) { await reviseFinanceLedger(accountID, ledgerValue, { name: name.value.trim(), code: code.value.trim(), description: description.value.trim() }); }
    else if (action.value === "ledger-close" && ledgerValue) { await closeFinancePeriod(accountID, ledgerValue, { through: toISO(dateValue.value), evidence: ids(evidence.value) }); }
    else if (action.value === "ledger-archive" && ledgerValue) { await archiveFinanceLedger(accountID, ledgerValue); path = "/app/finance"; }
    else if (action.value === "account-create" && ledgerValue) { const value = await createFinanceAccount(accountID, ledgerValue.id, { name: name.value.trim(), code: code.value.trim(), description: description.value.trim(), type: accountType.value, allow_posting: allowPosting.value, ...(parentID.value ? { parent_account_id: parentID.value } : {}) }); path = `/app/finance/accounts/${value.id}`; }
    else if (action.value === "account-edit" && selectedAccount.value) { await reviseFinanceAccount(accountID, selectedAccount.value, { name: name.value.trim(), code: code.value.trim(), description: description.value.trim(), allow_posting: allowPosting.value, ...(parentID.value ? { parent_account_id: parentID.value } : {}) }); }
    else if (action.value === "account-archive" && selectedAccount.value && ledgerValue) { await archiveFinanceAccount(accountID, selectedAccount.value); path = `/app/finance/ledgers/${ledgerValue.id}`; }
    else if (action.value === "entry-create" && ledgerValue) { const input = journalInput(); const value = await createFinanceEntry(accountID, ledgerValue.id, { ...input, currency: ledgerValue.currency }); path = `/app/finance/entries/${value.id}`; }
    else if (action.value === "entry-edit" && selectedEntry.value) { await reviseFinanceEntry(accountID, selectedEntry.value, journalInput()); }
    else if (action.value === "entry-post" && selectedEntry.value) {
      const current = selectedEntry.value;
      const combined = [...new Set([...(current.evidence ?? []), ...ids(evidence.value)])];
      if (combined.length !== (current.evidence ?? []).length) {
        selectedEntry.value = await reviseFinanceEntry(accountID, current, { entry_date: current.entry_date, description: current.description, reference: current.reference, lines: current.lines, evidence: combined });
      }
      await postFinanceEntry(accountID, selectedEntry.value);
    }
    else if (action.value === "entry-reverse" && selectedEntry.value) { const value = await reverseFinanceEntry(accountID, selectedEntry.value, { entry_date: toISO(dateValue.value), description: description.value.trim(), reference: reference.value.trim(), evidence: ids(evidence.value) }); path = `/app/finance/entries/${value.reversal.id}`; }
    else if (action.value === "reconciliation-create" && ledgerValue) { const value = await createFinanceReconciliation(accountID, ledgerValue.id, { posting_account_id: postingAccountID.value, as_of: toISO(dateValue.value), statement_balance: { currency: ledgerValue.currency, minor: toMinor(statementBalance.value) }, evidence: ids(evidence.value) }); path = `/app/finance/reconciliations/${value.id}`; }
    else if (action.value === "reconciliation-confirm" && selectedReconciliation.value) { await confirmFinanceReconciliation(accountID, selectedReconciliation.value); }
    modalOpen.value = false; announcement.value = `${title()} completed.`; if (path !== route.path) allowNextNavigation(); await navigate(path);
  } catch (cause) { if (cause instanceof APIProblem && cause.status === 409) { modalOpen.value = false; await load(); error.value = "This Finance record changed. Review its current version before trying again."; } else if (cause instanceof APIProblem && cause.status === 400 && action.value === "ledger-archive") error.value = "The ledger could not be archived. Archive its posting accounts and finish any draft entries first."; else if (cause instanceof APIProblem && cause.status === 400 && action.value === "account-archive") error.value = "The account could not be archived. Archive any child accounts and finish draft entries that use it first."; else error.value = cause instanceof APIProblem ? cause.message : "The Finance command could not be completed."; }
  finally { saving.value = false; }
}
watch(() => [session.selectedID, financePackage.value?.mode, route.path, entryState.value], () => void load(), { immediate: true });
</script>

<template>
  <section class="page finance-page">
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
    <p v-if="navigationNotice && !modalOpen" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <header class="page-heading page-heading--action"><div><h1>Finance</h1><p>Record income and expenses, review entries, and check account balances.</p></div><span v-if="available" class="state-badge">{{ writable ? "Package enabled" : "Read-only access" }}</span></header>
    <section v-if="!session.selectedID" class="queue-state"><h2>Select an Account</h2><p>Finance always belongs to one Account.</p></section>
    <section v-else-if="!available" class="queue-state"><h2>Finance is not included</h2><p>This Account's current package set does not expose Finance.</p><a href="/app/billing">Review Account billing</a></section>
    <template v-else>
      <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }}</p>
      <section class="finance-toolbar"><label>Active ledger<select :value="activeLedger?.id" :disabled="loading || (!activeLedger && ledgers.length === 0)" @change="selectLedger"><option v-if="ledgers.length === 0 && !activeLedger" value="">No ledgers</option><option v-if="activeLedger && !ledgers.some((item) => item.id === activeLedger?.id)" :value="activeLedger.id">{{ activeLedger.code }} · {{ activeLedger.name }}</option><option v-for="item in ledgers" :key="item.id" :value="item.id">{{ item.code }} · {{ item.name }}</option></select></label><div><IoButton v-if="ledgerCursor" kind="secondary" :disabled="loadingMore" @click="loadMore('ledgers')">More ledgers</IoButton><IoButton kind="secondary" @click="load">Refresh</IoButton><IoButton v-if="manageable && activeLedger?.state === 'active'" kind="secondary" @click="begin('ledger-edit')">Edit ledger</IoButton><IoButton v-if="manageable && activeLedger?.state === 'active'" kind="secondary" @click="begin('ledger-close')">Close period</IoButton><IoButton v-if="manageable && activeLedger?.state === 'active'" kind="secondary" @click="begin('ledger-archive')">Archive ledger</IoButton><IoButton v-if="manageable" @click="begin('ledger-create')">New ledger</IoButton></div></section>
      <div v-if="loading" class="queue-state" role="status">Loading Finance workspace…</div>
      <section v-else-if="ledgers.length === 0 && !activeLedger" class="queue-state"><h2>No ledgers yet</h2><p>Create a ledger to start recording your accounts.</p></section>
      <template v-else-if="activeLedger">
        <section class="finance-summary" aria-label="Ledger summary"><article><small>Income</small><strong>{{ money(summary?.income_minor) }}</strong><span>Posted</span></article><article><small>Expense</small><strong>{{ money(summary?.expense_minor) }}</strong><span>Posted</span></article><article><small>Net</small><strong>{{ money(summary?.net_minor) }}</strong><span>Income less expense</span></article><article><small>Drafts</small><strong>{{ summary?.draft_count ?? 0 }}</strong><span>Awaiting posting</span></article></section>
        <nav class="finance-tabs" aria-label="Finance workspace"><button v-for="value in (['accounts', 'journal', 'reconciliation'] as const)" :key="value" type="button" :aria-current="tab === value ? 'page' : undefined" @click="selectTab(value)">{{ value === 'accounts' ? 'Chart of accounts' : label(value) }}</button></nav>
        <section v-if="tab === 'accounts'" class="finance-workspace"><div class="finance-list-panel"><header><div><h2>Posting accounts</h2></div><IoButton v-if="manageable && activeLedger.state === 'active'" @click="begin('account-create')">Add account</IoButton></header><ol><li v-for="item in accounts" :key="item.account.id"><button type="button" @click="navigate(`/app/finance/accounts/${item.account.id}`)"><span><strong>{{ item.account.code }} · {{ item.account.name }}</strong><small>{{ label(item.account.type) }} · {{ label(item.account.normal_balance) }} normal</small></span><span><b>{{ money(item.balance_minor) }}</b><em>{{ label(item.account.state) }}</em></span></button></li></ol><p v-if="accounts.length === 0">No chart accounts have been added.</p><IoButton v-if="accountCursor" kind="secondary" :disabled="loadingMore" @click="loadMore('accounts')">Load more accounts</IoButton></div><aside class="finance-detail"><template v-if="selectedAccount"><h2>{{ selectedAccount.code }} · {{ selectedAccount.name }}</h2><p>{{ selectedAccount.description || "No description." }}</p><dl><div><dt>Type</dt><dd>{{ label(selectedAccount.type) }}</dd></div><div><dt>Normal balance</dt><dd>{{ label(selectedAccount.normal_balance) }}</dd></div><div><dt>Current balance</dt><dd>{{ money(accounts.find((item) => item.account.id === selectedAccount?.id)?.balance_minor) }}</dd></div><div><dt>Posting</dt><dd>{{ selectedAccount.allow_posting ? "Allowed" : "Summary only" }}</dd></div><div><dt>Version</dt><dd>{{ selectedAccount.version }}</dd></div></dl><div v-if="manageable && selectedAccount.state === 'active'" class="finance-detail-actions"><IoButton kind="secondary" @click="begin('account-edit')">Edit</IoButton><IoButton kind="secondary" @click="begin('account-archive')">Archive</IoButton></div></template><template v-else><h2>Select an account</h2><p>Choose an account to see its balance and details.</p></template></aside></section>
        <section v-else-if="tab === 'journal'" class="finance-workspace"><div class="finance-list-panel"><header><div><h2>Entries</h2></div><IoButton v-if="writable && activeLedger.state === 'active'" @click="begin('entry-create')">Draft entry</IoButton></header><label>Entry state<select v-model="entryState"><option value="">All states</option><option value="draft">Draft</option><option value="posted">Posted</option><option value="reversed">Reversed</option></select></label><ol><li v-for="item in entries" :key="item.id"><button type="button" @click="navigate(`/app/finance/entries/${item.id}`)"><span><strong>#{{ item.number }} · {{ item.description }}</strong><small>{{ date(item.entry_date) }} · {{ item.reference || "No reference" }}</small></span><span><b>{{ money(item.total_minor, item.currency) }}</b><em>{{ label(item.state) }}</em></span></button></li></ol><p v-if="entries.length === 0">No journal entries match this view.</p><IoButton v-if="entryCursor" kind="secondary" :disabled="loadingMore" @click="loadMore('journal')">Load more entries</IoButton></div><aside class="finance-detail"><template v-if="selectedEntry"><h2>#{{ selectedEntry.number }} · {{ selectedEntry.description }}</h2><p>{{ date(selectedEntry.entry_date) }} · {{ selectedEntry.reference || "No reference" }}</p><dl><div><dt>State</dt><dd>{{ label(selectedEntry.state) }}</dd></div><div><dt>Source</dt><dd>{{ label(selectedEntry.provenance.source) }}</dd></div><div><dt>Total</dt><dd>{{ money(selectedEntry.total_minor, selectedEntry.currency) }}</dd></div><div><dt>Evidence</dt><dd>{{ selectedEntry.evidence?.length ?? 0 }}</dd></div><div><dt>Version</dt><dd>{{ selectedEntry.version }}</dd></div></dl><div class="finance-lines-table"><div v-for="(line, index) in selectedEntry.lines" :key="index"><strong>{{ accountName(line.account_id) }}</strong><span>{{ line.memo || "No memo" }}</span><b>{{ line.debit_minor ? `Debit ${money(line.debit_minor, selectedEntry.currency)}` : `Credit ${money(line.credit_minor, selectedEntry.currency)}` }}</b></div></div><div v-if="selectedEntry.state === 'draft' && writable" class="finance-detail-actions"><IoButton kind="secondary" @click="begin('entry-edit')">Edit draft</IoButton><IoButton v-if="manageable" @click="begin('entry-post')">Post entry</IoButton></div><IoButton v-else-if="selectedEntry.state === 'posted' && manageable" @click="begin('entry-reverse')">Reverse entry</IoButton></template><template v-else><h2>Select an entry</h2><p>Choose an entry to see its details.</p></template></aside></section>
        <section v-else class="finance-workspace"><div class="finance-list-panel"><header><div><h2>Statement checks</h2></div><IoButton v-if="writable && activeLedger.state === 'active'" @click="begin('reconciliation-create')">Reconcile</IoButton></header><ol><li v-for="item in reconciliations" :key="item.id"><button type="button" @click="navigate(`/app/finance/reconciliations/${item.id}`)"><span><strong>{{ date(item.as_of) }}</strong><small>{{ accountName(item.posting_account_id) }}</small></span><span><b>{{ money(item.difference_minor, item.statement_balance.currency) }}</b><em>{{ label(item.state) }}</em></span></button></li></ol><p v-if="reconciliations.length === 0">No statement checks have been recorded.</p><IoButton v-if="reconciliationCursor" kind="secondary" :disabled="loadingMore" @click="loadMore('reconciliation')">Load more checks</IoButton></div><aside class="finance-detail"><template v-if="selectedReconciliation"><h2>{{ accountName(selectedReconciliation.posting_account_id) }}</h2><p>Statement position as of {{ date(selectedReconciliation.as_of) }}.</p><dl><div><dt>Statement</dt><dd>{{ money(selectedReconciliation.statement_balance.minor, selectedReconciliation.statement_balance.currency) }}</dd></div><div><dt>Ledger</dt><dd>{{ money(selectedReconciliation.ledger_balance.minor, selectedReconciliation.ledger_balance.currency) }}</dd></div><div><dt>Difference</dt><dd>{{ money(selectedReconciliation.difference_minor, selectedReconciliation.statement_balance.currency) }}</dd></div><div><dt>Evidence</dt><dd>{{ selectedReconciliation.evidence.length }}</dd></div><div><dt>State</dt><dd>{{ label(selectedReconciliation.state) }}</dd></div></dl><IoButton v-if="manageable && selectedReconciliation.state === 'proposed' && selectedReconciliation.difference_minor === 0" @click="begin('reconciliation-confirm')">Confirm reconciliation</IoButton></template><template v-else><h2>Select a check</h2><p>Choose a statement check to see any difference in balances.</p></template></aside></section>
      </template>
    </template>
    <div v-if="modalOpen" class="modal-backdrop">
      <form class="modal-card finance-modal" role="dialog" aria-modal="true" aria-labelledby="finance-modal-title" @submit.prevent="submit">
        <h2 id="finance-modal-title">{{ title() }}</h2>
        <template v-if="['ledger-create', 'ledger-edit'].includes(action)"><label>Name<input v-model="name" maxlength="160" required></label><label>Code<input v-model="code" maxlength="40" required></label><label v-if="action === 'ledger-create'">Currency<input v-model="currency" minlength="3" maxlength="3" pattern="[A-Za-z]{3}" required></label><label>Description<textarea v-model="description" maxlength="4000" rows="3"></textarea></label></template>
        <template v-else-if="action === 'ledger-close'"><p>You will not be able to post entries dated on or before this date.</p><label>Close through<input v-model="dateValue" type="date" required></label></template>
        <template v-else-if="['account-create', 'account-edit'].includes(action)"><label>Code<input v-model="code" maxlength="40" required></label><label v-if="action === 'account-create'">Type<select v-model="accountType"><option value="asset">Asset</option><option value="liability">Liability</option><option value="equity">Equity</option><option value="income">Income</option><option value="expense">Expense</option></select></label><label>Name<input v-model="name" maxlength="160" required></label><label>Parent account<select v-model="parentID"><option value="">No parent</option><option v-for="item in accounts.filter((candidate) => candidate.account.id !== selectedAccount?.id)" :key="item.account.id" :value="item.account.id">{{ item.account.code }} · {{ item.account.name }}</option></select></label><label>Description<textarea v-model="description" maxlength="4000" rows="3"></textarea></label><label class="confirmation"><input v-model="allowPosting" type="checkbox"><span>Allow journal postings</span></label></template>
        <template v-else-if="['entry-create', 'entry-edit'].includes(action)"><label>Entry date<input v-model="dateValue" type="date" required></label><label>Reference<input v-model="reference" maxlength="500"></label><label>Description<textarea v-model="description" maxlength="4000" rows="2" required></textarea></label><fieldset class="finance-line-editor"><legend>Balanced lines</legend><div v-for="(line, index) in lines" :key="index"><label>Account<select v-model="line.account_id" required><option value="">Choose account</option><option v-for="item in postingAccounts" :key="item.account.id" :value="item.account.id">{{ item.account.code }} · {{ item.account.name }}</option></select></label><label>Memo<input v-model="line.memo" maxlength="1000"></label><label>Debit<input v-model="line.debit" type="number" min="0" step="0.01"></label><label>Credit<input v-model="line.credit" type="number" min="0" step="0.01"></label><button v-if="lines.length > 2" type="button" @click="removeLine(index)">Remove line</button></div><IoButton type="button" kind="secondary" @click="addLine">Add line</IoButton><p :class="balanced ? 'referral-confirmed' : 'form-error'">Debit {{ money(lineTotals.debit) }} · Credit {{ money(lineTotals.credit) }}</p></fieldset></template>
        <template v-else-if="action === 'entry-reverse'"><p>A reversal offsets the original entry. Both entries stay in your history.</p><label>Entry date<input v-model="dateValue" type="date" required></label><label>Reference<input v-model="reference" maxlength="500"></label><label>Description<textarea v-model="description" maxlength="4000" rows="2" required></textarea></label></template>
        <template v-else-if="action === 'reconciliation-create'"><label>Posting account<select v-model="postingAccountID" required><option value="">Choose account</option><option v-for="item in postingAccounts" :key="item.account.id" :value="item.account.id">{{ item.account.code }} · {{ item.account.name }}</option></select></label><label>As of<input v-model="dateValue" type="date" required></label><label>Statement balance<input v-model="statementBalance" type="number" step="0.01" required></label></template>
        <p v-else>{{ action === 'entry-post' ? 'Posted entries cannot be edited. You can reverse an entry if you need to correct it.' : action === 'reconciliation-confirm' ? 'Confirm only when the statement and account balances match.' : action === 'ledger-archive' ? 'Archive all posting accounts and finish any draft entries first. Archived records remain in your history.' : 'Archive any child accounts and finish draft entries that use this account first. Archived records remain in your history.' }}</p>
        <div v-if="acceptsEvidence">
          <p v-if="savedEvidenceCount" class="form-note">{{ savedEvidenceCount }} supporting record{{ savedEvidenceCount === 1 ? '' : 's' }} already saved.</p>
          <label>Supporting note<textarea v-model="supportingNote" rows="3" minlength="3" maxlength="400" :required="needsSupportingNote" placeholder="Describe the receipt, statement, or reason supporting this entry."></textarea></label>
          <p class="form-note">Add a short reference to a receipt or statement, or explain why this entry is correct.</p>
        </div>
        <label v-if="requiresPhrase()">Type {{ phrase() }} to confirm<input v-model="confirmation" autocomplete="off" :pattern="phrase()" required></label><p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p><p v-if="error" class="form-error" role="alert">{{ error }}</p><div class="modal-actions"><IoButton type="button" kind="secondary" @click="modalOpen = false">Back</IoButton><IoButton type="submit" :disabled="saving || (requiresPhrase() && confirmation !== phrase()) || (['entry-create', 'entry-edit'].includes(action) && !balanced)">{{ saving ? "Saving…" : title() }}</IoButton></div>
      </form>
    </div>
  </section>
</template>
