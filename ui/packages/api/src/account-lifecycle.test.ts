import { afterEach, describe, expect, it, vi } from "vitest";
import type { AccountClosure, AccountExport } from "./generated/api-types";
import { cancelAccountClosure, cancelAccountExport, downloadAccountExport, requestAccountClosure } from "./account-lifecycle";

afterEach(() => vi.unstubAllGlobals());
const exported = { id: "export/id", account_id: "account", requested_by: "user", cell_id: "cell", placement_generation: 1, account_version: 2, state: "queued", version: 4, attempt_count: 0, requested_at: "2026-08-24T00:00:00Z", expires_at: "2026-08-31T00:00:00Z" } satisfies AccountExport;
const closure = { request_id: "request", account_id: "account", account_name: "Northstar", account_state: "closing", account_version: 3, state: "cooling_off", reason: "Closing", requested_at: "2026-08-24T00:00:00Z", execute_after: "2026-08-31T00:00:00Z" } satisfies AccountClosure;
describe("Account portability and lifecycle client", () => {
  it("binds export cancellation and lifecycle commands to current versions", async () => {
    const fetcher = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(exported), { status: 200, headers: { "content-type": "application/json" } }))); vi.stubGlobal("fetch", fetcher);
    await cancelAccountExport("account", exported); await requestAccountClosure("account", 7, "Operations ended."); await cancelAccountClosure(closure, "Operations resumed.");
    expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body))).toEqual({ expected_version: 4 });
    expect(JSON.parse(String(fetcher.mock.calls[1]?.[1]?.body))).toEqual({ expected_account_version: 7, reason: "Operations ended." });
    expect(JSON.parse(String(fetcher.mock.calls[2]?.[1]?.body))).toEqual({ expected_account_version: 3, reason: "Operations resumed." });
  });
  it("streams export bytes with a header capability and no session cookie", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(new Blob(["zip"]), { status: 200, headers: { "content-type": "application/zip" } })); vi.stubGlobal("fetch", fetcher);
    await downloadAccountExport("export/id", "secret-token"); const init = fetcher.mock.calls[0]?.[1];
    expect(String(fetcher.mock.calls[0]?.[0])).toBe("/api/v1/account-exports/export%2Fid/artifact"); expect(init?.credentials).toBe("omit"); expect((init?.headers as Record<string, string>).Authorization).toBe("SPYGLASS-ACCOUNT-EXPORT secret-token");
  });
});
