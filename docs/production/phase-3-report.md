# Phase 3 final construction plan

- Plan date: 2026-08-20
- Entry date: 2026-08-21
- Starts after: completed [Phase 2.5 realignment](phase-2-5-closeout-report.md)
- Purpose: complete every remaining application capability, migrate validated prototype behavior, certify the final LKE deployment and release the complete product
- Production rule: **all construction phases and advertised packages must be complete before production release**

## Phase 3 scope

Phase 3 consolidates the historical Phase 3-8 backlog into one final body of work. It is not limited to Work and Attention. It includes:

1. Work and Attention.
2. Knowledge and Baseline.
3. Agent workspace and durable execution.
4. Scheduling, Finance, Marketing and integrations.
5. HTTP, MCP and private React product surfaces.
6. Prototype data/behavior migration and retirement.
7. Production hardening, Linode deployment, canary and release.

No incomplete package is hidden to justify production launch. Feature flags and unpublished Catalog entries remain useful during development, but the production release gate requires the full intended application.

## Starting position after Phase 2.5

The repository already contains strong foundations:

- typed Work lifecycle, forced-RLS persistence, routed HTTP commands/queries and a private Work surface;
- Boardrooms, immutable Persona versions, Conversations, Agent Runs, dispatch/projection queues and encrypted runner exchange;
- package admission, action authorization/ledger foundations and content-safe observability;
- generated OpenAPI contracts and compatibility enforcement.
- global passkey incident response and restore-gated bounded identity retention with content-free operational metrics;
- two independently signed, attested and vulnerability-admitted application/website pairs (active RC.5 platform baseline and source-compatible RC.4 rollback), a live repeatable Hostinger deployment with isolated per-cell Docker runner paths and provider egress, and CI-rendered two-cell Linode overlays with a dedicated workload-mTLS tool router;
- a successful RC.5 -> RC.4 -> RC.5 connected-stage rollback, two 11-check anonymous boundary certificates, interruption-free single-replica recovery, and live Stripe sandbox/webhook, non-production OpenAI and SMTP TLS readiness.

The foundations do not yet constitute the full product. In particular:

- the Work-to-Agent claim/heartbeat/reconcile bridge is absent even though the lower-level Agent dispatch queue/worker exists;
- the Attention domain is absent;
- Knowledge/Baseline production modules are absent;
- Agent orchestration is limited to one durable sequential Boardroom pass; manager synthesis, delegation and resumable owner questions remain;
- production MCP is absent;
- Finance, Marketing and most integrations remain prototype-only or unimplemented;
- the private application is not yet the final React product surface;
- no complete prototype migration/cutover has occurred.

The Hostinger Phase 2.5 entry gate is complete. Stage is intentionally pristine: there are no users, Accounts, provider-price mappings, billing profiles or subscriptions. This prevents temporary Catalog/customer state from being mistaken for final-product release evidence. Phase 3 must publish reviewed stage mappings through the signed `catalog-admin` boundary and then run real email/passkey/Stripe journeys against the product capabilities being accepted. The LKE kubeconfig is first needed in P3.7: its initial use is a read-only cluster inventory, followed by explicit replacement of every fail-closed storage/CNI/API/add-on/secret/runtime placeholder before any apply.

## P3.0 — establish the Phase 3 acceptance baseline

Before feature construction fans out:

1. Treat RC.5 as the platform baseline, not as the final product release candidate.
2. Define the final launch Catalog and obtain exact-scope operator authorization for its stage Stripe price mappings; do not mutate protected Catalog tables directly.
3. Create revocable synthetic stage identities and mailboxes, with no customer data, for repeatable email, WebAuthn/passkey, Membership, billing and recovery journeys.
4. Use the revision-controlled [product-journey certification driver](journey-certification.md), which records content-free results while keeping session material, WebAuthn keys, provider payloads and identifiers outside Git.
5. Run the relevant customer journey after each package/surface becomes acceptance-ready, then rerun the complete matrix against the final immutable application/website pair.

Exit: Phase 3 has safe fixtures and evidence formats without treating unfinished product behavior as a release gate.

## P3.1 — Work and Attention

### Finish Work

- Add Persona foreign keys and Persona assignment policy.
- Add assignment editing, provenance attachment and conversation-link commands.
- Add representative query-plan, pagination property and concurrency stress tests.
- Preserve drafts and provide accessible reason/command interactions.
- Characterize and migrate prototype Work data with capacity reconciliation.

Implementation checkpoint (2026-08-21): the composite Account/Persona foreign key, active published Persona assignment policy, routed Persona assignment input, additive provenance and Conversation-link commands, Account-scoped Conversation/Run foreign keys, typed redacted mutation events, generated client contracts, erasure-safe deferred integrity behavior, representative query-plan fixtures, tie-heavy pagination properties, concurrent completion/assignment stress, assignment editing, explicit reason dialogs, tab-scoped create drafts and accessible command announcements are complete. Applied private-route browser/assistive-technology certification and prototype migration remain.

### Implement typed Attention

Create separate Account-scoped aggregates:

- `InformationRequest` for explicit fact requirements and exact parent resumption;
- `WorkReview` for version-bound review decisions;
- `ConsequentialApproval` for canonical payload, policy, evidence, expiry and cancellation.

Add forced-RLS persistence, optimistic versions, immutable events, queries, redacted DTOs and authorization matrices. Answering shared information may complete only eligible requests; altering a proposal invalidates its approval.

Implementation checkpoint (2026-08-21): the three aggregates now have separate typed kernels and persistence restore boundaries. Information requests bind exact fact key/scope and parent Work, reviews bind an assigned User to a Work version and proposal digest, and consequential approvals bind canonical duplicate-key-free JSON, evidence, capability, operation/invocation, policy, expiry and optional independent review. Four Account-owned cell tables add forced RLS, composite Work/Conversation/Agent references, queue/detail indexes, immutable redacted event shapes, movement write fences and cascade-safe exact erasure counts. Classified PostgreSQL repositories restore every row through the domain kernel, provide bounded stable keyset queries, make identical creates idempotent, enforce expected-version updates and append redacted events atomically. Package-authorized application commands/queries now apply the narrower role/object matrix, require an active eligible assigned reviewer and expose content-minimized queue views. Exact shared-fact completion is a bounded serializable transaction and returns only waiting parent Work items with no open information blocker; concurrent duplicate submissions converge without duplicate events. The routed cell runtime resolves assigned reviewers through the exact signed request proof and a read-only global broker, preserving the cell-only `app-api` credential. It passes identifier-only completion plans into one atomic Work-owned transition boundary, and replay reconstructs the original answer cohort so an interrupted resumption can safely finish. An approved aggregate records its exact execute-only runner authorization in the same transaction, and approved-proposal invalidation cancels that projection; conflicting runner state rolls back the aggregate and event. Nineteen customer HTTP operations now cover redacted queues, authorized detail, creation, decisions, cancellations and action recovery with route-bound idempotency, ETag concurrency where applicable, exact package allowlists and generated typed Go/TypeScript contracts. The 19-tool MCP adapter reuses those application services with Bearer-only protocol authentication, exact per-call Account/package/role authority, typed structured results, safe error parity and raw canonical-payload preservation. The private Your Turn surface combines open information, signed-in-reviewer Work, Owner/Administrator approval and unknown/manual action-recovery queues with exact detail, read-only modes, tab drafts, stable retries, version-conflict recovery, independent confirmation, keyboard movement and live announcements. Tests cover role/object/package authority, exact information eligibility and parent planning, routed reviewer eligibility, Work resumption/replay, review/proposal invalidation, approval projection/time/binding rollback, payload/evidence/policy invalidation, action recovery/redaction/dual control, repository replay/concurrency/conflict/isolation/redaction, RLS, cross-Account constraints, event immutability, movement participation, erasure/restore, HTTP/MCP boundary behavior and private-shell contracts. The production MCP token/Account-routing gateway, applied browser/assistive-technology certification and final React consolidation remain. See [Attention module](attention-module.md) and [MCP transport](mcp-transport.md).

### Connect Work to Agent execution

Implement a Work-owned claim/start/link/heartbeat/release/resume/reconcile state machine. Every crash point must converge to one linked active Run or one safely requeued Work item. Existing Agent dispatch and runner capacity are reused but do not replace this boundary.

Implementation checkpoint (2026-08-21): Persona assignment now freezes an immutable, Account-owned execution intent in the same Work transaction, including Work version/text, initiating User, exact Persona version, Boardroom policy version and deterministic Conversation/Run identities. A lease-fenced worker resolves current Membership, placement, enabled Agents mutation access, entitlement version and concurrent-Run limit through a private mTLS admission endpoint without presenting a browser bearer token or impersonating the User. Execute-only cell functions claim, heartbeat, retry/release and atomically create the complete one-turn Agent plan, enqueue the existing dispatch path, link Conversation/Run provenance, advance Work to `in_progress`, append a redacted Work event and retire the execution lease. Unknown commit replay reconciles the same deterministic linked pair; reassignment revokes an already-held lease; capacity and transient authority failures requeue with bounded backoff; permanent Work, Persona or authority drift dead-letters without creating a Run. Forced RLS, Account movement fencing, exact erasure counts, least-privilege Docker/Kubernetes composition and crash-window PostgreSQL tests are complete. Applied stage execution and final product-journey certification remain release evidence, not construction gaps. See [Work-to-Agent execution](work-agent-execution.md).

### Finish consequential actions

- Make Attention the owner of approval projections consumed by the action ledger.
- Add the executor registry, definite-failure retry policy, unknown reconciliation and dual-controlled manual resolution.
- Add at least one real consequential adapter with idempotency and side-effect-free lookup.
- Add redacted customer/operator views, alerting and recovery runbooks.

Implementation checkpoint (2026-08-21): Attention's approval transaction is now the sole producer of the exact runner authorization projection. The action ledger consumes a versioned content-free executor/retry registry, freezes executor and policy versions per operation, retries only registry-allowlisted definite no-effect failures with bounded delay, and forces all uncertain/completed replay through reconciliation. Unknown outcomes can enter a two-person manual-resolution record whose requester cannot confirm it. The first real executor, `stripe.customer.create`, uses the operation UUID as Stripe's idempotency key and reconciles through side-effect-free exact Account/operation metadata search; the Stripe credential never enters runner Jobs. Content-free broker metrics, paging rules and a recovery runbook are complete. A shared Owner/Administrator application service now supplies content-redacted stable pages/detail and replay-safe manual resolution commands through generated HTTP contracts, four optional MCP tools and Your Turn. Human evidence is retained only as a SHA-256 digest on these surfaces, and confirmation remains independently authorized. Applied stage execution and final journey certification remain release evidence. See [Consequential action execution](consequential-actions.md).

Exit: Work, Your Turn and approved external actions are complete through domain, persistence, HTTP, MCP and UI.

## P3.2 — Knowledge and Baseline

Implementation checkpoint (2026-08-21): the prototype inventory is complete and the final Knowledge boundary is fixed around immutable evidence, reviewable canonical claims and monotonic accepted fact revisions. The typed kernel freezes Account, source kind/reference/revision, SHA-256, capture time and actor; rejects duplicate/trailing/oversized JSON; models Account/Work/Conversation scopes and sensitivity; permits workloads to propose but only humans to decide; and refuses authoritative acceptance when Agent derivation is the only supporting evidence. The first cell schema adds evidence, claims, citations, current facts, immutable fact revisions and content-redacted events with forced RLS, composite Account references, database acceptance/projection guards, movement fences and exact erasure accounting. The classified PostgreSQL repository provides exact replay, domain restoration, serializable claim decision/fact projection, supersession, stable redacted fact pagination and event redaction. Fresh PostgreSQL tests prove cross-Account denial, immutable history, agent-only-evidence rejection, repository replay and accepted fact creation. Routed HTTP/MCP/private UI surfaces, document infrastructure, retrieval and the Baseline state machine remain in this block.

Routed-surface checkpoint (2026-08-21): evidence registration, claim proposal/detail/decision and sensitivity-filtered fact listing now cross the signed app-router/cell boundary through the shared Knowledge service. All mutations use routed UUID idempotency, decisions require weak version ETags, responses are typed in OpenAPI, and generated Go/web contract inventories are current. The optional MCP transport exposes the same five operations with Account/package authorization and bounded opaque cursors. A discoverable proposed-claim review queue and private Knowledge page remain before this surface is considered complete.

Review-surface checkpoint (2026-08-21): a sixth shared operation provides a stable, value-redacted claim queue filtered by state, scope, key prefix and caller sensitivity. The private `/app/knowledge` page uses that queue to discover proposals, fetches exact claim value/citations only on selection, and submits human accept/reject decisions with a fresh idempotency UUID and the claim ETag. The accepted-fact projection remains value-redacted in its list. Read-only package mode removes decision controls. Document admission, retrieval, citation-to-document validation and the Baseline lifecycle remain next.

Document-lifecycle checkpoint (2026-08-21): [ADR-0004](decisions/0004-knowledge-document-lifecycle.md) selects containerized MinIO in `ubunturojo`/Hostinger and Linode Object Storage in LKE behind one S3-compatible port. The Account-owned kernel, trusted admission, ClamAV/Tika processing, deterministic chunks, ready-only publication, content-free processing/deletion queues, exact-version receipts and atomic physical finalization are complete under forced RLS, movement fencing and erasure accounting. The existing least-privilege per-cell worker fairly services both queues and reports their independent age/dead-letter state. A generated PostgreSQL full-text vector and GIN index now support shared HTTP/MCP retrieval without another search store: query text stays in the request body, PostgreSQL filters Account, current-ready revision and sensitivity before ranking, and responses contain at most 20 integrity-bound 4 KiB chunks. Exact citation lookup binds Account/document/revision/chunk, offsets, digest and index generation and fails once a citation is stale or deleted. Generated contracts now cover 98 customer operations. Cross-Account search, restricted filtering, exact resolution, stale/deleted rejection, unknown-delete replay and full local Docker dependencies are tested. The document upload/deletion and retrieval/citation bullets below are constructed; Baseline is next.

Baseline-foundation checkpoint (2026-08-22): the typed assessment aggregate and first normalized cell schema are complete. The aggregate freezes catalog/scope-policy versions; records interview answers as exact accepted-Fact revisions or explicit unknowns; freezes requirement responsibility and renewal policy; binds immutable human decisions to Knowledge Evidence; requires every requirement to be explicitly satisfied, a gap or not applicable before plan submission; binds Owner/Administrator approval to the exact plan ID, SHA-256 and pre-plan assessment version; refuses readiness with gaps; and preserves superseded assessments through reassessment. Six Account-owned tables add forced RLS, exact Fact/Evidence and Persona references, immutable decision/event history, stable queue/renewal indexes, movement fencing and exact erasure/restore counts. Pure replay tests and the full `ubunturojo` Docker gate pass. Repository/application commands, deterministic Work-plan creation, routed surfaces and prototype transformation remain open.

- Implement source-attributed facts, claims, evidence, revisions, scope and confidence.
- Implement document upload, malware/type/size checks, extraction, chunking, indexing, retention and deletion.
- Implement Account-scoped retrieval, citation validation and bounded result contracts.
- Implement the Baseline interview/state machine, evidence decisions, readiness, renewal and reassessment.
- Implement evidence-source selection and plan generation without allowing unsupported Agent output to become authoritative fact.
- Migrate and reconcile prototype knowledge/documents/baselines with object, index and citation checks.

Exit: source-attributed business memory and baseline claims are complete and production-certifiable.

Baseline persistence checkpoint (2026-08-22): the Baseline aggregate is now carried through the canonical authorized application boundary and a classified, Account-scoped PostgreSQL repository. The repository persists each optimistic state transition with a redacted audit event, exact accepted Fact revision and Knowledge Evidence references, immutable plan binding and atomic reassessment. A PostgreSQL 17 lifecycle test covers idempotent start, stale-write conflict, complete restore, cross-Account denial, frozen and immutable history, event redaction, approval/readiness and one-current-assessment enforcement. The next P3.2 construction slice is the supported HTTP/MCP boundary and deterministic creation of proposed Work from the exact approved assessment/plan digest; this checkpoint does not declare P3.2 complete.

Baseline surface/planning checkpoint (2026-08-22): 12 routed HTTP operations now cover start/get, answers, inventory, evidence decisions, explicit dispositions, plan submission/approval/materialization, readiness and reassessment with strict JSON, Account-bound operation IDs, idempotency keys and optimistic ETags. Generated Go/TypeScript contracts and three MCP tools expose the same application outcomes. Plan submission no longer trusts a supplied digest or count: the aggregate deterministically hashes its frozen, sorted gap proposals and restore verifies the binding. An exact approved plan is materialized through Work's existing authorization, capacity reservation and idempotent repository boundary with deterministic Work/correlation IDs; a partial or unknown result is completed by replaying the same operation. P3.2 remains open for governed evidence catalogs and source grants, completed-Work evidence feedback, renewal workers and prototype transformation.

Baseline governed-scope checkpoint (2026-08-22): the versioned repository-owned catalog now defines seven explainable interview questions, twenty evidence definitions and deterministic software, field-service, professional-services and retail profiles, including a solo-business adjustment. Every required question must carry an exact accepted-Fact revision or explicit reasoned unknown before inventory; the Account-RLS repository resolves the exact historical revision and the application rejects unknown question keys, duplicate references, mismatched Fact keys and unsuitable canonical values. The application—not HTTP or MCP callers—now freezes current catalog/policy versions on start and reassessment and derives requirement IDs, titles, responsibilities and renewal periods from resolved facts. The three formerly caller-shaped operations accept only strict empty JSON objects and generated clients have dropped the obsolete inputs. PostgreSQL coverage proves exact resolution and cross-Account denial. P3.2 remains open for scoped source grants/evidence selection, completed-Work evidence feedback, renewal/reassessment scheduling and prototype transformation/reconciliation.

Baseline source/evidence checkpoint (2026-08-22): immutable-scope, revocable email and Google Drive grants now permit only selected folders plus optional email date bounds after plan approval, with no credential storage. Owner/Administrator application policy, forced-RLS persistence, redacted events, stable listing, exact erasure/movement participation, three generated HTTP operations and two MCP tools are complete under the Integrations package; Integrations now explicitly depends on Knowledge. An exact app-router allowlist now makes all Baseline routes reachable and assigns lifecycle routes to Knowledge and grant routes to Integrations. Evidence decisions resolve the Account-owned Knowledge Evidence source before changing the assessment. Human evidence registration is limited to owner statements, while Agent derivations and integration records without the future connector-to-grant capture binding fail closed and cannot satisfy requirements. The following checkpoint closes completed-Work evidence feedback and deterministic renewal/reassessment materialization; connector execution remains in P3.4.

Baseline completion/maintenance checkpoint (2026-08-22): approved Baseline plans now persist immutable normalized Work lines behind the frozen digest/count, preventing later requirement completion from invalidating the historical plan or changing exact-retry materialization. A linked requirement is satisfied only when the application resolves both an exact completed Account Work item carrying that requirement's frozen Baseline provenance and a separate acceptable Knowledge Evidence identity; completion alone, Agent derivation and unbound integration records remain non-authoritative. The reasoned confirmation is an optimistic Baseline event and renews the requirement from the human decision instant. Readiness freezes a 90-day reassessment date. The 30-day maintenance window deterministically materializes renewal and reassessment Work, uses urgent priority after the due instant and derives cycle-specific replay IDs from the exact obligation/due date. Two additional routed HTTP operations and the existing MCP mutation boundary bring the customer contract to 115 operations. PostgreSQL plan-line RLS, immutability, movement/erasure participation, completed-Work resolution, cross-Account denial and restore are covered. P3.2 now remains open for scheduler-driven invocation and prototype transformation/reconciliation; connector execution remains in P3.4.

Prototype transformation checkpoint (2026-08-22): a new read-only operator command snapshots an exact retained prototype tenant under PostgreSQL repeatable read, verifies all stored document revision checksums and emits a deterministic, content-sealed private bundle. The manifest reconciles exact source table counts, schema versions and transaction checkpoint to normalized fact candidates, one reconstructed text object per non-deleted legacy document revision, regenerated final chunk expectations, governed Baseline question/requirement review plans and deterministic target IDs. A standalone unresolved report accounts for missing owner actor identity, unbound email/Drive/public captures, legacy evidence decisions, interview messages, research results and Baseline/Work links. No ambiguous record becomes authoritative, Agent-derived material stays non-authoritative, deleted documents are not resurrected, and export writes no destination state. Atomic no-overwrite publication plus a rollback checkpoint prevent partial bundles. Pure and PostgreSQL 17 tests cover deterministic ordering, checksum/tamper rejection, exact numeric confidence conversion and full inventory scanning. P3.2 remains open for the destination receipt/import/reconciliation operation and owner review; unattended maintenance invocation belongs to the workload scheduling construction boundary.

Prototype destination checkpoint (2026-08-22): the immutable release image now includes a strict local bundle loader and replay-safe `prototype-import` operator binary. Import is performed only as the named `prototype-migration` workload through the final Knowledge services and S3 object boundary. It admits unpublished reconstructed documents plus reviewable Evidence/Claims, never owner statements or accepted Facts. A forced-RLS run/receipt/event ledger binds the exact manifest, source checkpoint, expected counts, deterministic targets and reconciliation digest; movement fencing, immutable history and exact Account-erasure participation are tested. Post-processing reconciliation opens both exact object versions, compares bodies and SHA-256 values, regenerates every chunk, validates all Evidence/Claim/citation records and refuses missing or extra receipts. A first run safely stops in `imported` while the normal document worker processes; exact retry converges to `reconciled` and writes a private no-overwrite certificate. PostgreSQL 17 coverage proves receipt replay/conflict behavior, immutable terminal state, pending-to-ready retry and exact erasure. A dedicated global/cell database role, separate prefix-scoped object identity, exact cell-placement check and dual restore gates now constrain deployment. The executed internal cohort processed eight reconstructed revisions, reconciled 16 exact object versions and every deterministic chunk, accounted for all 539 unresolved records, and sealed reconciliation SHA-256 `2ad508e3d704bdc5c68bc058ed832dadc69d8b27070e0eb52c6952410728c1c9`. It exposed and closed a forced-RLS event-replay grant and Tika plain-text normalization mismatch. P3.2 remains open only for the real Owner to resolve or decline the private review packet and sign its review certificate; no connected environment was mutated.

Baseline scheduler checkpoint (2026-08-22): a content-free per-cell technical queue now transactionally tracks the earliest 30-day lead window for every ready assessment's reassessment or satisfied-requirement renewal obligation. `baseline-maintenance-worker` is the sole unattended identity: it leases with `SKIP LOCKED`, revalidates exact Account placement, package authority and assessment version across dedicated least-privilege global/cell credentials, and calls the same capacity-governed Work service used by humans. Deterministic cycle IDs make daily retries and crash recovery duplicate-free; stale leases fail, bounded failures enter dead-letter state, and no workload path can decide evidence, approve plans or impersonate a User. Restore-gated Docker local/Hostinger stage topology and two-replica LKE cell manifests are included. PostgreSQL 17 tests cover trigger scheduling, early exclusion, lease fencing, recurring replay, dead-letter stats and exact erasure. Construction and the sealed internal migration cohort are complete; P3.2 now remains open only for the human Owner review certificate.

## P3.3 — Workspace and Agent execution

Cost-control checkpoint (2026-08-22): the provider-neutral usage contract now includes gateway-calculated micro-unit cost. Model-gateway starts only with a syntactically valid operator price book, requires an exact entry for the requested model and fails closed before provider invocation when no entry exists. Provider responses cannot inject cost. The bounded runner carries the immutable Persona maximum, accumulates token and cost usage across every sequential model/tool turn with overflow checks, and stops with a stable `cost_limit_exceeded` failure once the ceiling is crossed. Successful projection stores cost beside token counters, binds it into idempotent replay comparison and exposes the completed usage on the generated HTTP/TypeScript Agent Run contract. Stage secret preparation, Compose and LKE secret contracts now require the price book without committing actual provider prices.

Context-snapshot checkpoint (2026-08-22): StartRun now accepts explicit bounded Work-item, accepted Knowledge-Fact and Baseline-Assessment attachments. The cell repository captures all selected sources in one repeatable-read transaction, freezes exact source versions and canonical content into the existing Account-owned Run row, and binds per-item plus aggregate SHA-256 digests. Active-fact and membership sensitivity rules are rechecked before capture, missing or oversized selections fail atomically, idempotent retries compare the original attachment identities, and failed-run retry copies the original bytes rather than reading newer source state. Dispatch validates the canonical envelope and digest, prepends it as clearly labeled untrusted evidence, and otherwise uses the existing Conversation sequence watermark. The generated HTTP/TypeScript contract publishes only attachment kind, identity, version and digest. This closes the deterministic Work/Knowledge/Baseline/Conversation context boundary without adding a new erasure/movement table. Orchestration/synthesis, model fallback, schedules, document attachments, governed proposals and remaining queue recovery work were still open at this checkpoint.

Sequential-orchestration checkpoint (2026-08-22): an ordered Run no longer dispatches every Persona from the original user-message watermark. Only turn one is initially eligible. Successful projection atomically commits its validated Persona Message, advances exactly the next turn's frozen Conversation watermark to that Message sequence and enqueues exactly that invocation. The later dispatch snapshot therefore contains every preceding contribution in order while retaining the Run's original Work/Knowledge/Baseline bytes and Persona versions. A failed turn atomically cancels every still-queued downstream turn with `prior_turn_failed` and makes the Run terminal (`failed` before any contribution, otherwise `partially_failed`), so a plan cannot strand undispatchable invocations. Customer retry recreates the failed turn and its entire canceled successor cohort as a new sequential plan from the failed turn's last valid watermark rather than dropping work or reading newer context. Migration refuses to change sequencing while an older multi-turn Run is active. Fresh PostgreSQL migration/integration certification covers deferred dispatch, watermark advancement, downstream cancellation, exact recovery and idempotent projection. This completes the durable sequential Boardroom pass; manager synthesis, delegation, resumable owner questions, model fallback, schedules, document attachments, governed proposals and remaining queue recovery work are still open.

Document-attachment checkpoint (2026-08-22): the same StartRun context contract now accepts explicit Knowledge Document identities. Capture resolves only each document's ready current revision under Account RLS, rechecks restricted sensitivity against the initiating Membership, freezes the revision identity, title, media type, text digest, index generation and every ordered integrity-verified chunk, and exposes only the document identity/revision/digest reference on Run reads. No attachment is silently truncated: a non-ready, inaccessible, corrupt or aggregate-over-48-KiB selection fails the entire Run transaction. Exact retry compares the selected document identities and failed-Run retry copies the already frozen bytes, so later publication, deletion or reindexing cannot change the execution. Generated HTTP/TypeScript contracts and fresh local PostgreSQL dispatch certification cover the attachment path. Larger documents remain available through bounded retrieval tools; model fallback, manager/delegation flows, resumable owner questions, schedules and governed proposals remain open.

Fallback-protocol checkpoint (2026-08-22): capability execution failures can now preserve one validated content-free machine classification across the private broker transport while retaining the existing generic failure type. The gateway emits no provider body or private error detail, malformed reason codes collapse to `execution_failed`, and transport/client tests bind `model_provider_unavailable` end to end. This is prerequisite plumbing, not fallback completion: immutable ordered target policy, preallocated attempt identities, selective retry, accumulated usage and projected selected-model audit remain open.

Fallback-completion checkpoint (2026-08-22): immutable Persona policy now freezes one primary model and at most two ordered same-provider fallbacks under the same reasoning, token, cost and tool policy. Every possible model step/target pair receives a deterministic operation identity before dispatch. The runner may advance targets only when the first provider call returns the exact content-free `model_provider_unavailable` classification; every other failure is terminal, and the first successful response pins that model for all provider-continuation steps. Usage and cost remain cumulative across successful model steps. The Account-owned invocation freezes all permitted targets, successful projection validates and stores the selected target atomically, exact replay compares it, and Agent Run reads expose it without provider error detail. The Work-to-Agent producer uses the same target and operation-shape boundary. Fresh PostgreSQL migration, upgrade-role, forced-RLS, Work-link, projection, API-contract, website and full local Docker certification pass. Manager/delegation flows, resumable owner questions, schedules, governed proposals and remaining queue recovery remain open.

Result-policy checkpoint (2026-08-22): result publication now applies a named, frozen version-1 policy after structural/provider validation and before any customer Message commit. The content-free projection claim carries the immutable Persona citation/action policy, only later-turn Persona identities from the frozen Run plan and exact document/chunk identities from the frozen context envelope. Required citations must bind one of those exact pairs, `none` suppresses citations, duplicate or invented citations/delegates fail closed, delegations may target each later Persona at most once and `none` suppresses proposed actions. New document snapshots include the exact chunk identity as well as its integrity fields. An accepted targeted delegation is deterministically inserted after its source contribution as labeled untrusted task content when the later Persona dispatches; models still cannot enqueue Personas or mutate the plan. The policy version is stored on every invocation so replay cannot silently adopt a later implementation. Pure policy, projection rejection, targeted-history, fresh PostgreSQL migration/least-privilege claim and full local Docker/race certification pass. Deterministic delegation expansion beyond the initially frozen plan, manager configuration/final synthesis, owner-question wait/resume and projection of proposed actions into Attention remain open.

Manager-synthesis checkpoint (2026-08-22): a Boardroom now has optimistic, Account-scoped manager configuration that accepts only an active Persona with a published version in that exact Boardroom. The mutable manager identity and Boardroom policy version affect only future plans. `manager_led` Run creation freezes the caller-selected specialists in order and the manager's exact current immutable Persona version as the final turn; selecting the manager as a specialist is rejected, and the existing sequential watermark/projection path makes every prior specialist contribution available to that final synthesis. The Run mode is durable, exposed in the generated HTTP/TypeScript contract and preserved on failed-turn retry while the retry continues to reuse the original frozen Persona versions and context bytes. Public HTTP requires an explicit mode; legacy trusted one-turn producers normalize to `selected`. The private Agents surface can set the synthesis manager, selects explicit run mode and excludes the manager from the specialist picker. Fresh PostgreSQL coverage proves optimistic replay, specialist-to-manager order and manager-version freezing after later publication. Account movement remains acyclic because same-Boardroom/active publication validation is performed transactionally in the command path rather than by a reverse foreign key. Full local Docker, PostgreSQL, account-movement, erasure, race, OpenAPI, browser, website and vulnerability certification pass. Deterministic expansion beyond the frozen plan, owner-question wait/resume, proposed-action projection and remaining queue/operator recovery remain open.

Governed-action checkpoint (2026-08-22): new Agent invocations now freeze result-policy version 2, while already queued version-1 work remains replayable and message-only. Persona versions may enumerate at most 32 immutable action capabilities. A v2 result may propose no more than eight actions and every action kind must match that exact frozen allowlist; `action_policy=none`, unknown capabilities and malformed or excessive proposals fail before publication. The projection worker derives stable approval, operation and audit-event identities from the invocation, binds canonical input and evidence/result digests, and uses one security-definer transaction to commit the Persona Message and independent-review Attention approvals. Work-originated Runs retain their Work identity; direct Boardroom Runs produce Account-scoped approvals. Projection never executes the proposed effect. Exact retry must find the same immutable approval and event or fails as a conflict, and the worker retains execute-only authority with the superseded projection surfaces revoked. OpenAPI and generated TypeScript expose action-capability configuration. Pure policy/projection tests plus fresh PostgreSQL 17 integration prove allowlist denial, deterministic identities, one approval/event on replay, Account isolation, least privilege and rollback of the entire Message/Run/Attention transaction. Full local Docker, race, website build/lint/audit, object-policy, movement and erasure certification pass. Owner-question wait/resume, deterministic plan expansion and remaining queue/operator recovery remain open.

Owner-question checkpoint (2026-08-22): result-policy version 3 now permits at most eight owner questions per completed turn while preserving replay of versions 1 and 2. For a Work-originated Run, successful projection atomically commits the Persona Message, creates one exact Account-scoped Attention `InformationRequest` per question, transitions Work from `in_progress` to `waiting`, and moves the Run to `waiting_input`. Stable request, Attention-event and Work-event identities derive from the invocation; each fact key is the SHA-256 prefix of the exact UTF-8 question and PostgreSQL independently recomputes that binding. Immutable approval and question timestamps derive from the authenticated runner submission, so a projection retry after an uncertain commit cannot drift. A direct Boardroom Run remains conversational and publishes questions without fabricating a Work lifecycle. Once the latest request cohort is answered through independently supported accepted Knowledge Facts, the Work-owned resumption boundary closes the waiting Run, returns Work to `in_progress` and queues a new deterministic Persona execution with a bounded, explicitly untrusted owner-fact appendix. Fresh pure-policy/projection and PostgreSQL 17 integration coverage proves the pause/request/fact/resume/continuation path, Work-versus-Boardroom behavior, deterministic identities and the eight-question bound. The complete `ubunturojo` Docker certificate passes normal and race tests, migrations, vet, formatting, API contracts, website build/lint/audit and object-policy checks. Deterministic plan expansion, schedules and remaining queue/operator recovery remain open.

- Complete workspace/Boardroom configuration and immutable Persona/version lifecycle.
- Implement deterministic plan expansion beyond the frozen plan and complete structured recovery; multi-turn orchestration, manager synthesis, bounded delegation and resumable owner questions are constructed.
- Implement deterministic context assembly from Work, Knowledge, Baseline and Conversation watermarks.
- Complete provider-neutral invocation contracts, budget/cost/token enforcement and model fallback policy.
- Complete bounded sequential tool execution, tool-output validation, citation binding and result publication.
- Add schedules; document attachments and governed consequential proposals are complete.
- Add workflow/replay durability where long-lived orchestration requires it.
- Complete dispatch/projection dead-letter, retention and operator recovery paths.

Exit: Agent behavior is durable, replay-safe, Account-isolated and recoverable across every crash/provider boundary.

## P3.4 — Scheduling, business packages and integrations

Complete every intended launch package rather than leaving preview shells:

- Scheduling: durable definitions, time zones, missed-run policy, leases, pause/resume and idempotent dispatch.
- Finance: governed records, summaries, reconciliations, evidence and approved external effects.
- Marketing: campaign/asset/workflow capabilities defined by final product requirements.
- Email: inbound/outbound boundaries, threading, credentials, retries and approval rules.
- Google Drive and document sources: scoped credentials, sync cursors, revocation and deletion.
- Web research: safe retrieval, source attribution, policy and failure handling.
- Integrations package: connector health, capability grants, provider degradation and credential lifecycle.

Every package receives Catalog features, entitlement/downgrade behavior, HTTP/MCP/UI surfaces, schedules/workers/tools, observability, retention and acceptance tests.

Exit: the Catalog, website claims and application capabilities reconcile exactly.

## P3.5 — Final transports and product surface

- Implement one canonical application boundary per use case.
- Publish complete generated HTTP contracts and sanitized OpenAPI artifacts.
- Implement production MCP tools over the same commands/queries and prove HTTP/MCP outcome parity.
- Build the private feature-organized React application for Account, Work, Your Turn, Knowledge, Baseline, Agents, Finance, Marketing, integrations, billing and security.
- Add reliable streaming/event replay, optimistic concurrency, draft preservation and session/network recovery.
- Complete keyboard, screen-reader, contrast, zoom/reflow, forced-colors, reduced-motion and supported-device behavior.
- Remove prototype compatibility adapters only after migration evidence and rollback windows close.

Exit: no production use case depends on prototype runtime code, manually duplicated models or transport-specific authorization.

## P3.6 — Data migration and operational completion

- Inventory and characterize every retained prototype route, MCP tool, workflow, schedule, table and external object.
- Build Account-cohort migration tools with checksums, row/object/index counts and rollback checkpoints.
- Reconcile Membership, placement, entitlements, Work, Knowledge, Baseline, Agents, schedules, actions and provider references.
- Extend the Phase 2.5 PostgreSQL Account-movement and existing erasure/restore foundations across every new enabled object, search, workflow and provider-reference store; no store may ship without copy/reconciliation/rollback/retirement and erasure/replay handlers.
- Complete dashboards, alerts, runbooks, capacity limits, security/privacy review and support procedures.
- Run clean-environment, restored-environment, load/fairness, provider-degradation, game-day and soak suites.

Backup scheduling/storage is configured by the project owner per environment after the application/database topology is present. Phase 3 must supply consistent snapshots, restore checkpoints, replay tooling and documented hooks so that configuration is verifiable.

Exit: one synthetic and one internal Account complete migration, operation, backup/restore verification and rollback without prototype dependencies.

## P3.7 — Linode production certification and release

1. Apply the reviewed LKE controllers and overlays.
2. Hostinger-certify the final Phase 3 application and website digests, then deploy that exact pair to a non-customer Linode environment.
3. Apply production migrations and containerized PostgreSQL clusters.
4. Run NetworkPolicy, workload identity, admission, RuntimeClass, HPA, pod/node loss, database failover, key/CA rollover and runner-compromise tests.
5. Complete real provider, accessibility, isolation, load, restore and full-product journeys.
6. Obtain engineering/product/security/operations approval; in this one-person project the approvals are explicit recorded owner decisions, not implied by a successful command.
7. Deploy internal Accounts, then a bounded customer canary, then general availability after the observation window.
8. Retain and rehearse rollback for both application and website digests plus compatible schema/Catalog/configuration.

## Dependency order

```text
Phase 2.5 platform/deployment final form
  -> Work + Attention
  -> Knowledge + Baseline
  -> complete Agent workspace/execution
  -> Scheduling + Finance + Marketing + integrations
  -> final HTTP/MCP/React surfaces
  -> prototype migration + operational completion
  -> LKE certification + production release
```

Some implementation can proceed in parallel, but no downstream package may invent a second identity, Account, entitlement, placement, action or execution boundary.

## Phase 3 completion rule

Phase 3—and therefore application construction—is complete only when:

- every prototype capability marked `retain` or `replace` maps to a production use case and acceptance test;
- Work, Attention, Knowledge, Baseline, Agents, Scheduling, Finance, Marketing and integrations are complete;
- HTTP, MCP, schedules, workers, runners and UI share canonical authorization and entitlement outcomes;
- all production data is Account-isolated, movable, exportable, restorable and erasable;
- the final React application and public site pass the complete accessibility/device matrix;
- containerized PostgreSQL and all application workloads pass local, Hostinger and LKE certification appropriate to each environment;
- production backup/restore configuration has recorded owner verification;
- the exact signed artifact pair passes migration, load, resilience, security/privacy, canary and rollback gates; and
- no production process imports, executes or depends on the prototype or preview-hosting runtime.
