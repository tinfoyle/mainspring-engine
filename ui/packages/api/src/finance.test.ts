import { afterEach, describe, expect, it, vi } from "vitest";
import type { FinanceJournalEntry, FinanceLedger } from "./generated/api-types";
import { listFinanceEntries, postFinanceEntry, reviseFinanceLedger } from "./finance";

afterEach(() => vi.unstubAllGlobals());
const ledger = { id: "ledger/id", account_id: "account", name: "Main", code: "MAIN", description: "", currency: "USD", state: "active", version: 4, created_by: { kind: "user", id: "user" }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" } satisfies FinanceLedger;
const entry = { id: "entry/id", account_id: "account", ledger_id: ledger.id, number: 1, entry_date: "2026-08-24T00:00:00Z", description: "Entry", reference: "", currency: "USD", total_minor: 100, state: "draft", version: 7, lines: [], evidence: [], provenance: { source: "manual" }, created_by: { kind: "user", id: "user" }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" } satisfies FinanceJournalEntry;
describe("Finance client", () => {
  it("preserves opaque cursors and binds mutations to durable versions", async () => {
    const fetcher = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(ledger), { status: 200, headers: { "content-type": "application/json" } }))); vi.stubGlobal("fetch", fetcher);
    await listFinanceEntries("account", ledger.id, "draft", "opaque+/="); await reviseFinanceLedger("account", ledger, { name: "Main", code: "MAIN", description: "Updated" }); await postFinanceEntry("account", entry);
    expect(String(fetcher.mock.calls[0]?.[0])).toContain("cursor=opaque%2B%2F%3D");
    expect(new Headers(fetcher.mock.calls[1]?.[1]?.headers).get("If-Match")).toBe('W/"4"');
    expect(new Headers(fetcher.mock.calls[2]?.[1]?.headers).get("If-Match")).toBe('W/"7"');
    expect(new Headers(fetcher.mock.calls[1]?.[1]?.headers).get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
  });
  it("reuses one command identity across an exact failed retry", async () => {
    const fetcher = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ title: "Unavailable" }), { status: 503, headers: { "content-type": "application/problem+json" } }))); vi.stubGlobal("fetch", fetcher);
    const input = { name: "Main", code: "MAIN", description: "Updated" };
    await expect(reviseFinanceLedger("account", ledger, input)).rejects.toThrow();
    await expect(reviseFinanceLedger("account", ledger, input)).rejects.toThrow();
    const first = new Headers(fetcher.mock.calls[0]?.[1]?.headers).get("Idempotency-Key"); const second = new Headers(fetcher.mock.calls[1]?.[1]?.headers).get("Idempotency-Key");
    expect(first).toBe(second);
  });
});
