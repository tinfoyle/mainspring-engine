# Mainspring Production Rewrite Plan

- Status: Working production charter
- Prototype baseline: `8024f8d`
- Last updated: 2026-08-14

## 1. Purpose

The prototype proved the central Mainspring product loop:

1. Establish a source-attributed understanding of a business.
2. Turn missing knowledge and operating gaps into visible work.
3. Assign bounded work to durable, configurable agents.
4. Let agents use only explicitly granted tools.
5. Bring consequential decisions and unavailable private facts back to a person.
6. Preserve useful results as reusable business knowledge.

The production rewrite will preserve that loop while replacing accidental package boundaries, duplicated transports, oversized services, manual contracts, and prototype-grade operational assumptions with a modular, testable, observable system.

This is an incremental replacement program, not a greenfield reinvention. The prototype remains runnable under `prototype/` and acts as a behavioral reference until the last parity gate is passed.

## 2. Outcomes

The rewrite is successful when Mainspring can be changed and operated confidently under real customer load.

The production system must provide:

- Explicit module ownership and enforceable dependency direction.
- Tenant isolation that is testable at every entry point and data boundary.
- Durable, replay-safe orchestration for scheduled, conversational, and ticket-owned agent work.
- One canonical implementation of every use case shared by HTTP, MCP, schedules, and internal automation.
- Stable, generated API contracts rather than manually mirrored Go and TypeScript models.
- Idempotent, payload-bound external actions and deterministic recovery from ambiguous outcomes.
- Production authentication, authorization, secret storage, audit, backup, restore, monitoring, and incident procedures.
- A feature-organized, accessible React application with reliable streaming and error recovery.
- Safe database evolution with upgrade, rollback, reconciliation, and tenant-fleet rollout procedures.
- Enough observability to answer what happened, for which tenant, under whose authority, and how to recover it.

## 3. Product and architectural invariants

These are constraints, not implementation suggestions.

### 3.1 The application owns orchestration

Models may propose delegations and actions through structured output. Models do not start other agents, select arbitrary tools, mutate workflow state, decide authorization, or declare external actions complete.

### 3.2 Durable records are authoritative

PostgreSQL owns user-visible domain state. Temporal owns coordination history, retries, signals, and timers. Provider sessions, prompts, and runner files are transient implementation details.

### 3.3 Tenant identity is explicit

Every request, workflow, activity, repository, audit event, tool token, integration call, and provider invocation carries a validated tenant identity. Host routing alone is never authorization.

### 3.4 Providers are replaceable

The execution contract cannot depend on Codex-specific session behavior or output conventions. Provider-specific code is an adapter around the application invocation contract.

### 3.5 Tools are capabilities

Tool descriptions in a prompt are not permission. Authorization is enforced server-side against a short-lived, invocation-bound grant. Conditions can narrow document IDs, domains, folders, operation kinds, amounts, or other capability-specific scope.

### 3.6 External effects require authority and idempotency

Agents propose consequential effects. An authorized policy or person approves the canonical payload. Execution uses a durable idempotency key and records succeeded, failed, or unknown outcomes.

### 3.7 Human attention is typed

Requests for information, work-review decisions, and consequential approvals remain distinct concepts with different completion and resumption rules.

### 3.8 Business knowledge is attributable

Facts, documents, evidence, citations, revisions, and agent-produced artifacts retain source, author, time, scope, and history. Unsupported output does not silently become an authoritative business fact.

### 3.9 The prototype is a reference, not the new foundation

Production modules may reuse proven algorithms and tests. They must not recreate the existing god objects or presentation coupling under new directory names.

## 4. Prototype findings driving the rewrite

The current implementation is functionally rich but structurally concentrated:

- `prototype/internal/tenant` contains more than ten thousand handwritten lines and combines HTTP, application workflows, persistence, MCP, onboarding, work dispatch, finance presentation, and view-model assembly.
- `tenant.Server` has roughly 202 methods and registers approximately 135 routes.
- `tenant.Store` owns unrelated account, messaging, onboarding, baseline, work, and operational concerns.
- `boardroom.Service.ExecuteTurn` performs context building, policy construction, capacity admission, tool execution, citation binding, escalation, artifact publication, usage reconciliation, and approval projection in one method.
- `internal/tools` combines the capability-security kernel with approvals, owner input, shared knowledge, finance adapters, and action persistence.
- `/api/v2` response structures reuse server-rendered Templ view models, while TypeScript mirrors them manually.
- The React workspace concentrates routing, server-state behavior, streaming, and most pages in one source file.
- Workflow, runner, gateway, and scheduling behavior relies heavily on smoke coverage rather than focused replay and contract tests.
- Lifecycle fields and policies are often raw strings crossing handlers, services, and SQL.

The rewrite must address these causes. Merely splitting large files is insufficient.

## 5. Target shape

Mainspring will remain a modular monolith delivered as one versioned application image with multiple runtime modes. A module is a business boundary with owned concepts and persistence, not just a folder.

```text
cmd/
  mainspring/                 process entry point and subcommands
internal/
  bootstrap/                  mode-specific dependency composition
    control/
    gateway/
    tenantapi/
    worker/
    runnercontroller/
  platform/                   non-business infrastructure
    authn/
    authz/
    clock/
    config/
    database/
    eventing/
    httpserver/
    observability/
    secrets/
    tenancy/
    transactions/
  modules/
    identity/
    workspace/
    work/
    baseline/
    knowledge/
    execution/
    attention/
    scheduling/
    finance/
    integrations/
  transport/
    httpapi/
    mcp/
    legacyweb/                temporary compatibility only
api/
  openapi/
ui/
  src/app/
  src/features/
  src/shared/
deploy/
docs/
prototype/
```

Within a module, start with the smallest useful structure:

```text
modules/work/
  model.go                    typed entities, values, transitions
  service.go                  use cases
  ports.go                    interfaces required by the use cases
  postgres/                   repository implementation and queries
  module_test.go              package-level contracts
```

Add subpackages only when the module genuinely contains multiple cohesive concepts. Avoid a universal `models`, `services`, `repositories`, or `utils` package.

## 6. Module ownership

| Module | Owns | Does not own |
|---|---|---|
| Identity | Tenant users, roles, membership, invitations, sessions, ownership transfer, platform notices delivered to users | Boardroom permissions, provider identity, billing accounts |
| Workspace | Boardrooms, agent/persona configuration, immutable persona versions, conversations, messages, attachments | Invocation execution, provider calls, work-item lifecycle |
| Work | Work items, hierarchy, assignment, priority, due dates, lifecycle, provenance, agent-work claim/resume policy | Agent turn execution, owner approvals, document storage |
| Baseline | Assessment versions, interview state machine, evidence requirements, dispositions, reassessment policy | Generic documents, generic work items, integration credentials |
| Knowledge | Business facts, fact history, documents, chunks, revisions, evidence links, citations, provenance | Model orchestration, UI-specific search results |
| Execution | Run plans, invocation state, context budgets, tool loop, provider abstraction, result validation, usage admission | User-facing approval decisions, email delivery implementation |
| Attention | Human-input requests, coordinator questions, work review, approvals, canonical payload binding, action ledger | Work-item persistence, provider invocation |
| Scheduling | Schedule definitions, pause/resume, trigger policy, Temporal schedule adapter | Boardroom execution semantics |
| Finance | Ledgers, accounts, balanced journal entries, posting, voiding, finance audit events | Billing Mainspring subscriptions, arbitrary payment execution |
| Integrations | Email, Google Drive, web research, connector scopes, credential references, provider-specific delivery semantics | Capability authorization and tenant-wide business policy |
| Control plane | Tenant registry, runtime placement, provisioning state, subscriptions, platform operations | Tenant business records or tenant database credentials in query paths |

The detailed dependency and data design is in [architecture.md](architecture.md).

## 7. Dependency rules

1. Transport packages depend on public module use cases and DTO mappers.
2. Module application logic depends on its own domain types and consumer-owned ports.
3. Infrastructure adapters implement ports and are connected only in `bootstrap`.
4. One module cannot query another module's tables directly.
5. A module cannot import HTTP, MCP, React, Templ, Temporal, pgx, or a provider SDK into its domain model.
6. Cross-module synchronous calls use narrow interfaces defined by the consumer.
7. Durable asynchronous behavior uses explicit events or Temporal commands with versioned payloads.
8. Shared code is limited to technical primitives. Business concepts never move into `platform` merely because two modules reference them.
9. API DTOs are transport contracts and never double as database or domain models.
10. Automated architecture tests fail CI on forbidden imports.

## 8. Production execution pipeline

The current turn implementation will become an explicit pipeline:

1. `RunPlanner` loads the conversation and snapshots the ordered immutable persona versions.
2. `ContextAssembler` selects bounded messages, ticket context, active business facts, and authorized document scope.
3. `InvocationPolicy` calculates effective grants, budgets, provider settings, action policy, and citation requirements.
4. `CapacityGate` reserves concurrency, input/output tokens, and estimated cost.
5. `InvocationRepository` creates or resumes an idempotent invocation attempt.
6. `Provider` performs one bounded structured turn.
7. `ToolLoop` validates requests, invokes the governed broker, records results, and enforces count and byte ceilings.
8. `ResultValidator` validates schema, citations, delegation targets, proposed actions, and policy-specific output.
9. `ResultPolicies` apply deterministic product rules such as missing-information escalation and parent-ticket binding.
10. `ArtifactPublisher` preserves deliberate agent documents or a bounded fallback record.
11. One transaction persists the message, structured result, usage, citations, events, and proposed-action projections.
12. `RunFinalizer` schedules application-owned delegations, waits for typed attention, or completes the run.

Every stage receives typed input and produces typed output. Policy transformations are pure where possible. Provider or tool retries cannot duplicate messages, actions, documents, or financial records.

## 9. Contract strategy

The production HTTP API will be contract-first.

- OpenAPI is the source of truth for paths, requests, responses, errors, pagination, idempotency headers, and streaming discovery.
- Go transport types and the TypeScript client are generated or conformance-tested from the same schema.
- Domain types are mapped explicitly to transport DTOs.
- Errors use one problem-details envelope with stable machine codes and request IDs.
- Collection endpoints use bounded cursor pagination.
- Mutations that can be retried accept an idempotency key.
- Optimistic concurrency uses version numbers or ETags on records vulnerable to lost updates.
- SSE events use versioned envelopes, monotonic cursors, heartbeat behavior, and documented replay limits.
- MCP tools call the same use cases and authorization policies as HTTP; they are not a parallel business implementation.
- The prototype `/api/v2` contract remains behind a compatibility adapter until all React routes have migrated.

## 10. Data evolution

Already-applied prototype migrations remain immutable. Production migration work proceeds forward:

1. Build a schema inventory and mark the owning target module for every table and column.
2. Add repository contract tests against a real PostgreSQL instance.
3. Introduce new columns/tables additively.
4. Backfill in bounded, restartable batches with progress records.
5. Dual-read or compare old and new projections where a representation changes.
6. Reconcile counts, hashes, foreign keys, and lifecycle invariants per tenant.
7. Cut reads to the production repository behind a feature flag.
8. Cut writes only after shadow verification.
9. Retire compatibility columns in a later release after the rollback window.

The first production release retains physical control/tenant database separation and per-tenant database credentials. Deployment topology remains replaceable, but combining tenant databases or weakening the current isolation boundary is out of scope.

## 11. Delivery principles

- Ship vertical slices that can be verified independently.
- Keep the prototype executable throughout the program.
- Migrate one use case to one canonical implementation before moving the next.
- Put compatibility at the edges, never in new domain code.
- Add observability and tests before moving high-risk behavior.
- Do not assign calendar dates until team capacity and baseline test duration are measured.
- Each phase has an exit gate; unfinished exit criteria block the next dependent cutover.

## 12. Phases and exit gates

### Phase 0 — Preserve and measure the prototype

Status: checkpoint created; characterization work remains.

Deliverables:

- Preserve the prototype under `prototype/` and tag its last known-good revision.
- Run and record fast tests, frontend build, Docker full suite, and one real-provider canary.
- Inventory routes, MCP tools, workflows, activities, tables, status values, configuration, and external dependencies.
- Capture representative golden API/MCP payloads and state-transition fixtures.
- Measure test duration, startup time, database size, query latency, runner latency, and agent-run cost.
- Create a requirements traceability matrix connecting product flows to tests and modules.

Exit gate:

- The prototype can be built from a clean checkout.
- Every critical product flow has a named characterization test or an explicitly accepted gap.
- No committed credential or customer data exists in history introduced by the checkpoint.

### Phase 1 — Establish the production skeleton

Deliverables:

- Root Go module, production command, configuration loader, structured logging, metrics, tracing, and health/readiness endpoints.
- Mode-specific bootstrap packages with constructor validation and graceful shutdown.
- Platform tenant context, typed IDs, clock, transaction boundary, secret references, and standardized errors.
- Dependency-policy test and CI pipeline.
- Initial OpenAPI document and generated client workflow.
- Local development stack that can run prototype and production components side by side without port or database collision.

Exit gate:

- A minimal authenticated health/use-case path runs through transport, application, port, and adapter layers.
- CI runs formatting, static analysis, unit tests, contract checks, dependency checks, secret scanning, and build provenance generation.

### Phase 2 — Extract Work and Attention

Rationale: these modules form the operational spine and currently cross the most prototype boundaries.

Deliverables:

- Typed work item values and lifecycle transition table.
- Work repository, service, parent/child invariants, assignment policy, and query API.
- Agent-work claim/requeue/resume service using database leases and explicit recovery semantics.
- Human-input, coordinator, review, approval, and action-ledger services.
- Transactional projection from approved ticket proposals into work items.
- HTTP and MCP adapters calling the same use cases.
- Shadow comparison against prototype work queries and attention counts.

Exit gate:

- No production transport writes work or attention tables directly.
- Duplicate dispatch, duplicate approval, and process-crash recovery tests pass.
- Work and attention golden contracts match or have approved versioned differences.

### Phase 3 — Extract Knowledge and Baseline

Deliverables:

- Business-fact model with provenance, scope, sensitivity, confidence, confirmation, staleness, and immutable history.
- Document service with upload validation, extraction, revision history, chunking, citation identities, and retention policy.
- Baseline interview and evidence inventory as pure state machines.
- Evidence catalogs separated from SQL and presentation, versioned as governed data.
- Source adapters for uploads, public research, email, and Drive with explicit scopes.
- Baseline-to-work planning through the Work module API.
- Reassessment and renewal policies with deterministic clocks.

Exit gate:

- Every baseline transition has table-driven tests.
- Completing linked work updates evidence through a documented module contract.
- Knowledge written by one run is retrievable and citable by a later authorized run.

### Phase 4 — Rebuild Workspace and Execution

Deliverables:

- Workspace service for boardrooms, personas, immutable versions, conversations, attachments, and messages.
- Execution pipeline components described in Section 8.
- Provider conformance suite covering structured output, tool continuation, cancellation, timeout, usage, and error classification.
- Versioned Temporal workflows and activities with replay fixtures.
- Local dispatcher implementing the same command port for deterministic development.
- Capability broker reduced to authorization, definition lookup, invocation, bounds, and audit.
- Usage admission, circuit breaking, retry classification, and cost reconciliation.
- Policy packs for citations, document preflight, delegation, unknown answers, owner questions, and artifact publication.

Exit gate:

- Recorded prototype scenarios produce semantically equivalent production runs.
- Temporal replay passes against every released workflow history fixture.
- Provider and tool failures cannot create duplicate visible records or effects.
- Execution can be unit-tested with in-memory ports and no PostgreSQL, Temporal, or provider process.

### Phase 5 — Rebuild Scheduling, Finance, and Integrations

Deliverables:

- Scheduling module with explicit schedule/run relationship and timezone/DST tests.
- Finance module with typed money, immutable posted entries, reversals, period policy, and authorization.
- Integration framework with encrypted secret references, connector health, scope display, revocation, and reconciliation.
- Email delivery with durable outbox and unknown-outcome handling.
- Hardened web research adapter with SSRF protection, redirect checks, content limits, source attribution, and egress controls.
- Google Drive OAuth lifecycle, token rotation, folder scopes, and revocation tests.

Exit gate:

- Connector outages degrade only dependent capabilities.
- Finance invariants hold under concurrent writes and retries.
- External-action reconciliation has an operator-visible queue and runbook.

### Phase 6 — Replace tenant transports and React workspace

Deliverables:

- Thin HTTP route groups by feature.
- Thin MCP server registering use-case adapters.
- React application organized by feature and route.
- Router with nested layouts, not-found handling, route-level error boundaries, and navigation blocking for unsaved drafts.
- Query-key conventions, request cancellation, optimistic-update policy, retry policy, and normalized problem handling.
- One reusable SSE client supporting resume cursors, backoff, visibility changes, and terminal states.
- Accessible design system primitives, keyboard behavior, reduced motion, responsive navigation, and automated accessibility checks.
- Prototype compatibility routes only where migration remains unfinished.

Exit gate:

- Every core route uses generated API types.
- Browser end-to-end tests cover onboarding, work, conversation streaming, owner input, approvals, documents, finance, and session expiry.
- No production JSON DTO imports a server-rendered component model.

### Phase 7 — Production platform hardening

Deliverables:

- Production identity integration or hardened first-party authentication decision implemented.
- Key management and secret rotation.
- Backup automation, restore verification, tenant export, retention, and deletion workflows.
- SLO dashboards, alerts, audit search, operational consoles, and incident runbooks.
- Container signing, SBOM, dependency scanning, provenance, least-privilege runtime profiles, and admission policy.
- Load, soak, failure-injection, workflow-replay, migration-fleet, and disaster-recovery exercises.
- Staged tenant-fleet rollout and automatic rollback controls.

Exit gate:

- Production-readiness review in [quality-security-operations.md](quality-security-operations.md) is complete.
- A fresh environment and a restored environment both pass the release suite.
- On-call can diagnose and recover the documented failure modes without database improvisation.

### Phase 8 — Cutover and prototype retirement

Deliverables:

- Canary tenants moved through read shadowing, write cutover, and reconciliation.
- Compatibility traffic and mismatch dashboards reach agreed zero thresholds.
- Remaining legacy routes and adapters removed.
- Prototype made read-only, then archived after the rollback and audit window.
- Final data ownership map, API documentation, operational handbook, and architecture decision index published.

Exit gate:

- All tenants use production paths.
- No runtime dependency points into `prototype/`.
- Rollback, recovery, and customer-support procedures have been exercised.

The work breakdown and acceptance checklists are expanded in [delivery-plan.md](delivery-plan.md).

## 13. Initial production objectives

These are proposed engineering objectives to validate with observed prototype and beta traffic, not contractual promises:

- Tenant API availability: 99.9% monthly, excluding declared maintenance.
- Authenticated read latency: p95 below 300 ms when no external connector is involved.
- Authenticated write latency: p95 below 600 ms before asynchronous work begins.
- Durable run acceptance: p95 below 2 seconds from mutation acknowledgement.
- SSE reconnect: recover from the last acknowledged cursor without duplicate UI records.
- Duplicate consequential external effects: zero tolerated.
- Tenant-boundary authorization failures caused by system defects: zero tolerated.
- Restore objective: initial RPO of 15 minutes and RTO of 4 hours, tightened after restore exercises.
- Audit coverage: 100% of authentication, authorization, tool invocation, approval, ownership, integration, and external-effect decisions.

## 14. Explicit non-goals for the first production release

- Splitting the modular monolith into independent business microservices.
- Combining tenant databases into a shared business-data database.
- Allowing providers direct database, Docker, credential-store, or unrestricted network access.
- Policy-based automatic approval of consequential actions.
- Replacing Temporal with an in-house workflow engine.
- Supporting arbitrary third-party tools without a reviewed capability definition and adapter.
- Rebuilding every prototype administration screen before core customer workflows are production-ready.
- Preserving accidental internal package APIs or HTML structure.

## 15. Decision gates still requiring evidence

The following decisions must be resolved by ADR before their dependent phase begins:

1. Production authentication and enterprise identity requirements.
2. Per-tenant runtime workloads versus a pooled stateless tenant API tier at expected fleet size.
3. Object storage, malware scanning, and retention implementation for original uploads.
4. Embedding provider, indexing topology, and reindex cost model.
5. Provider credential ownership and tenant-level model policy.
6. Billing provider and entitlement enforcement boundary.
7. Regional deployment, residency, and disaster-recovery requirements.
8. Audit retention and customer-visible audit export requirements.
9. Whether finance remains an internal operational ledger or targets formal accounting interoperability.
10. Supported browser, mobile, and accessibility conformance targets.

## 16. Definition of production-ready

Production-ready means all of the following, not merely feature parity:

- Architecture boundaries are enforced automatically.
- Critical state machines, repositories, workflows, providers, and transports have the prescribed tests.
- Tenant, role, capability, and object-level authorization is deny-by-default and audited.
- All consequential effects are idempotent and reconcilable.
- Database changes are rehearsed against representative tenant snapshots.
- Backups are encrypted and restore tests are current.
- Logs, metrics, traces, and audit records support tenant-safe diagnosis.
- User-facing failures are recoverable and do not strand durable work.
- Accessibility and responsive behavior pass the agreed standard.
- Deployments are signed, reproducible, staged, observable, and reversible.
- Operator and incident documentation is executable and tested.
- No production runtime dependency reaches into `prototype/`.
