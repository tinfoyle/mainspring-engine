# ADR-0011: Keep document ingestion behind the tenant RAG service

Status: Accepted

## Context

Owners need to upload and review the business documents their personas may retrieve. The tenant web runtime could write directly to the shared tenant database, but doing so would split ingestion rules between the boardroom and RAG services and make later extraction, embedding, and object-storage work harder to isolate.

## Decision

The tenant web runtime exposes the authenticated document-library experience, validates the browser upload, and calls the tenant RAG service over the private network using the tenant ID and RAG credential. The RAG service remains the sole application boundary for document creation, source retrieval, listing, chunking, and search.

The first ingestion pipeline accepts UTF-8 text-native files up to 2 MB: TXT, Markdown, CSV/TSV, JSON, XML, HTML, YAML, and LOG. It stores the exact source content on the document row and creates independent overlapping search chunks. This allows the user-facing detail view to remain faithful instead of reconstructing a document from lossy retrieval chunks.

The tenant runtime never gives RAG credentials to the browser. Uploads require a tenant session, completed onboarding, same-origin validation, and CSRF verification. Document views are read-only, and persona access remains controlled by `documents.read` capability grants.

## Consequences

- Listing, viewing, ingestion, and retrieval use one tenant-scoped service boundary.
- Exact source content can be displayed while chunking and embedding strategies change.
- The MVP has a clear and testable upload limit and file-type allowlist.
- The RAG service and tenant runtime must both be available for uploads and document views.
- Source text stored in PostgreSQL is acceptable for the MVP limit but is not the final binary-storage design.

## Deferred work

- PDF and Word extraction in an isolated ingestion worker
- Tenant object storage and malware scanning for original binary files
- Replacement, version history, deletion, retention, and legal holds
- OCR for scanned documents and images
