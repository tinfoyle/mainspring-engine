// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, FinanceJournalEntry, FinanceLedger, FinanceLedgerSummary } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";
import FinanceView from "./FinanceView.vue";

const api = vi.hoisted(() => ({
  captureOwnerKnowledgeEvidence: vi.fn(), listFinanceLedgers: vi.fn(), getFinanceLedger: vi.fn(), listFinanceAccounts: vi.fn(), listFinanceEntries: vi.fn(), listFinanceReconciliations: vi.fn(), getFinanceAccount: vi.fn(), getFinanceEntry: vi.fn(), getFinanceReconciliation: vi.fn(), createFinanceLedger: vi.fn(), reviseFinanceLedger: vi.fn(), closeFinancePeriod: vi.fn(), archiveFinanceLedger: vi.fn(), createFinanceAccount: vi.fn(), reviseFinanceAccount: vi.fn(), archiveFinanceAccount: vi.fn(), createFinanceEntry: vi.fn(), reviseFinanceEntry: vi.fn(), postFinanceEntry: vi.fn(), reverseFinanceEntry: vi.fn(), createFinanceReconciliation: vi.fn(), confirmFinanceReconciliation: vi.fn()
}));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));
const ledger = { id: "20000000-0000-4000-8000-000000000002", account_id: "10000000-0000-4000-8000-000000000001", name: "Operating", code: "OPS", description: "Primary book", currency: "USD", state: "active", version: 3, created_by: { kind: "user", id: "user" }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" } satisfies FinanceLedger;
const summary = { ...ledger, account_count: 2, draft_count: 1, income_minor: 5000, expense_minor: 2000, net_minor: 3000 } satisfies FinanceLedgerSummary;
const entry = { id: "30000000-0000-4000-8000-000000000003", account_id: ledger.account_id, ledger_id: ledger.id, number: 8, entry_date: "2026-08-24T00:00:00Z", description: "Monthly close", reference: "CLOSE-8", currency: "USD", total_minor: 5000, state: "draft", version: 5, lines: [{ account_id: "cash", memo: "", debit_minor: 5000, credit_minor: 0 }, { account_id: "income", memo: "", debit_minor: 0, credit_minor: 5000 }], evidence: [], provenance: { source: "manual" }, created_by: { kind: "user", id: "user" }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" } satisfies FinanceJournalEntry;
const account = { account_id: ledger.account_id, account_type: "paid", account_state: "active", account_version: 2, cell_id: "cell-a", display_name: "Northstar", placement_generation: 1, role: "owner", slug: "northstar", owner_enrollment_required: false, entitlements: { account_id: ledger.account_id, catalog_version: 1, evaluated_at: "2026-08-24T00:00:00Z", version: 1, packages: [{ code: "finance", mode: "enabled", sources: ["subscription"], version: 1 }] } } satisfies AccountChoice;
beforeEach(() => { setActivePinia(createPinia()); Object.values(api).forEach((mock) => mock.mockReset()); api.listFinanceLedgers.mockResolvedValue({ items: [summary] }); api.getFinanceLedger.mockResolvedValue(ledger); api.listFinanceAccounts.mockResolvedValue({ items: [] }); api.listFinanceEntries.mockResolvedValue({ items: [entry] }); api.listFinanceReconciliations.mockResolvedValue({ items: [] }); api.getFinanceEntry.mockResolvedValue(entry); api.postFinanceEntry.mockResolvedValue({ ...entry, state: "posted", version: 6 }); const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; });
function testRouter() { return createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/finance", component: FinanceView }, { path: "/app/finance/entries/:entryID", component: FinanceView }] }); }
describe("Finance workspace", () => {
  it("saves supporting information before posting and retries without duplicating it", async () => {
    const revised = { ...entry, evidence: ["audit-evidence"], version: 6 };
    api.captureOwnerKnowledgeEvidence.mockResolvedValue({ id: "audit-evidence" });
    api.reviseFinanceEntry.mockResolvedValue(revised);
    api.postFinanceEntry.mockRejectedValueOnce(new Error("Temporary connection failure")).mockResolvedValueOnce({ ...revised, state: "posted", version: 7 });
    const router = testRouter(); await router.push(`/app/finance/entries/${entry.id}`); await router.isReady();
    const wrapper = mount(FinanceView, { global: { plugins: [router] } }); await flushPromises();
    expect(wrapper.text()).toContain("#8 · Monthly close"); await expectNoAxeViolations(wrapper.element);
    await wrapper.findAll("button").find((item) => item.text() === "Post entry")!.trigger("click");
    await wrapper.get("[role=dialog] input").setValue("POST");
    await wrapper.get("[role=dialog]").trigger("submit"); await flushPromises();
    expect(api.postFinanceEntry).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain("Add a supporting note");
    await wrapper.get("[role=dialog] textarea").setValue("Receipt 12 matches these amounts.");
    await wrapper.get("[role=dialog]").trigger("submit"); await flushPromises();
    expect(api.captureOwnerKnowledgeEvidence).toHaveBeenCalledWith(account.account_id, "Receipt 12 matches these amounts.", "Finance: Receipt 12 matches these amounts.");
    expect(api.reviseFinanceEntry).toHaveBeenCalledWith(account.account_id, entry, expect.objectContaining({ evidence: ["audit-evidence"], lines: entry.lines }));
    expect(api.postFinanceEntry).toHaveBeenCalledWith(account.account_id, revised);
    expect(wrapper.text()).toContain("The Finance command could not be completed.");
    await wrapper.get("[role=dialog]").trigger("submit"); await flushPromises();
    expect(api.captureOwnerKnowledgeEvidence).toHaveBeenCalledTimes(1);
    expect(api.reviseFinanceEntry).toHaveBeenCalledTimes(1);
    expect(api.postFinanceEntry).toHaveBeenCalledTimes(2);
    expect(wrapper.find("[role=dialog]").exists()).toBe(false);
  });
  it("keeps a read-only package free of mutation controls", async () => { const session = useSessionStore(); session.accounts = [{ ...account, entitlements: { ...account.entitlements, packages: [{ ...account.entitlements.packages[0]!, mode: "read_only" }] } }]; const router = testRouter(); await router.push("/app/finance"); await router.isReady(); const wrapper = mount(FinanceView, { global: { plugins: [router] } }); await flushPromises(); expect(wrapper.text()).toContain("Read-only access"); expect(wrapper.text()).not.toContain("New ledger"); expect(wrapper.text()).not.toContain("Draft entry"); });
});
