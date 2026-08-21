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
- Agent orchestration is limited to one ordered Persona pass;
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

### Connect Work to Agent execution

Implement a Work-owned claim/start/link/heartbeat/release/resume/reconcile state machine. Every crash point must converge to one linked active Run or one safely requeued Work item. Existing Agent dispatch and runner capacity are reused but do not replace this boundary.

### Finish consequential actions

- Make Attention the owner of approval projections consumed by the action ledger.
- Add the executor registry, definite-failure retry policy, unknown reconciliation and dual-controlled manual resolution.
- Add at least one real consequential adapter with idempotency and side-effect-free lookup.
- Add redacted customer/operator views, alerting and recovery runbooks.

Exit: Work, Your Turn and approved external actions are complete through domain, persistence, HTTP, MCP and UI.

## P3.2 — Knowledge and Baseline

- Implement source-attributed facts, claims, evidence, revisions, scope and confidence.
- Implement document upload, malware/type/size checks, extraction, chunking, indexing, retention and deletion.
- Implement Account-scoped retrieval, citation validation and bounded result contracts.
- Implement the Baseline interview/state machine, evidence decisions, readiness, renewal and reassessment.
- Implement evidence-source selection and plan generation without allowing unsupported Agent output to become authoritative fact.
- Migrate and reconcile prototype knowledge/documents/baselines with object, index and citation checks.

Exit: source-attributed business memory and baseline claims are complete and production-certifiable.

## P3.3 — Workspace and Agent execution

- Complete workspace/Boardroom configuration and immutable Persona/version lifecycle.
- Implement multi-turn orchestration, manager synthesis, delegation, resumable owner questions and structured recovery.
- Implement deterministic context assembly from Work, Knowledge, Baseline and Conversation watermarks.
- Complete provider-neutral invocation contracts, budget/cost/token enforcement and model fallback policy.
- Complete bounded sequential tool execution, tool-output validation, citation binding and result publication.
- Add schedules, attachments and governed consequential proposals.
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
