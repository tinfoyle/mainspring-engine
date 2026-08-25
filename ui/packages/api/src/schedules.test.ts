import { afterEach, describe, expect, it, vi } from "vitest";
import type { Schedule } from "./generated/api-types";
import { listSchedules, reviseSchedule } from "./schedules";

afterEach(() => vi.unstubAllGlobals());

const schedule = {
  id: "20000000-0000-4000-8000-000000000002",
  account_id: "10000000-0000-4000-8000-000000000001",
  name: "Monday launch review",
  timezone: "America/New_York",
  recurrence: { frequency: "weekly", weekdays: [1], local_hour: 9, local_minute: 30, gap_policy: "next_valid", overlap_policy: "first" },
  missed_run_policy: "catch_up_one",
  template: { boardroom_id: "30000000-0000-4000-8000-000000000003", mode: "selected", persona_ids: ["40000000-0000-4000-8000-000000000004"], subject: "Launch review", prompt: "Review launch readiness.", work_item_ids: null, knowledge_fact_ids: null, knowledge_document_ids: null, baseline_assessment_ids: null },
  state: "active", next_run_at: "2026-08-31T13:30:00Z", version: 4,
  created_by: "50000000-0000-4000-8000-000000000005", created_at: "2026-08-24T20:00:00Z", updated_at: "2026-08-24T20:00:00Z"
} satisfies Schedule;

describe("Schedules client", () => {
  it("passes opaque pagination cursors without interpreting them", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [], next_cursor: "opaque+/=" }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    await listSchedules(schedule.account_id, "opaque+/=", 12);
    expect(String(fetcher.mock.calls[0]?.[0])).toContain("limit=12&cursor=opaque%2B%2F%3D");
  });

  it("keeps the exact revision idempotency key across a failed retry", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ detail: "Retry" }), { status: 503, headers: { "content-type": "application/problem+json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify(schedule), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    const input = { name: schedule.name, timezone: schedule.timezone, recurrence: schedule.recurrence, missed_run_policy: schedule.missed_run_policy, template: schedule.template, reason: "Keep the weekly review current." };
    await expect(reviseSchedule(schedule.account_id, schedule, input)).rejects.toThrow("Retry");
    await reviseSchedule(schedule.account_id, schedule, input);
    const first = new Headers(fetcher.mock.calls[0]?.[1]?.headers);
    const second = new Headers(fetcher.mock.calls[1]?.[1]?.headers);
    expect(first.get("Idempotency-Key")).toBeTruthy();
    expect(second.get("Idempotency-Key")).toBe(first.get("Idempotency-Key"));
    expect(JSON.parse(String(fetcher.mock.calls[1]?.[1]?.body))).toMatchObject({ expected_version: 4 });
  });
});
