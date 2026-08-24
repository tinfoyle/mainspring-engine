# Phase 3 local backend completion plan

- Plan date: 2026-08-23
- Starting revision: `ede239d` plus the reviewed Google Drive work currently in the local worktree
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

The final customer-facing React implementation, mobile visual design and applied browser/accessibility/device acceptance are outside this plan. Existing browser surfaces may be used for diagnostics, but their visual completion cannot block the backend boundary.

## Starting position

The machine-readable package inventory currently marks Work, Agents, Knowledge and Finance executable. Marketing and Integrations remain deliberately non-executable.

The repository already contains:

- complete Work, Attention, Knowledge, Baseline, Agent, Scheduling and Finance backend paths;
- governed Marketing aggregates, persistence, HTTP/MCP contracts, Agent draft tools and approval-driven activation;
- the provider-neutral Integration connection, credential-attestation, health, delivery, reconciliation and manual-resolution boundaries;
- real outbound SMTP and exact HTTPS publication adapters;
- scoped email, Google Drive and web-research connector kinds in the domain model;
- an immutable prototype transformation/import path and Account-portability coverage; and
- the Google Drive grant, sync queue, sealed cursor, Knowledge admission/deletion and prior-membership checkpoints already committed through `ede239d`.

The current uncommitted checkpoint adds the production Drive API adapter, exact direct-child crawl, incremental changes/removals, Workspace export, health probe, worker composition, local fixture, least-privilege storage/database policy and safe source resurrection. Focused tests and policy/render checks passed, but the full local Docker gate was interrupted and must be rerun before that checkpoint is accepted.

The remaining backend gaps are concentrated in provider completion:

1. the Drive checkpoint is not yet fully certified or committed;
2. Google consent, callback, code exchange, refresh-token rotation and revocation are not a product-owned lifecycle;
3. `email.read` has a governed scope but no inbound source adapter;
4. `web.research` exists in the domain but has no hardened retrieval/capture adapter or public contract;
5. the real SMTP/HTTPS execution path still needs a complete disposable-provider local certificate; and
6. Marketing and Integrations cannot become executable until every advertised provider path and lifecycle is locally complete.

## Non-goals for this window

The following work is deliberately deferred, not silently discarded:

- Git pushes and the GitHub Actions activity they trigger;
- GHCR image construction, signing, attestation, vulnerability admission and release records;
- any Stage secret, database, container, Catalog or application mutation;
- live-provider Stage acceptance;
- LKE inventory, controller installation, overlay application or production database creation;
- production backup configuration, canary, observation window and general availability;
- the final React information architecture and visual system;
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

Status on 2026-08-23: **in progress.** The durable authorization-session aggregate and migration are complete. The provider-secret port now also has a local production-shaped AES-256-GCM filesystem implementation: its key, root and sealed files require restrictive non-symlink paths; authorization PKCE verifiers and credential generations are separated; writes are atomic and replay-safe; and rotation/revocation fencing stops further leases before later purge. Lease requests continue to require the exact Account, connection, credential generation, provider, reference digest, purpose, capability and expiry. Focused race tests prove ciphertext tamper denial, no plaintext at rest, exact replay/conflict behavior, expiry, rotation/revocation fencing, purge replay and Drive health/sync purpose separation. Google consent/code exchange, orchestration, lifecycle transports and the deterministic provider fixture remain.

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

The backend feature boundary is reached only when all of the following are true:

- Work, Agents, Knowledge, Finance, Marketing and Integrations are executable in the machine inventory;
- Scheduling and Baseline maintenance execute through their canonical workers;
- Drive, inbound email, outbound email, HTTPS publication and web research have real adapters plus deterministic local conformance suites;
- all customer backend operations exist in shared application services and are exposed consistently through HTTP and MCP where product policy calls for both;
- generated contracts are stable enough for the final React application to consume without inventing domain behavior;
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
