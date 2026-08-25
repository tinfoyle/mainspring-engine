import { afterEach, describe, expect, it, vi } from "vitest";
import type { BaselineAssessment } from "./generated/api-types";
import { answerBaseline, getCurrentBaseline, listBaselineSourceGrants, startBaseline } from "./baseline";

afterEach(() => vi.unstubAllGlobals());
const assessment = { id: "assessment/id", account_id: "account", catalog_version: "catalog", scope_policy_version: "scope", state: "interview", answers: [], requirements: [], created_by_user_id: "user", version: 4, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" } satisfies BaselineAssessment;

describe("Baseline client", () => {
  it("discovers current state and binds versioned commands", async () => {
    const fetcher = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(assessment), { status: 200, headers: { "content-type": "application/json" } }))); vi.stubGlobal("fetch", fetcher);
    await getCurrentBaseline("account/id"); await startBaseline("account/id");
    await answerBaseline("account/id", assessment, { question_key: "organization.legal_name", kind: "unknown", reason: "Still confirming registration" });
    await listBaselineSourceGrants("account/id", assessment.id, "opaque+/=");
    expect(fetcher.mock.calls[0]?.[0]).toBe("/api/v1/accounts/account%2Fid/baseline-assessments/current");
    expect(new Headers(fetcher.mock.calls[1]?.[1]?.headers).get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
    expect(new Headers(fetcher.mock.calls[2]?.[1]?.headers).get("If-Match")).toBe('W/"4"');
    expect(String(fetcher.mock.calls[3]?.[0])).toContain("cursor=opaque%2B%2F%3D");
  });

  it("reuses one identity for an exact failed retry", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ title: "Unavailable" }), { status: 503, headers: { "content-type": "application/problem+json" } })); vi.stubGlobal("fetch", fetcher);
    await expect(startBaseline("account")).rejects.toThrow(); await expect(startBaseline("account")).rejects.toThrow();
    expect(new Headers(fetcher.mock.calls[0]?.[1]?.headers).get("Idempotency-Key")).toBe(new Headers(fetcher.mock.calls[1]?.[1]?.headers).get("Idempotency-Key"));
  });
});
