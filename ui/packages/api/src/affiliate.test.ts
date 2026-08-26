import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadAffiliateDataExport, enrollAffiliate, getAffiliateProgram, getAffiliateStatement, replaceAffiliateCode } from "./affiliate";

afterEach(() => vi.unstubAllGlobals());

describe("Affiliate client", () => {
  it("uses the authenticated identity boundary without an Account path", async () => {
    const fetchMock = vi.fn().mockImplementation(async () => new Response(JSON.stringify({ enrollment_open: false }), {
      status: 200, headers: { "Content-Type": "application/json" }
    }));
    vi.stubGlobal("fetch", fetchMock);
    await getAffiliateProgram();
    await getAffiliateStatement();
    await downloadAffiliateDataExport();
    expect(fetchMock.mock.calls.map((call) => call[0])).toEqual(["/api/v1/affiliate", "/api/v1/affiliate/statement", "/api/v1/affiliate/data-export"]);
    expect((fetchMock.mock.calls[2]?.[1] as RequestInit).credentials).toBe("same-origin");
  });

  it("accepts only the current terms and optional owned settlement Account", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ enrollment_open: true }), {
      status: 201, headers: { "Content-Type": "application/json" }
    }));
    vi.stubGlobal("fetch", fetchMock);
    await enrollAffiliate({ accepted_terms_version: 2, settlement_account_id: "10000000-0000-4000-8000-000000000001" });
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ accepted_terms_version: 2, settlement_account_id: "10000000-0000-4000-8000-000000000001" }));
  });

  it("replaces a code only against the exact enrollment version", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ enrollment_open: true }), {
      status: 200, headers: { "Content-Type": "application/json" }
    }));
    vi.stubGlobal("fetch", fetchMock);
    await replaceAffiliateCode({ expected_version: 4 });
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/affiliate/code-replacements");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ expected_version: 4 }));
  });
});
