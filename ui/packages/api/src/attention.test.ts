import { afterEach, describe, expect, it, vi } from "vitest";
import type { WorkReview } from "./generated/api-types";
import { decideWorkReview, listAttentionQueue } from "./attention";

afterEach(() => vi.unstubAllGlobals());

describe("Attention client", () => {
  it("loads only the entitled, assigned open queues", async () => {
    const fetcher = vi.fn().mockImplementation((path: string) => Promise.resolve(new Response(JSON.stringify({
      items: [],
      ...(String(path).includes("information-requests") && !String(path).includes("cursor=") ? { next_cursor: "opaque-next-page" } : {})
    }), { status: 200, headers: { "content-type": "application/json" } })));
    vi.stubGlobal("fetch", fetcher);

    await listAttentionQueue("10000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000002", {
      work: true, workWritable: true, approvals: false, approvalsWritable: false
    });

    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(fetcher.mock.calls.map((call) => String(call[0]))).toEqual(expect.arrayContaining([
      expect.stringContaining("information-requests?state=open"),
      expect.stringContaining("cursor=opaque-next-page"),
      expect.stringContaining("reviewer_id=20000000-0000-4000-8000-000000000002")
    ]));
  });

  it("keeps the idempotency key across a retry and binds the current version", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ detail: "Try again" }), {
        status: 503, headers: { "content-type": "application/problem+json" }
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ state: "approved" }), {
        status: 200, headers: { "content-type": "application/json" }
      }));
    vi.stubGlobal("fetch", fetcher);
    const review = { id: "30000000-0000-4000-8000-000000000003", version: 7 } as WorkReview;
    const input = { decision: "approve" as const, reason: "The proposal is ready." };

    await expect(decideWorkReview("10000000-0000-4000-8000-000000000001", review, input)).rejects.toThrow("Try again");
    await decideWorkReview("10000000-0000-4000-8000-000000000001", review, input);

    const first = new Headers(fetcher.mock.calls[0]?.[1]?.headers);
    const second = new Headers(fetcher.mock.calls[1]?.[1]?.headers);
    expect(first.get("Idempotency-Key")).toBeTruthy();
    expect(second.get("Idempotency-Key")).toBe(first.get("Idempotency-Key"));
    expect(first.get("If-Match")).toBe('W/"7"');
  });
});
