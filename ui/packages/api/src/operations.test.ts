import { afterEach, describe, expect, it, vi } from "vitest";
import { createOperationsSupportGrant, openOperationsSupportView, operationsAnalyticsReport, operationsLookup } from "./operations";

afterEach(() => vi.unstubAllGlobals());

describe("operations API client", () => {
  it("uses only the isolated operations routes and exact audited bodies", async () => {
    const calls: Array<[string, RequestInit]> = [];
    vi.stubGlobal("fetch", vi.fn(async (path: string, init: RequestInit) => {
      calls.push([path, init]);
      return new Response(JSON.stringify(path.includes("views") ? { mode: "read_only_support_view", staff: {}, view: {} } : path.includes("lookups") ? { results: [] } : path.includes("analytics") ? { rows: [] } : { grant: { id: "grant" } }), { status: 200, headers: { "content-type": "application/json" } });
    }));
    await operationsLookup({ kind: "email", value: "person@example.com", ticket: "SUP-1", reason: "Customer requested assistance." });
    await createOperationsSupportGrant({ target_user_id: "user", account_id: "account", lifetime_seconds: 900, ticket: "SUP-1", reason: "Customer requested assistance." });
    await openOperationsSupportView("grant/id", { ticket: "SUP-1", reason: "Inspect the customer-safe projection." });
    await operationsAnalyticsReport({ from: "2026-08-01T00:00:00Z", to: "2026-08-02T00:00:00Z", bucket: "day", dimension: "none", minimum_cohort: 5, ticket: "AN-1", reason: "Review aggregate launch behavior." });

    expect(calls.map(([path]) => path)).toEqual([
      "/api/operations/v1/lookups",
      "/api/operations/v1/support-grants",
      "/api/operations/v1/support-grants/grant%2Fid/views",
      "/api/operations/v1/analytics/reports"
    ]);
    expect(calls.every(([, init]) => init.method === "POST")).toBe(true);
    expect(calls.every(([, init]) => String(init.body).includes("reason"))).toBe(true);
  });
});

