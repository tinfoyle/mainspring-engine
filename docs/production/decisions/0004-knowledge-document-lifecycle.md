# ADR-0004: Account-isolated Knowledge document lifecycle

- Status: Accepted
- Date: 2026-08-21
- Owners: Knowledge, platform operations

## Context

The prototype accepted small text-native files directly into PostgreSQL and later added in-process PDF/DOCX extraction. That preserved useful upload and retrieval behavior, but it did not provide a production boundary for hostile input, binary objects, immutable source revisions, retention, Account movement, or erasure. Historical citations must continue to resolve after a document changes, and neither an unscanned object nor a failed extraction may enter retrieval.

The final deployment targets are local Docker in `ubunturojo`, the Hostinger stage VPS under Docker, and a vanilla shared LKE production cluster. A storage contract must work in all three without making the application depend on a preview-hosting provider or on a single Kubernetes volume.

## Decision

Knowledge owns documents, immutable source revisions, extracted bodies, chunks, citation identities, and processing history. PostgreSQL stores Account-scoped metadata and state; source binaries and extracted bodies live behind a private S3-compatible object port. Objects are never public and are never addressed by a caller-supplied key.

The environment mapping is:

| Environment | Object implementation | Processing dependencies |
|---|---|---|
| Local `ubunturojo` | containerized MinIO with disposable or named Docker volumes | containerized ClamAV and constrained extraction worker |
| Hostinger stage | containerized MinIO with VPS-persistent volumes | containerized ClamAV and constrained extraction worker |
| LKE production | Linode Object Storage through its S3-compatible API | horizontally scalable ClamAV/extraction workers with private services |

The application derives every immutable source key as `accounts/{account_id}/documents/{document_id}/revisions/{revision_id}/source`, verifies the requested Account before object access, and records the provider's opaque version identity. Bucket versioning and server-side encryption are required outside disposable local tests. Credentials are workload-only; browsers receive neither object credentials nor public bucket URLs.

Admission is fail closed:

1. The authenticated application command verifies package authority, filename, declared media type, bounded stream length, and request idempotency.
2. A trusted sniffer verifies the actual media type and rejects extension/type disagreement. The initial allowlist is PDF, DOCX, UTF-8 text, Markdown, CSV/TSV, JSON, XML, HTML, YAML, and LOG. Source input is non-empty and at most 50 MiB; extracted text is non-empty and at most 8 MiB.
3. The source is hashed while streaming into its immutable Account-derived quarantine key. Database creation records the exact byte count, SHA-256, provider object version, actor, time, sensitivity, retention, and change summary.
4. A private worker streams the quarantined object through ClamAV. Scanner unavailability, timeout, protocol error, or a positive result cannot advance the revision.
5. Only a clean revision enters a network-isolated extractor with CPU, memory, archive-entry, decompression, output-size, and time bounds. Extracted text receives its own digest and immutable object identity.
6. Chunking and indexing record a versioned algorithm/generation and stable revision/chunk identities. Only a complete index transition makes the revision ready and eligible to become the document's current revision.

Document metadata may change only through versioned commands. Revision source identity—Account, document, revision number, filename, verified type, byte count, source digest, object key/version, actor, creation time, and change summary—never changes. Processing fields advance monotonically and append content-redacted events. Updating a document creates a new revision; it never overwrites a prior source, extracted body, chunks, or citation targets.

Deletion is asynchronous. A request first checks legal hold and `retain_until`, hides the document from new retrieval, and records a deletion checkpoint. The erasure worker removes index generations, extracted objects, source objects, and eligible metadata in an idempotent order, recording content-free receipts. Legal hold and unexpired retention stop physical deletion. Account erasure and movement must enumerate object/index handlers, verify per-revision digests/counts, and preserve rollback checkpoints; a cell schema may not ship document tables without those hooks.

## Consequences

- PostgreSQL is not a binary object store and application replicas remain stateless.
- Local and stage exercise the same S3 protocol used by production while production uses managed Linode object durability.
- A document can remain quarantined or failed indefinitely without leaking into retrieval.
- Storage, malware scanning, extraction, indexing, movement, export, retention, and erasure become explicit operational dependencies with health, queue-age, capacity, and recovery evidence.
- Backup configuration remains environment-owned by the project owner, but the application supplies object manifests, version identities, database checkpoints, and restore reconciliation hooks.

## Verification

- Domain tests reject invalid keys, type mismatches, oversize input, out-of-order transitions, infected/error scans, and publication before indexing.
- PostgreSQL tests prove forced RLS, composite Account references, immutable revision identity, transition guards, movement fences, and exact erasure counts.
- Adapter tests stream bounded objects through S3-compatible storage and ClamAV without buffering whole uploads.
- Local Docker and Hostinger stage certify MinIO/ClamAV/extraction failure modes; LKE certification repeats the contract against Linode Object Storage.
- Retrieval tests prove that unauthorized, quarantined, failed, deleted, or superseded scope cannot influence scores, counts, metadata, or citations.
