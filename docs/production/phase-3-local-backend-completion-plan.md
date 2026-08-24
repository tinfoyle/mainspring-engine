# Phase 3 local backend completion plan

- Plan date: 2026-08-23
- Starting revision: `ede239d`; LB0 and the application-owned Google credential lifecycle are now committed in the local Phase 3 history
- Purpose: complete the remaining product backend locally, establish an objective backend-to-UI handoff boundary, and move overall feature construction to approximately the final ten-percent customer-interface window
- Parent plan: [Phase 3 final construction plan](phase-3-report.md)

## Decision

The next Phase 3 execution window is local backend completion. It does not build or promote release images, push commits to GitHub, mutate Hostinger Stage, use an LKE kubeconfig, or deploy production infrastructure. Local commits are permitted so the work remains checkpointed without triggering remote CI or deployment activity.

For this plan, **backend** includes:

- domain rules, application use cases and canonical authorization;
- PostgreSQL migrations, repositories, forced RLS, queues and workers;
- provider-neutral ports and real provider adapters;
- OAuth and provider-credential lifecycle boundaries;
- HTTP and MCP operations over the same application services;
- generated OpenAPI, Go and TypeScript contracts needed by the later UI;
- Catalog/package execution policy, observability and operator recovery;
- Account movement, portability, erasure and restore participation; and
- complete deterministic local Docker fixtures and certification.

The final Vue customer surface—including public acquisition, feature/package education, checkout, the private SPA, mobile visual design and applied browser/accessibility/device acceptance—is outside this completed backend plan and governed by the [Phase 3 Vue SPA product-surface plan](phase-3-vue-spa-plan.md). Existing browser surfaces may be used for diagnostics, but their visual completion cannot block the backend boundary.

## Starting position and completed position

At the start of this window the machine-readable package inventory marked Work, Agents, Knowledge and Finance executable while Marketing and Integrations were deliberately closed. The completed local certificate now marks all six packages executable with their exact browser, HTTP, MCP, Agent/action and worker boundaries.

The repository already contains:

- complete Work, Attention, Knowledge, Baseline, Agent, Scheduling and Finance backend paths;
- governed Marketing aggregates, persistence, HTTP/MCP contracts, Agent draft tools and approval-driven activation;
- the provider-neutral Integration connection, credential-attestation, health, delivery, reconciliation and manual-resolution boundaries;
- real outbound SMTP and exact HTTPS publication adapters;
- scoped email, Google Drive and web-research connector kinds in the domain model;
- an immutable prototype transformation/import path and Account-portability coverage; and
- the Google Drive grant, sync queue, sealed cursor, Knowledge admission/deletion and prior-membership checkpoints already committed through `ede239d`.

LB0 is certified and committed. The production Drive API adapter, exact direct-child crawl, incremental changes/removals, Workspace export, health probe, worker composition, local fixture, least-privilege storage/database policy and safe source resurrection all passed the complete local Docker gate.

The provider gaps identified at the start were:

1. exercising OAuth-authorized Drive through complete source sync and Knowledge settlement;
2. adding the governed `email.read` inbound source adapter;
3. adding hardened `web.research` retrieval/capture and public contracts;
4. certifying outbound SMTP/HTTPS execution and recovery locally; and
5. opening Marketing and Integrations only after every advertised provider lifecycle passed.

LB1 through LB5 close those gaps. LB6 extends movement/export/erasure/restore coverage, LB7 records the complete local gate, and LB8 publishes the revision-controlled interaction guide before the required stop.

## Non-goals for this window

The following work is deliberately deferred, not silently discarded:

- Git pushes and the GitHub Actions activity they trigger;
- GHCR image construction, signing, attestation, vulnerability admission and release records;
- any Stage secret, database, container, Catalog or application mutation;
- live-provider Stage acceptance;
- LKE inventory, controller installation, overlay application or production database creation;
- production backup configuration, canary, observation window and general availability;
- the final Vue public/private information architecture and mobile-first visual system;
- mobile layout refinement and applied browser, screen-reader and supported-device certification; and
- final production security/privacy, load, game-day, soak and rollback approval.

## LB0 — certify the in-progress Drive execution checkpoint

Finish the work already present before opening another provider surface:

1. Retain exact folder scope, no shortcut traversal, bounded page/content limits and fixed Google origins.
2. Complete initial-snapshot, change-feed, move, inaccessible, deletion and return-after-deletion semantics.
3. Preserve immutable Knowledge history when a source returns; only a new processed revision may resurrect the document.
4. Prove connector database and object-store permissions permit only the exact source operations required.
5. Run the uncached Go, race, vet, formatting, migration, object-policy, Compose-render, API-contract and website gates through `ubunturojo` Docker.
6. Align the Integration and Phase 3 reports with the constructed adapter and worker.
7. Commit the certified checkpoint without bundling provider credentials or environment evidence.

Exit evidence:

- the complete local gate passes from the current worktree;
- both local connector workers are healthy;
- an interrupted page never advances its sealed cursor;
- a removed then restored file becomes a new immutable ready revision; and
- the repository documentation no longer describes the Drive adapter or worker as absent.

Status on 2026-08-23: **complete.** The real adapter, health probe, worker composition, source-object role and local fixture are constructed. The complete `ubunturojo` Docker gate passed uncached and race-enabled Go tests, 109 fresh migrations, vet, formatting, generated-contract drift, process inventory, object-policy verification and website checks. Exact connector source create/read/delete permission is certified while bucket listing, derived Knowledge writes and Marketing mutation remain denied. Interrupted pages retain their prior sealed cursor, and removal followed by return creates a new immutable ready revision without rewriting deleted history. This checkpoint is committed locally with no provider credential or environment evidence and without a Git push or Stage mutation.

## LB1 — construct the provider credential and OAuth control plane

Make provider authorization a product backend capability rather than an operator-authored refresh-token file:

1. Define one manager-authorized Integration authorization-session aggregate bound to Account, connection, provider, requested scope revision, redirect origin, expiry and one-use state.
2. Implement Google server-side authorization-code exchange with exact state, redirect and scope binding; deny scope widening and open redirects.
3. Store refresh material only behind an encrypted provider-secret port. PostgreSQL retains opaque identity, generation, digest, status and audit evidence, never the secret.
4. Rotate credential generation transactionally when Google returns replacement material. Revoke the broker object and durable credential binding together through a replay-safe workflow.
5. Preserve the existing one-operation worker lease so a connector receives only the exact credential generation and purpose it claimed.
6. Add canonical HTTP operations for begin, exact redirect callback completion, status and revoke. Expose only the safe lifecycle operations through MCP where product policy permits; MCP must not become a second callback or token-exchange boundary. The later UI will call these operations rather than own OAuth state.
7. Extend Account movement, export omission, erasure, restore gates, metrics and runbooks for the new records and secret-store references.
8. Provide a deterministic local OAuth server fixture covering consent, code replay, expiry, scope mismatch, rotation, revocation and provider outage.

Status on 2026-08-23: **complete.** The durable authorization-session aggregate, encrypted provider-secret port, fixed-origin Google protocol adapter and complete manager/recent-passkey application lifecycle are constructed. Begin freezes exact authority; callback claims state once, seals returned refresh material and converges activation/rotation without repeating an uncertain provider effect. Safe status persists expiry without exposing protocol digests. Reviewed Google callback denial is terminal and replay-safe. Migration 74 adds a forced-RLS, non-portable revocation ledger: provider confirmation precedes vault fencing, and vault fencing precedes the guarded serializable credential/connection revocation. Provider outage stays retryable without early fencing, while unsettled workflows block competing connection authority changes. Canonical HTTP exposes begin, callback, status and revoke; MCP exposes only begin, status and revoke, so no authorization code or token can cross the MCP boundary. Signed routing carries recent passkey evidence to both transports. A generated local-only 32-byte vault key, deterministic OAuth/Drive fixture and Caddy-routed `oauth.infiniteocean.localhost` journey prove connection creation, consent, PKCE code exchange, callback activation, secret-free status and provider/vault/database revocation without a manually provisioned refresh file. Focused race, fresh PostgreSQL and Docker tests cover activation/replay, generation-two rotation/revocation, changed code/state/denial, concurrent exchange leases, scope failure, ambiguous writes, unknown outcomes, expiry, provider-outage retry, required fence ordering, RLS, portability omission, exact erasure coverage, immutable redacted events and PostgreSQL timestamp normalization across all 112 migrations. `make verify-google-oauth` is the repeatable local certificate. No Git push or Stage/release environment mutation is part of this checkpoint.

Exit evidence:

- no manually created Drive refresh file is required for a local product journey;
- replay, cross-Account state, redirect drift and scope widening fail closed;
- rotation fences the previous generation before new work is leased;
- revocation stops new sync without rewriting capture history; and
- secret bytes never enter PostgreSQL, logs, events, telemetry, HTTP/MCP results, exports or Git.

## LB2 — close Google Drive as a local product feature

Exercise the OAuth and source-sync paths as one backend journey:

1. Run a containerized fake Google OAuth/Drive service with protocol-real token, folder, file, export and change endpoints.
2. Cover direct-child initial crawl, pagination, ordinary updates, moves into and out of scope, deletion, loss of access, restoration, revoked credentials and cursor restart.
3. Cover binary files plus supported Google Workspace PDF exports and explicit unsupported/oversized outcomes.
4. Prove captured bytes pass through the ordinary Knowledge quarantine, malware scan, extraction, indexing and workload-only publication boundary.
5. Prove exact Drive health gates sync and that stale/unavailable health postpones rather than consumes work.
6. Prove Baseline source grants cannot widen the connection folder scope and only admitted capture evidence can satisfy governed evidence selection.
7. Add content-free health, queue-age, retry, removal and capture metrics with bounded operator recovery.

Status on 2026-08-24: **complete.** The protocol fixture and adapter suites cover direct-child crawl, pagination, incremental updates, movement, deletion/access loss, restoration, Workspace export, unsupported and oversized content, health degradation and cursor settlement. The composed Docker certificate now joins that coverage to the application-owned OAuth lifecycle: it authorizes a Drive connection without a manually provisioned refresh token, leases the encrypted generation to the real connector adapter, captures exactly one immutable source revision, passes it through the ordinary Knowledge scan/extract/index pipeline, publishes it under the exact internal source workload, and then proves revocation leaves history intact while preventing more sync work. The certificate exposed and corrected two orchestration defects: stale mock workers could consume seeded work, and the Knowledge worker lacked the narrow document-row update needed to publish a ready source revision. Forced Account RLS remains active. `make verify-google-oauth` now certifies the complete OAuth-to-Knowledge-and-revocation path; focused race tests prove fixture protocol use and publication without an external routed authorization context. This checkpoint is local only and does not push, release or mutate Stage/LKE.

Exit evidence:

- the complete local OAuth-to-Knowledge journey is deterministic and replay-safe;
- every admitted/deleted capture has exact content-free provenance;
- revocation and package/placement drift stop new work; and
- no Drive identifier, name, content or credential appears in operational telemetry.

## LB3 — implement inbound email capture

Complete the already modeled `email.read` capability independently of outbound campaign delivery:

1. Freeze the minimum mailbox/folder and optional date scope required by a source grant; never infer an Account-wide mailbox scope.
2. Add an implicit-TLS IMAP provider port with UIDVALIDITY/UID-based cursor semantics, bounded pagination and exact message identity.
3. Parse a bounded MIME subset, preserve threading/source attribution, admit supported bodies and attachments through Knowledge, and reject executable, malformed or oversized material before admission.
4. Treat expunge, folder movement, UIDVALIDITY reset, credential revocation and partial page failure explicitly; cursor advancement remains capture-after-settlement.
5. Reuse the provider credential control plane and one-operation broker with a purpose that cannot send email.
6. Add a no-network unit fixture plus a containerized TLS mail fixture for initial sync, incremental mail, attachment, deletion, reset, outage and revocation tests.
7. Add HTTP/MCP connection and source-grant contract coverage without exposing message content on Integration status surfaces.

Status on 2026-08-24: **complete.** Migration `000075` extends the private cursor/capture queue to exact active email grants while retaining Drive checks. The implicit-TLS IMAP adapter implements UIDVALIDITY/UID cursor semantics, bounded pagination and reviewed MIME body/attachment admission. Claims freeze one-to-twenty mailbox folders and optional UTC date bounds, and completion repeats the current connection/revision/credential/folder authority before advancing a cursor. Provider `imap` material is available only for `email.read` sync/health and remains unusable for `email.send`. The composed TLS fixture covers initial/incremental mail, attachment, expunge, UIDVALIDITY reset, outage and revocation. `make verify-imap` passes the capture-to-immutable-Knowledge journey.

Exit evidence:

- an exact scoped mailbox can populate and update Knowledge locally;
- `email.read` material cannot invoke `email.send` and vice versa;
- thread/source evidence remains attributable without leaking into metrics; and
- retries cannot duplicate documents, revisions or deletion effects.

## LB4 — implement hardened web research

Turn the existing `web.research` kind into a bounded evidence-source capability:

1. Define immutable allowed-origin, path, content-type, byte/time and redirect policy; deny generic arbitrary-URL execution.
2. Resolve and pin public addresses, reject private/link-local/metadata destinations, validate every redirect and disallow credential forwarding across authority changes.
3. Bound DNS, connection, header, body and decompression work; accept only reviewed document/media types.
4. Freeze canonical URL, retrieval time, response digest, declared/verified type and source attribution before Knowledge admission.
5. Treat retrieved material as evidence, never an automatically accepted Fact, and bind any Baseline use to the ordinary human evidence decision.
6. Add deterministic local HTTP/TLS fixtures for valid content, redirect chains, DNS rebinding attempts, private-address denial, decompression bombs, oversized bodies, timeouts, unsupported types and changed content.
7. Add health, degradation, revocation, HTTP/MCP contracts and content-free operator recovery.

Status on 2026-08-24: **complete.** Immutable connection scope, a fixed-purpose Firecrawl adapter and the hardened retriever enforce reviewed origins/paths, public-address DNS pinning, redirect revalidation and bounded DNS/connect/header/body/decompression/type/time work. Search returns safe metadata. Read freezes canonical URL, retrieval time, digest, type and attribution before immutable Knowledge admission. HTTP and MCP share the application service; read additionally requires Knowledge mutation authority. The deterministic HTTPS fixture proves SSRF denial, health degradation, exact replay, changed-content revision creation and secret isolation. `make verify-web-research` passes.

Exit evidence:

- the adapter passes an SSRF and resource-exhaustion regression suite;
- every accepted result carries verifiable source attribution and digest;
- provider failure degrades only the dependent research operation; and
- research output cannot bypass Knowledge or Baseline governance.

## LB5 — close Marketing and Integrations execution locally

Bring the two remaining non-executable packages through their complete local backend lifecycle:

1. Certify outbound SMTP and exact HTTPS publication against disposable TLS providers, including success, definite no-effect, ambiguous outcome, reconciliation, bounded re-execution and manual resolution.
2. Exercise credential rotation/revocation, health staleness, package downgrade, Account placement drift and approval expiry across all real adapters.
3. Confirm Marketing release preparation reconstructs the exact immutable asset manifest and that no connector can widen the approved release.
4. Complete provider-specific retention and deletion behavior plus queue/operator views and runbooks.
5. Expose every retained Integration use case through canonical HTTP and MCP services, including `web.research`, without transport-owned authorization logic.
6. Reconcile generated OpenAPI, Go and TypeScript contracts and the package/process/surface inventories.
7. Mark Marketing and Integrations executable only after all positive and negative local gates pass.

Status on 2026-08-24: **complete for the local construction boundary.** The outbound connector certificate proves exact Marketing execution preparation, fresh content-free health, execute settlement, database/object/credential least authority and worker readiness. SMTP and HTTPS adapter suites retain exact scope, immutable payload, ambiguous outcome, digest reconciliation and no-blind-resend behavior. The OAuth/Drive, IMAP and web-research certificates cover the remaining advertised connectors. Generated contracts now contain 188 operations and the complete Integrations MCP composition contains 20 tools. Marketing and Integrations are executable in the machine inventory. Applied Stage or production provider credentials remain environment release evidence, not local backend construction.

Exit evidence:

- every product package in `deploy/package-surface-inventory.json` is executable;
- each advertised connector has a real adapter, deterministic fixture, health producer and recovery path;
- local Catalog enable/read-only/disable behavior matches worker admission; and
- Marketing activation through external delivery is exact, approval-bound and replay-safe.

## LB6 — cross-package backend closure

Close integration-created drift across the already constructed platform:

1. Extend Account movement, erasure, portability and restore inventories for every new OAuth, email and research record/object.
2. Verify the Account export includes customer-owned portable provenance/content and excludes credentials, queue leases, cursor secrets and broker references.
3. Reconcile the prototype inventory so every retained backend behavior maps to a final use case or an explicit reviewed retirement; unresolved customer evidence remains human-review work, not an automatic import.
4. Exercise Baseline maintenance through the existing schedule worker and prove deterministic renewal/reassessment Work creation.
5. Run provider-degradation and queue-recovery scenarios across Drive, email, research, SMTP and HTTPS publication.
6. Ensure operational metrics, alerts and status endpoints remain content-free and identify each bounded recovery action.
7. Remove backend compatibility code only when its final replacement and migration/rollback evidence exist.

Status on 2026-08-24: **complete for the new provider state and the local backend boundary.** Movement/namespace fencing covers the durable Integration connection, OAuth, capture and research families while derived queues are regenerated. Account export carries reviewed non-secret connection/capture provenance and content while excluding credentials, security digests, leases, sealed cursors and broker references. Erasure policy and fresh PostgreSQL tests include web-research captures and every source queue. The complete migration, portability, erasure, object-policy and process/package inventory suites pass with no executable prototype dependency.

Exit evidence:

- fresh, retained, moved, exported, restored and erased Accounts reconcile across all product packages;
- no enabled backend use case imports or executes prototype runtime code;
- every queue has bounded retry, dead-letter/unknown handling and an operator recovery path; and
- the machine inventories contain no unexplained product surface or process gap.

## LB7 — certify the backend-to-UI boundary

Run one final local certificate before opening the customer-interface workstream:

1. Apply every migration to fresh global, cell A and cell B PostgreSQL instances and upgrade the retained local databases without replacement.
2. Run all Go tests uncached, the race suite, vet, formatting, generated-contract drift, migration/portability/erasure suites and package/process inventory checks.
3. Run MinIO, database-role, workload-TLS, provider-egress and secret-mount positive/negative policy gates.
4. Run complete local HTTP/MCP parity journeys for every package, including OAuth and provider failure/recovery.
5. Run bounded concurrency, lease-loss, unknown-commit, provider-degradation and worker-restart tests.
6. Produce a content-free, revision-bound backend completion report listing every use case, canonical operation, worker/adapter and acceptance test.

Status on 2026-08-24: **complete.** `go test ./...`, the full Docker `make test` gate, provider certificates, generated-contract drift, migration/RLS/movement/export/erasure, object policy, website and package/process inventory checks pass from UbuntuRojo. The revision-controlled [backend completion report](phase-3-backend-completion-report.md) records the use-case/operation/worker/adapter/acceptance matrix and the deliberate UI/release exclusions.

The backend feature boundary is reached only when all of the following are true:

- Work, Agents, Knowledge, Finance, Marketing and Integrations are executable in the machine inventory;
- Scheduling and Baseline maintenance execute through their canonical workers;
- Drive, inbound email, outbound email, HTTPS publication and web research have real adapters plus deterministic local conformance suites;
- all customer backend operations exist in shared application services and are exposed consistently through HTTP and MCP where product policy calls for both;
- generated contracts are stable enough for the final Vue acquisition and private application surfaces to consume without inventing domain behavior;
- all Account-owned state is isolated, movable, exportable where appropriate, restorable and erasable;
- no known backend feature is deferred into the UI implementation; and
- the complete local certificate passes from a clean checkout with no external provider or deployment dependency.

Reaching this boundary is expected to represent approximately 85-90% of application feature construction. It does not mean production-ready, because customer-interface completion and environment certification still follow.

## LB8 — publish the API and MCP interaction guide, then stop

Only after LB7 passes, create a revision-controlled API/MCP guide from the generated contracts and the certified runtime. The guide must document:

- the local base origins, authentication methods and Account-selection/routing model;
- the generated HTTP operation inventory grouped by product use case;
- request/response schemas, opaque pagination, weak ETags, `If-Match` and route-bound idempotency requirements;
- OAuth begin/callback/status/revoke behavior and recent-passkey requirements;
- the MCP discovery, authorization, consent, PKCE, token rotation/revocation and JSON-RPC flow;
- every MCP tool grouped by package, with read/mutation classification and equivalent HTTP operation where one exists;
- package, role, object and read-only outcome matrices;
- standard safe error outcomes, retry guidance and unknown-result reconciliation rules;
- queue, health, degraded, conflict and recovery states a future client must present;
- deterministic local fixtures and worked `curl`/JSON-RPC examples that contain no credentials or customer data;
- generated Go and TypeScript client locations and regeneration checks; and
- operator-only boundaries that must never be surfaced as customer actions.

The guide must be verified against the local HTTP and MCP services rather than inferred only from OpenAPI. Its examples must pass as a local documentation test or executable fixture.

Status on 2026-08-24: **complete.** The revision-controlled [API and MCP interaction guide](api-mcp-interaction-guide.md) documents the 188-operation generated HTTP inventory, all 94 published MCP tools (89 routed cell tools plus five global export tools), local origins, session/OAuth routing, package/role/read-only behavior, safe retry/recovery rules and deterministic local fixtures. `TestAPIMCPGuideMatchesGeneratedInventories` fails on operation/tool-count drift, missing published tools, missing required protocol contracts or invalid fenced JSON examples.

After the backend completion report and API/MCP guide are committed locally, this execution window stops. It does not begin the customer-facing UI workstream automatically. UI planning and implementation resume only under a later explicit direction. GHCR, Stage, LKE and production remain later release work after both backend and UI feature completion.

## Execution order

```text
LB0 certify current Drive checkpoint
  -> LB1 provider credential/OAuth control plane
  -> LB2 complete Google Drive locally
  -> LB3 inbound email capture
  -> LB4 hardened web research
  -> LB5 enable Marketing + Integrations locally
  -> LB6 cross-package backend closure
  -> LB7 backend-to-UI boundary certificate
  -> LB8 API + MCP interaction guide
  -> STOP

Later explicit workstream:
  customer-facing UI
  -> artifact, Stage, LKE and production certification
```
