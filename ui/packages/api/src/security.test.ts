import { afterEach, describe, expect, it, vi } from "vitest";
import { beginContactChange, completePasskeyReauthentication, revokeMCPGrant, revokeSession } from "./security";

afterEach(() => vi.unstubAllGlobals());
describe("Security client", () => {
  it("keeps verified-contact requests typed and same-origin", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ contact_change_id: "id", new_email: "new@example.com", expires_at: "2026-08-25T00:00:00Z", status: "verification_required" }), { status: 202, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher); await beginContactChange("new@example.com");
    expect(String(fetcher.mock.calls[0]?.[0])).toBe("/api/v1/contact-change-requests");
    expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body))).toEqual({ new_email: "new@example.com" });
  });
  it("uses bounded identifiers for passkey completion and revocations", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(null, { status: 204 })); vi.stubGlobal("fetch", fetcher);
    const credential = { id: "key", rawId: "cmF3", type: "public-key", response: { clientDataJSON: "YQ", authenticatorData: "Yg", signature: "Yw" } } as const;
    await completePasskeyReauthentication("ceremony/id", credential); await revokeSession("session/id"); await revokeMCPGrant("grant/id");
    expect(fetcher.mock.calls.map((call) => String(call[0]))).toEqual(["/api/v1/passkey-reauthentications/ceremony%2Fid/complete", "/api/v1/sessions/session%2Fid", "/api/v1/mcp-grants/grant%2Fid"]);
  });
});
