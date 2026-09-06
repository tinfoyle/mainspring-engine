import { requestJSON } from "./client";
import type { KnowledgeDocumentCitation, KnowledgeDocument, KnowledgeDocumentDetail, KnowledgeDocumentPage, KnowledgeDocumentRetrieval, KnowledgeSensitivity } from "./generated/api-types";

function base(accountID: string): string { return "/api/v1/accounts/" + encodeURIComponent(accountID) + "/knowledge"; }
export function listDocuments(accountID: string, options: { cursor?: string; title?: string } = {}): Promise<KnowledgeDocumentPage> {
  const query = new URLSearchParams({ limit: "25" });
  if (options.cursor) query.set("cursor", options.cursor);
  if (options.title) query.set("title_prefix", options.title);
  return requestJSON(base(accountID) + "/documents?" + query);
}
export function getDocument(accountID: string, id: string): Promise<KnowledgeDocumentDetail> {
  return requestJSON(base(accountID) + "/documents/" + encodeURIComponent(id));
}
export function documentPassages(accountID: string, id: string, revision: string, after?: number): Promise<KnowledgeDocumentRetrieval> {
  return requestJSON(base(accountID) + "/retrieval", { method: "POST", body: JSON.stringify({
    query: "", document_id: id, revision_id: revision, limit: 20,
    ...(after !== undefined ? { after_chunk_index: after } : {})
  }) });
}
export function searchDocuments(accountID: string, query: string): Promise<KnowledgeDocumentRetrieval> {
  return requestJSON(base(accountID) + "/retrieval", { method: "POST", body: JSON.stringify({ query, limit: 20 }) });
}
export function uploadDocument(accountID: string, file: File, title: string, sensitivity: KnowledgeSensitivity, operationID: string): Promise<KnowledgeDocumentDetail> {
  const form = new FormData(); form.set("file", file); form.set("title", title); form.set("sensitivity", sensitivity);
  return requestJSON(base(accountID) + "/documents", { method: "POST", headers: { "Idempotency-Key": operationID }, body: form });
}
export function publishDocument(accountID: string, detail: KnowledgeDocumentDetail, operationID: string): Promise<KnowledgeDocument> {
  return requestJSON(base(accountID) + "/documents/" + encodeURIComponent(detail.document.id) + "/publications", {
    method: "POST", headers: { "Idempotency-Key": operationID, "If-Match": 'W/"' + detail.document.version + '"' },
    body: JSON.stringify({ revision_id: detail.latest_revision.id })
  });
}
export function deleteDocument(accountID: string, document: KnowledgeDocument, operationID: string): Promise<KnowledgeDocument> {
  return requestJSON(base(accountID) + "/documents/" + encodeURIComponent(document.id), {
    method: "DELETE", headers: { "Idempotency-Key": operationID, "If-Match": 'W/"' + document.version + '"' }
  });
}

export function getDocumentCitation(accountID: string, documentID: string, revisionID: string, chunkID: string): Promise<KnowledgeDocumentCitation> {
  return requestJSON(base(accountID) + "/documents/" + encodeURIComponent(documentID) + "/revisions/" + encodeURIComponent(revisionID) + "/chunks/" + encodeURIComponent(chunkID));
}
