# Knowledge and Baseline module

- Status: Knowledge/document lifecycle and Baseline lifecycle complete through governed interview/scope catalogs, exact accepted-Fact resolution, HTTP/MCP and deterministic approved-gap Work materialization; source grants, renewal feedback and prototype transformation remain
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

Document lifecycle checkpoint (2026-08-21): [ADR-0004](decisions/0004-knowledge-document-lifecycle.md) fixes the local/stage/production object mapping to MinIO/MinIO/Linode Object Storage behind one S3-compatible port, with private ClamAV and constrained extraction workers. The typed document/revision kernel enforces Account-derived immutable source and extracted-text object identities, bounded media and text, quarantine-first processing, stable hashed chunks, ready-only publication, sensitivity, retention, legal hold and asynchronous deletion. Forward migrations add exact object identities, content-free processing/deletion queues, immutable exact-version deletion receipts and a generated PostgreSQL full-text vector with a GIN index. Customer metadata stays behind forced Account RLS and movement/erasure fences. The routed and optional MCP read boundary accepts query content only in a body, searches only the currently published ready revision, filters restricted documents in PostgreSQL, returns at most 20 integrity-bound 4 KiB chunks, and resolves an exact citation only while its document/revision/chunk tuple remains current and ready. Every citation carries Account/document/revision/chunk IDs, byte offsets, content SHA-256 and index generation; deletion or publication change makes a stale tuple not found. Physical deletion rechecks eligibility, loads only an Account-scoped exact-version manifest, writes immutable content-free receipts, removes source and extracted versions idempotently, and then removes chunks, marks every revision and document deleted, appends a redacted audit event and completes its lease in one transaction. Unknown object-store commit replay, incomplete-receipt refusal, cross-Account retrieval denial and stale/deleted citation rejection are covered. The complete document lifecycle is now constructed; the Baseline state machine and prototype transformation remain next.

Baseline lifecycle checkpoint (2026-08-22): the pure aggregate fixes the lifecycle as `interview -> inventory -> gap_review -> plan_approval -> active -> ready`, with reassessment archiving the explainable prior assessment and starting a new interview. Interview answers reference an exact accepted Fact revision or record an explicit reasoned unknown. Requirements freeze their evidence-catalog and scope-policy versions, responsibility and renewal policy; human evidence decisions bind immutable Knowledge Evidence identities. Every requirement must be explicitly satisfied, a gap or not applicable before plan submission. The aggregate—not the caller—sorts explicit gaps and derives a length-framed, assessment/version/catalog/scope-bound SHA-256 plan plus exact Work count; restore rejects persisted digest/count drift. Owner/Administrator approval binds that exact plan ID, digest and pre-plan assessment version, and readiness refuses unresolved gaps. Six normalized cell tables carry these records under forced Account RLS, exact Knowledge Fact/Evidence and Persona references, immutable decision/event history, movement fencing and exact erasure/restore accounting. The transport-neutral application service applies the canonical Knowledge-package authorization policy to human-only, optimistic commands. Its classified PostgreSQL adapter restores the complete aggregate, rejects stale writes, records content-free audit events, supports replay-safe creation and atomically archives the prior assessment during reassessment. PostgreSQL 17 integration coverage proves Account isolation, exact Fact/Evidence foreign keys, frozen interview answers, immutable evidence decisions, redacted events, plan binding and the single-current-assessment invariant. Routed HTTP operations and MCP tools call this same boundary. Approved gaps materialize only through Work's authorized/capacity-governed command service using deterministic Work and per-item correlation IDs, making partial or unknown outcomes safe to complete by exact retry without duplicate Work. Governed evidence catalogs/source grants, completion-to-evidence feedback, renewal processing and prototype transformation remain open P3.2 work.

Governed scope checkpoint (2026-08-22): a repository-owned, versioned interview and evidence catalog now defines seven explainable questions, twenty evidence definitions and deterministic software, field-service, professional-services and retail scope profiles. Required questions must be answered with an exact Fact revision or an explicit reasoned unknown before inventory can begin; unknown question keys are rejected. The application resolves every referenced `(Fact ID, revision)` under Account RLS, requires the resolved Fact key to match the governed question, accepts only meaningful scalar values, and derives the frozen requirement set, responsibility, renewal policy and deterministic IDs itself. Solo businesses omit workforce-only requirements. Start, inventory completion and reassessment accept empty bodies, so HTTP/MCP callers can no longer choose catalog versions, policy versions or requirement drafts; generated contracts enforce the same boundary. Historical Fact revisions remain resolvable after their accepted claims are superseded. Focused application, transport and PostgreSQL coverage proves required-question completeness, mismatched-key refusal, caller-field rejection, deterministic profile selection and cross-Account Fact denial. Scoped source grants/evidence selection, Work-completion evidence feedback, renewal processing and prototype transformation remain open P3.2 work.
