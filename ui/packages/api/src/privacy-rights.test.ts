import { afterEach, describe, expect, it, vi } from "vitest";
import { cancelPrivacyRightsRequest, listPrivacyRightsRequests, submitPrivacyRightsRequest } from "./privacy-rights";

afterEach(() => vi.unstubAllGlobals());

describe("privacy rights client", () => {
  it("uses the authenticated identity-wide request boundary", async () => {
    const responses = [
      new Response(JSON.stringify({ requests: [] }), { status: 200, headers: { "Content-Type": "application/json" } }),
      new Response(JSON.stringify({ request_id: "10000000-0000-4000-8000-000000000001" }), { status: 201, headers: { "Content-Type": "application/json" } }),
      new Response(JSON.stringify({ request_id: "10000000-0000-4000-8000-000000000001" }), { status: 200, headers: { "Content-Type": "application/json" } })
    ];
    const fetchMock = vi.fn().mockImplementation(async () => responses.shift());
    vi.stubGlobal("fetch", fetchMock);

    await listPrivacyRightsRequests();
    await submitPrivacyRightsRequest({ kind: "erasure", scope: "affiliate" });
    await cancelPrivacyRightsRequest("10000000-0000-4000-8000-000000000001");

    expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
      "/api/v1/privacy/rights-requests",
      "/api/v1/privacy/rights-requests",
      "/api/v1/privacy/rights-requests/10000000-0000-4000-8000-000000000001"
    ]);
    const [, submitInit] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(submitInit.method).toBe("POST");
    expect(submitInit.body).toBe(JSON.stringify({ kind: "erasure", scope: "affiliate" }));
  });
});
