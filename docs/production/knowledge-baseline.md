# Knowledge and Baseline module

- Status: Knowledge review surface and the document admission, processing, routed lifecycle and physical deletion path implemented; retrieval and citation validation next
- Phase: 3.2
- Owns: immutable evidence identity, reviewable claims, accepted fact revisions, documents/citations and the Baseline assessment lifecycle
- Does not own: Work lifecycle, Agent execution, provider credentials or external source synchronization

## Prototype findings

The retained prototype is behavioral input, not the production data model. Its useful behavior includes a guided business interview, tailored evidence topics, uploads and source discovery, document recall, renewal dates, reassessment and creation of follow-up Work. The final implementation must preserve those user outcomes.

The prototype persistence cannot be migrated in place:

- `business_knowledge_facts` is one mutable row per free-form key; its history trigger records snapshots but not the decision, evidence set or causal revision that made a value authoritative.
- `source_type` and `source_ref` are strings rather than foreign keys to immutable evidence. Confidence is floating-point and source revisions are not frozen.
- owner answers, document matches, public results and Agent-produced material can converge directly into an active fact without one uniform claim/review boundary.
- Baseline persistence combines interview messages, facts, connectors, source items, research, evidence decisions and Work generation in one store. It has no Account RLS because isolation relied on a database per prototype tenant.
- document rows and chunks lack the final object-store, malware, extraction, retention, deletion and citation-revision boundaries required for production.

Migration will therefore characterize and transform prototype records into the final aggregates. It will not copy tables or preserve ambiguous `source_ref` values as trusted evidence.

## Final Knowledge boundary

Knowledge distinguishes three concepts:

1. **Evidence** is an immutable, Account-owned identity for one exact owner statement, document revision, integration record, captured public source or Agent derivation. It freezes the source identifier/revision, content SHA-256, capture time and creating actor. Evidence content may live in a restricted object or source store; ordinary events and list views carry only identifiers and digests.
2. **Claim** is an immutable canonical JSON proposition for one typed key and scope, with fixed citations, sensitivity and integer confidence (0-1000). A claim begins `proposed`. A human with Knowledge mutation authority may accept or reject it with an optimistic version and bounded reason. Agent/workload output can propose but cannot decide.
3. **Fact** is the current accepted projection for one `(Account, scope, key)`. It points to an accepted claim and has a monotonic revision. Accepting a replacement claim supersedes the previous claim and advances the fact in one transaction; history is retained rather than overwritten.

An accepted claim must have at least one supporting non-Agent evidence source. Agent derivation may supplement evidence but can never be the only basis for authoritative fact. Refuting evidence remains attached and visible to authorized review. Canonical JSON rejects duplicate keys, trailing content, invalid UTF-8, excessive depth and oversized values.

Initial scope vocabulary matches the rest of the final application:

- `account`, with no scope identifier;
- `work_item`, with an Account-owned Work UUID;
- `conversation`, with an Account-owned Conversation UUID.

The initial sensitivity vocabulary is `public`, `internal`, `confidential`, and `restricted`. Redacted queues never contain claim values, citations, source references, document text or decision reasons.

## Baseline boundary

Baseline consumes accepted Knowledge facts and explicit evidence decisions; it does not create authoritative facts by updating Knowledge tables. Its final state machine will separate:

1. interview and explicit unknown answers;
2. evidence-source selection and scoped connector grants;
3. inventory and claim proposals;
4. human evidence decisions;
5. readiness/gap review and version-bound plan approval;
6. active baseline, renewal and reassessment.

Plan generation may create proposed Work only from the accepted assessment version and its frozen facts/evidence decisions. Unsupported Agent output remains a proposal. Renewal or source change marks affected claims/facts for review; it does not silently rewrite them.

## Construction and acceptance order

1. Implement and certify the Knowledge evidence/claim/fact kernel, Account-RLS persistence, redacted events, stable queries and claim decisions.
2. Add routed HTTP, MCP and private application surfaces over one Knowledge application service.
3. Implement document object admission, type/size/malware checks, extraction, immutable revisions, chunks, indexing, retention and deletion.
4. Add retrieval with Account scope, bounded citations and citation-to-document-revision validation.
5. Implement the Baseline state machine and evidence decisions over accepted Knowledge and Documents.
6. Transform prototype facts, documents and baselines with explicit unresolved-source reports, checksums, object/index counts and rollback checkpoints.

Every new cell table must participate in forced RLS, placement write fencing, Account movement, exact erasure accounting and restore replay before its surface is considered complete.

Document foundation checkpoint (2026-08-21): [ADR-0004](decisions/0004-knowledge-document-lifecycle.md) fixes the local/stage/production object mapping to MinIO/MinIO/Linode Object Storage behind one S3-compatible port, with private ClamAV and constrained extraction workers. The typed document/revision kernel now enforces Account-derived immutable source and extracted-text object identities, a 50 MiB source and 8 MiB extraction bound, extension/verified-media agreement, quarantine-first scan/extraction/index transitions, stable hashed chunks, publish-only-when-ready, sensitivity, retention, legal hold and asynchronous deletion states. Forward cell migrations add exact derived-object key/version persistence plus content-free, lease-based per-cell processing and deletion queues without rewriting shipped migrations. The cell schema and classified PostgreSQL repository persist documents, revisions, chunks, deletion receipts and content-redacted events with forced RLS where customer metadata is present, immutable fields, exact-ready-revision publication guards, Account movement fences and erasure counts. Application commands separate human admission/publication/deletion from workload-only processing and derive replay-stable chunk identities. Disposable PostgreSQL tests cover replay, cross-Account denial, queue exclusion/lease completion, processing order, publication, immutable metadata, redacted events, deletion entry and physical finalization. The object adapter streams exact-size/digest conditional writes for source and extracted text into a private versioned S3-compatible bucket, requires opaque object-version identities, verifies metadata on replay/read, deletes only exact versions and cleans newly-created source orphans after database failure. Local and CI Docker exercise the adapter against encrypted, versioned MinIO; stage secret generation and Compose use the same pinned services without exposing an object port. The ClamAV adapter and pinned private service use `VERSIONCOMMANDS` and `INSTREAM`, exact declared-size streaming, bounded chunks/replies, operation deadlines and fail-closed clean/infected/error parsing; real-container tests prove clean content and the standard harmless antivirus signature. Local/CI freeze the image signatures while stage updates a persistent signature volume. The Tika adapter uses private HTTP, exact content length, the fixed media allowlist, disabled embedded extraction, a two-minute task timeout and an 8 MiB UTF-8 output ceiling; real pinned-container tests certify text, PDF and DOCX extraction. The versioned UTF-8 chunker deterministically prefers paragraph, line and whitespace boundaries, retains a bounded overlap, and records exact source-byte offsets and lexical token counts for replay-stable citations. A restart-safe worker per cell resumes from persisted scan/extraction/index state and fairly services physical deletion, conditionally creates extracted objects, cleans newly created orphans on failed recording, bounds retries/dead letters and reports both queue ages plus dependency readiness under a dedicated least-privilege role. Local Compose certifies both worker replicas against real PostgreSQL, MinIO, ClamAV and Tika; the future stage command enables the same `knowledge-processing` profile. The signed app-router/cell boundary now exposes typed multipart upload, content-redacted stable list, exact detail, ready-revision publication and retention-checked deletion entry. The router and cell independently bind the complete request digest while spooling up to the 50 MiB contract through mode-private temporary files; the application derives byte count, SHA-256 and verified media type instead of trusting caller metadata, validates PDF/DOCX/structured-text signatures, and streams only the rewound verified source into its immutable key. Physical deletion rechecks eligibility, loads only an Account-scoped exact-version manifest, writes immutable content-free receipts, removes source and extracted versions idempotently, and then removes chunks, marks every revision and document deleted, appends a redacted audit event and completes its lease in one database transaction. Unknown object-store commit replay and incomplete-receipt refusal are tested. Retrieval and citation validation remain open, so this checkpoint does not yet claim the complete document/retrieval bullet.
