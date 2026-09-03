import { afterEach, describe, expect, it, vi } from "vitest";
import type { KnowledgeClaim } from "./generated/api-types";
import { captureOwnerKnowledgeEvidence, captureOwnerKnowledgeFact, decideKnowledgeClaim, listProposedKnowledgeClaims } from "./knowledge";

afterEach(() => vi.unstubAllGlobals());

describe("Knowledge client", () => {
  it("drains opaque claim cursors through a bounded client", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [], next_cursor: "opaque-page" }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [] }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    await listProposedKnowledgeClaims("10000000-0000-4000-8000-000000000001");
    expect(String(fetcher.mock.calls[1]?.[0])).toContain("cursor=opaque-page");
  });

  it("preserves decision idempotency and current claim version across retry", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ detail: "Retry" }), { status: 503, headers: { "content-type": "application/problem+json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ claim: { state: "accepted" } }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    const claim = { id: "20000000-0000-4000-8000-000000000002", version: 6 } as KnowledgeClaim;
    const input = { accept: true, reason: "The cited evidence supports this value." };
    await expect(decideKnowledgeClaim("10000000-0000-4000-8000-000000000001", claim, input)).rejects.toThrow("Retry");
    await decideKnowledgeClaim("10000000-0000-4000-8000-000000000001", claim, input);
    const first = new Headers(fetcher.mock.calls[0]?.[1]?.headers);
    const second = new Headers(fetcher.mock.calls[1]?.[1]?.headers);
    expect(first.get("Idempotency-Key")).toBe(second.get("Idempotency-Key"));
    expect(first.get("If-Match")).toBe('W/"6"');
  });

  it("turns one owner-confirmed answer into evidence, an accepted claim and a Fact", async () => {
    const evidence = { id: "30000000-0000-4000-8000-000000000003" };
    const claim = { id: "40000000-0000-4000-8000-000000000004", version: 1 };
    const fact = { id: "50000000-0000-4000-8000-000000000005", key: "organization.legal_name", revision: 1 };
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(evidence), { status: 201, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify(claim), { status: 201, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ claim: { ...claim, version: 2 }, fact }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    await expect(captureOwnerKnowledgeFact("account", "organization.legal_name", "Northstar Studio LLC", "baseline/assessment/legal-name")).resolves.toMatchObject(fact);
    expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body))).toMatchObject({ source_kind: "owner_statement", content_sha256: expect.stringMatching(/^[0-9a-f]{64}$/) });
    expect(JSON.parse(String(fetcher.mock.calls[1]?.[1]?.body))).toMatchObject({ key: "organization.legal_name", value: "Northstar Studio LLC", confidence: 1000, citations: [{ evidence_id: evidence.id }] });
    expect(new Headers(fetcher.mock.calls[2]?.[1]?.headers).get("If-Match")).toBe('W/"1"');
  });

  it("records a plain-language Baseline review as attributable owner evidence", async () => {
    const evidence = { id: "60000000-0000-4000-8000-000000000006", source_kind: "owner_statement" };
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(evidence), { status: 201, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);

    await expect(captureOwnerKnowledgeEvidence("account", "We track complaints in the service log.", "baseline/assessment/customer_feedback")).resolves.toMatchObject(evidence);

    const request = JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body));
    expect(request).toMatchObject({ source_kind: "owner_statement", source_reference: "baseline/assessment/customer_feedback", content_sha256: expect.stringMatching(/^[0-9a-f]{64}$/) });
    expect(new Headers(fetcher.mock.calls[0]?.[1]?.headers).get("Idempotency-Key")).toBeTruthy();
  });
});
