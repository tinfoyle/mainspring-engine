// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { documentPassages, publishDocument, uploadDocument } from "./documents";
import type { KnowledgeDocumentDetail } from "./generated/api-types";
beforeEach(() => sessionStorage.clear());
afterEach(() => vi.unstubAllGlobals());
describe("workspace document client", () => {
  it("pins published preview pages to the document revision", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [] }), { headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    await documentPassages("account", "document", "revision", 19);
    expect(JSON.parse(fetcher.mock.calls[0]![1].body)).toEqual({ query: "", document_id: "document", revision_id: "revision", limit: 20, after_chunk_index: 19 });
  });
  it("uploads as multipart with one caller-owned operation identity", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ document: {} }), { status: 201, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    await uploadDocument("account", new File(["supplier requirements"], "requirements.txt", { type: "text/plain" }), "Requirements", "internal", "operation");
    const request = fetcher.mock.calls[0]![1];
    expect(request.body).toBeInstanceOf(FormData);
    expect(request.body.get("title")).toBe("Requirements");
    expect(new Headers(request.headers).has("Content-Type")).toBe(false);
    expect(new Headers(request.headers).get("Idempotency-Key")).toBe("operation");
  });
  it("publishes only the reviewed document version and revision", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({}), { headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    await publishDocument("account", { document: { id: "document", version: 4 }, latest_revision: { id: "revision" } } as KnowledgeDocumentDetail, "operation");
    const request = fetcher.mock.calls[0]![1];
    expect(new Headers(request.headers).get("If-Match")).toBe('W/"4"');
    expect(JSON.parse(request.body)).toEqual({ revision_id: "revision" });
  });
});
describe("agent send recovery across browser reload", () => {
  it("retains an unconfirmed operation identity when the client is reloaded", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ detail: "Unconfirmed" }), { status: 503, headers: { "content-type": "application/problem+json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: "run" }), { status: 201, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    const input = { subject: "Workspace retry", prompt: "Review my task.", mode: "selected" as const, persona_ids: ["agent"] };
    const first = await import("./agents");
    await expect(first.startAgentRun("account", "room", input)).rejects.toThrow("Unconfirmed");
    const original = new Headers(fetcher.mock.calls[0]![1].headers).get("Idempotency-Key");
    vi.resetModules();
    const reloaded = await import("./agents");
    await reloaded.startAgentRun("account", "room", input);
    expect(new Headers(fetcher.mock.calls[1]![1].headers).get("Idempotency-Key")).toBe(original);
    expect(sessionStorage.getItem("spyglass.agent-operations")).toBe("{}");
  });
});
