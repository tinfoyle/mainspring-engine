# Production Delivery Plan

- Status: Detailed work breakdown
- Parent: [Production rewrite plan](README.md)

## 1. How to use this plan

This document is the ordered delivery backlog. Each work package should become an epic with linked design decisions, implementation changes, tests, rollout evidence, and operational documentation.

Calendar estimates are intentionally omitted until the team records capacity and cycle time. Work packages are sequenced by dependency and risk. Parallel work is safe only where the listed prerequisites are satisfied.

For every work package, record:

- Owner and reviewers.
- Related product requirement.
- Threat-model impact.
- Data migration impact.
- API and compatibility impact.
- Observability added.
- Test evidence.
- Rollout and rollback procedure.
- Final production-readiness decision.

## 2. Program-level artifacts

Create and maintain these artifacts before dependent implementation begins:

| Artifact | Purpose | Completion evidence |
|---|---|---|
| Capability map | Map prototype behavior to target module and use case | Every route, MCP tool, workflow, schedule, background loop, and table has an owner |
| Requirements traceability matrix | Prevent silent feature loss | Each critical flow links to acceptance tests and production use cases |
| Data ownership catalog | Stop cross-module SQL | Every table and material column has one owning module and approved readers |
| Threat model | Define assets, trust boundaries, attackers, and controls | Reviewed diagrams and mitigations linked to backlog |
| API inventory | Establish compatibility surface | Current payload fixtures, auth, errors, and stream semantics captured |
| Workflow inventory | Establish replay surface | Workflow/activity/signal versions and histories cataloged |
| Operational dependency map | Expose degraded-mode behavior | PostgreSQL, Temporal, object storage, provider, runner, email, Drive, and research failure modes documented |
| SLO specification | Align alerts and capacity | Indicators, objectives, error budgets, and owner approved |
| Decision log | Keep architecture intentional | Required ADRs accepted before implementation gates |

## 3. Phase 0 backlog — Prototype baseline

### P0.1 Clean-checkout reproducibility

- Clone into a clean environment without local credentials.
- Document required Go, Node, Docker, Compose, templ, and platform versions.
- Run code generation and prove it produces no unexpected diff.
- Run `go test ./...` uncached.
- Run TypeScript checking and production bundle build.
- Run the isolated Docker full suite.
- Run one real provider canary with a dedicated non-production credential.
- Archive machine-readable test results and build hashes.

Acceptance:

- One documented command sequence rebuilds the prototype.
- Generated files are deterministic.
- Failures identify the missing dependency or configuration key clearly.

### P0.2 Surface inventory

- Export all HTTP routes with method, auth, CSRF, onboarding gate, request, response, and handler.
- Export MCP tools with schemas, capability, actor requirements, and mutation class.
- List Temporal workflows, activities, signals, task queues, retry policies, and IDs.
- List background goroutines and their leases/recovery behavior.
- List configuration variables, defaults, secret classification, and runtime consumers.
- List database tables, constraints, indexes, triggers, and migration ownership.

Acceptance:

- Inventory totals reconcile to source scans.
- Every surface is tagged `retain`, `replace`, `compatibility`, or `retire`.

### P0.3 Characterization suite

Capture deterministic tests for:

- Owner setup, login, logout, invitation acceptance, member removal, and ownership transfer.
- Template selection and legacy alias behavior.
- Baseline interview, evidence scope selection, evidence decisions, plan creation, readiness, renewal, and reassessment.
- Document upload, extraction, indexing, attachment, retrieval, agent revision, and citation validation.
- Agent configuration, immutable version snapshots, delegation, manager synthesis, and provider failures.
- Work creation, hierarchy, assignment, lifecycle, agent dispatch, owner question, resume, review, and close.
- Approval creation, payload binding, rejection, successful execution, retry, and unknown result.
- Email, Drive, web research, finance, schedules, messenger, platform notices, and MCP parity.
- Account mismatch, Membership role, package entitlement, capability, and object denial at every transport.

Acceptance:

- Every critical flow has a black-box test and a stable fixture.
- Time, IDs, providers, and connectors can be made deterministic.

### P0.4 Baseline measurements

Measure and record:

- Cold/warm startup and readiness.
- Fast and full test durations.
- API latency and query counts for representative pages.
- SSE connection count and event lag.
- Document extraction/indexing throughput.
- Run queue, runner startup, provider duration, token usage, and cost.
- PostgreSQL connection usage by workload class and cell under many-account load.
- Container memory/CPU under idle, typical, and concurrent runs.

Acceptance:

- Initial objectives and capacity assumptions cite measurements rather than guesses.

## 4. Phase 1 backlog — Foundation

### P1.1 Repository and CI foundation

- Create the production Go module at the root.
- Add formatter, vet/static analysis, vulnerability scan, test, race, contract, and dependency checks.
- Add frontend lint, type, unit, accessibility, and build checks.
- Add secret scanning, license policy, SBOM, signed artifacts, and dependency update policy.
- Cache dependencies without caching test results that hide state leakage.
- Separate required pull-request checks from scheduled exhaustive checks.

Acceptance:

- A new module violating dependency rules fails CI with an actionable message.
- Release artifacts are traceable to source and test results.

### P1.2 Typed platform primitives

- Typed UUID identifiers with parsing isolated at transport boundaries.
- `AccountContext` containing immutable Account ID, cell placement generation, and safe request metadata.
- `Actor` containing actor kind, system-wide ID, selected Account, Membership roles, and authentication method.
- Clock and ID-generator ports for deterministic tests.
- Classified application error type with safe public detail and internal cause.
- Transaction runner supporting retry on safe serialization conflicts.
- Redaction helpers and safe structured-logging conventions.

Acceptance:

- Domain code contains no direct `time.Now`, random UUID generation, or transport status code.

### P1.3 Bootstrap and process lifecycle

- One composition package per runtime mode.
- Constructor validation with no optional nil service graph.
- Graceful HTTP shutdown, worker drain, runner cancellation, and telemetry flush.
- Readiness dependency checks and degraded dependency reporting.
- Startup repair moved into explicit reconciliation services with metrics and bounded execution.

Acceptance:

- Dependency construction is not duplicated between app API and worker modes.
- Shutdown tests prove accepted work is drained or durably recoverable.

### P1.4 API foundation

- OpenAPI conventions for IDs, timestamps, money, pagination, versions, problem details, and idempotency.
- Generator and drift check for Go/TypeScript contracts.
- Authentication, Account boundary, Membership, entitlement, request ID, rate limit, body size, CSRF, and security-header middleware.
- Standard response writer and no-cache policy for sensitive data.
- Compatibility test harness capable of comparing prototype and production responses semantically.

Acceptance:

- A sample feature works through generated contracts in Go and TypeScript.

### P1.5 Observability foundation

- OpenTelemetry traces and metrics with safe attribute policy.
- Structured logging schema and correlation propagation.
- Dashboards for HTTP, database, Temporal, runner, provider, and tool broker.
- Alert routing and ownership metadata.
- Development collector and deterministic telemetry tests.

Acceptance:

- A synthetic request can be traced through account router, cell API, workflow, activity, runner, and tool call without exposing content.

## 5. Phase 2 backlog — Commercial foundation and pooled platform

This phase follows the detailed decisions in [accounts-packages-billing.md](accounts-packages-billing.md) and [kubernetes-topology.md](kubernetes-topology.md). It must land before feature modules rely on identity, package access, quotas, or data placement.

Architecture prerequisites are accepted in [ADR-0001](decisions/0001-product-identity-and-web-surfaces.md), [ADR-0002](decisions/0002-system-identity-accounts-packages-and-billing.md), and [ADR-0003](decisions/0003-pooled-cell-runtime.md). A Phase 2 implementation is incomplete if it passes a feature test while violating an ADR verification criterion—for example, by authorizing from Stripe synchronously, treating a User as an Account, checking packages only in the UI, or creating customer-specific Kubernetes resources.

### P2.1 Product identity and public website

- Establish Infinite Ocean brand and **Infinite Ocean: Spyglass** product naming in code, configuration, metadata, UI, email, documentation, and deployment labels.
- Implement `infiniteocean.net` public information architecture: company, product overview, package pages, cross-package outcomes, pricing, security/privacy, legal, support, login, and signup.
- Separate public cacheable routes from private authenticated routes and host-scope cookies.
- Publish a read-only Catalog API for package and pricing presentation with a last-known-published fallback.
- Add analytics consent and event taxonomy without collecting customer business content.

Acceptance:

- No production-facing surface calls the product Mainspring; historical prototype references remain explicit.
- Anonymous pages contain no private API data, secrets, unpublished offers, or live customer demo content.
- Core signup and pricing journeys pass accessibility, responsive, performance, metadata, and link checks.

Implementation checkpoint: the anonymous website renders the Infinite Ocean/Spyglass information architecture, proxies only the published Catalog through a bounded same-origin cache, and labels bundled fallback prices as illustrative. Public CTAs perform a GET-only handoff to the configured private application origin and never put names, email addresses, or business details in marketing-site requests. A selected paid offer remains an opaque intent: registration accepts it only when the current Catalog publishes an effective paid offer, the encrypted verification outbox and SMTP link preserve it, verification and sign-in revalidate it, and the authenticated billing view merely highlights it. Account creation remains free; only a separately authorized POST can create Stripe Checkout, and only projected signed Stripe state can change entitlements. Consent-aware analytics, generated accessibility evidence, and deployed-origin link certification remain.

### P2.2 System-wide identity

- Implement User, authentication identity, verified contact method, session, recovery, MFA/passkey policy, and security-event models.
- Ensure one User can authenticate once and belong to multiple Accounts.
- Implement session rotation, revocation, reauthentication for sensitive changes, and durable login/recovery rate limits.
- Keep provider-specific identity claims behind an adapter and map them to immutable local User IDs.
- Keep passkey credentials and ceremonies User-scoped, encrypted at rest, replica-independent, short-lived, and single-use; require user verification and atomic counter updates.
- Record authentication assurance separately from Membership and Account context so future MFA/step-up policy cannot accidentally grant Account authority.

Acceptance:

- Registration, verification, login, logout-all, expiry, recovery, and compromised-session tests pass.
- Authentication never implies Account Membership or package authorization.
- Passkey challenge, origin, RP-ID, signature, replay, expiry, counter-race, and cross-User isolation tests pass against application and PostgreSQL boundaries.

Implementation checkpoint: system-wide password/passkey authentication, durable sessions, password recovery, shared abuse budgets, user-verified cryptographic step-up, encrypted replica-independent passkey ceremonies/credentials, active-plus-retained envelope keyrings, audited bounded key re-encryption, hashed single-use recovery codes with session-bound lost-passkey replacement, mandatory customer-owner passkey/recovery-code enrollment enforced by the shared authorizer, and signed phishing-resistant platform-administrator authorization with explicit dual-approved break glass are executable. Customer-visible factor-loss/support review, verified contact change, credential response, retention operations, and real-device/browser certification remain.

### P2.3 Accounts and Memberships

- Implement Account, Membership, invitation, AccountRole, lifecycle, ownership transfer, and account switcher use cases.
- Define role permissions for owner, billing administrator, administrator, member, and read-only/auditor variants.
- Make Account creation transactional with owner Membership and audit record.
- Define suspension, closure, deletion, ownership continuity, last-owner, and invitation-expiry rules.

Acceptance:

- A User can create, join, leave, and switch Accounts without session duplication or cache/data leakage.
- Every Account operation passes actor, role, state, object, and cross-account authorization matrices.

Implementation checkpoint: invitation/join, Account switching, active-and-suspended roster visibility, mandatory owner factor readiness, owner role changes, bounded administrator removal/suspension/reactivation, non-owner self-service leave, atomic ownership transfer, and recoverable Account closure are executable across application, memory, PostgreSQL, JSON, worker, Kubernetes-reference, and browser boundaries. Suspension immediately revokes authorization while preserving role; removal/leave are terminal; an owner can neither be suspended nor leave. Every owner read or mutation requires at least one passkey and one unused recovery code, while privileged mutations additionally require recent user-verified passkey proof. Mutations use optimistic target versions, transactional actor-role rechecks, bounded audit reasons, and immutable before/after events. Ownership transfer atomically queues encrypted, independently retryable previous/new-owner notices in the same transaction as the role swap. Closure immediately freezes access, permits owner restoration during a seven-day cooling-off period, blocks on projected billing state, and ends in irreversible logical closure with a retention deadline. Audited export and physical erasure after retention remain.

### P2.4 Catalog and Feature Packages

- Implement immutable/versioned FeaturePackage, PackageFeature, Plan, Offer, OfferPrice, LimitDefinition, and CatalogPublication records.
- Seed initial package identifiers for Work, Agents, Finance, and Marketing without coupling code packages to commercial bundles.
- Map local Offers to allowlisted Stripe Product/Price IDs; the browser submits only an opaque local Offer ID.
- Build draft, review, publish, retire, effective-date, currency, tax presentation, and grandfathering workflows.
- Define which APIs, UI routes, MCP tools, schedules, worker jobs, agent tools, and limit counters belong to each PackageFeature.

Acceptance:

- A published catalog is immutable, reproducible, and rollbackable by republishing a prior version.
- Unpublished/retired offers and arbitrary Stripe IDs cannot begin Checkout.

### P2.5 Entitlement engine

- Implement EntitlementGrant sources: free plan, subscription, trial, promotion, support override, grandfathering, suspension, and safety policy.
- Support `enabled`, `read_only`, and `suspended` modes; effective windows; numeric limits; source precedence; and explainable denials.
- Compute immutable account EntitlementSnapshots transactionally and distribute/invalidate them with versioned events.
- Add one enforcement adapter used by HTTP, MCP, schedules, background workers, execution planning, tool definitions, and usage admission.
- Define downgrade behavior per package: creation blocked, existing data read-only, export available, retention clock explicit, restoration tested.

Acceptance:

- Property/table-driven tests prove deterministic precedence and limit evaluation.
- Removing a package prevents new use through every transport and asynchronous path without deleting customer data immediately.
- Runtime checks use the local snapshot and do not call Stripe.

### P2.6 Stripe billing integration

- Implement BillingProfile, Subscription, SubscriptionItem, invoice/payment summary, provider reference, webhook inbox, projection checkpoint, and reconciliation record.
- Create/reuse a Stripe Customer only when billing begins; implement server-created Checkout and short-lived Customer Portal sessions.
- Pin the Stripe API version and isolate SDK types behind a Billing provider adapter.
- Verify webhook signatures against the untouched raw body, persist/deduplicate events, return success quickly, and project asynchronously.
- Make projection order-independent by retrieving current provider objects when an event cannot safely advance local state.
- Reconcile provider/local state on schedule and expose operator replay, refresh, mismatch, and explanation tools.
- Define reviewed policies for trials, upgrade/proration, cancellation, grace, failed payment, pause, refund/dispute, recovery, and tax/invoice handling.

Acceptance:

- Free signup succeeds during a Stripe outage.
- Duplicate, delayed, and out-of-order webhooks converge without duplicate grants or premature revocation.
- Checkout redirects never grant access; only verified projected state changes paid entitlements.
- No card data is accepted or stored by Spyglass application servers.

Implementation checkpoint: signed, exact-scope `billing-admin` inspection, stored verified-event replay, and known-subscription refresh commands are executable through execute-only database functions with immutable same-transaction evidence. A real Stripe test-mode Checkout/webhook/failure/remediation/cancellation exercise and mismatch explanation output remain.

The public-to-paid journey now has an executable browser contract: published offer code -> private signup -> encrypted verification delivery -> free Account provisioning -> authenticated selected-plan view -> privileged server-created Checkout. No public-site request starts billing, no redirect grants access, and a removed, future, free, or invented offer intent is discarded or rejected before it can reach Checkout.

### P2.7 Global control plane and cell data boundary

Implementation checkpoint: transaction-local Account RLS, versioned request/body/semantic-header-bound route context, signed mutation operation IDs, key rotation, candidate route-key/workload-certificate canaries, overlapping-CA rollover evidence, bounded shared cell replay receipts with a lease-coordinated per-cell cleanup worker, placement-generation rejection, draining/frozen write denial, executable app-router/app-api/admission-api modes, content-free route status, bounded directory caching, TLS 1.3 workload identity, stateless router-replica handoff, shared cell-replica replay defense, zero-unavailable/node-spread router rollout, one-retry same-cell Service connection recovery with fresh route receipts and stable mutation identity, idempotent admission connection recovery, routed Work reads/commands, split-role global capacity admission, a split-credential Work capacity reconciler, and a three-database two-cell placement/move/attack/outage contract are implemented. Applied ingress failure injection, managed database, sustained admission-outage, and load/fairness evidence remain. See [routing-boundary.md](routing-boundary.md).

- Create global schemas for Identity, Accounts, Catalog, Billing, Entitlements, Account Directory, and platform operations.
- Create cell schemas for account business records with non-null `account_id`, RLS, explicit predicates, and composite account-scoped keys/foreign keys.
- Define database roles so serving users neither own protected tables nor bypass RLS.
- Implement transaction-local account context, signed route context, directory cache, placement generation, and stale-route rejection.
- Build cross-account attack fixtures for joins, guessed IDs, jobs, workflow payloads, cache keys, search indexes, objects, exports, and support tooling.

Acceptance:

- Two Accounts sharing the same pods, pool, and cell database cannot read, mutate, reference, schedule, retrieve, or export each other's data.
- Global services cannot query cell business data through ordinary request paths.

### P2.8 Pooled Kubernetes workloads and autoscaling

- Deploy separate shared workload classes for website, account API, app router/API, billing workers, Temporal workers, ingestion/indexing, connectors, and runner controller.
- Define resource requests/limits, bounded process concurrency, readiness/startup probes, graceful drain, disruption budgets, topology spread, and least-privilege service accounts.
- Configure HPA/event-driven scaling from request/latency, CPU, queue age/depth, and Temporal schedule-to-start signals with downstream caps.
- Implement weighted fair admission, account/package quotas, provider-wide rate limits, and one-hot-account protection.
- Keep ephemeral runner jobs per bounded invocation with no database/Kubernetes authority and restricted egress.

Acceptance:

- Normal signup creates no Deployment, Pod, Service, namespace, database, credential, or load balancer.
- Many-small-account and one-hot-account tests meet latency/queue objectives without cross-account starvation.
- Pod/node/zone loss drains or retries accepted work without duplicate visible effects.

### P2.9 Account placement and cell operations

- Implement capacity-aware cell selection, placement audit, soft/hard cell admission thresholds, and cell health signals.
- Implement durable drain, copy/change-capture, reconcile, placement-generation switch, resume, rollback-window, and source-retention workflow.
- Make Account restore, export, deletion, cell evacuation, and dedicated enterprise placement use the same account identity boundary.
- Exercise global-control outage, cell outage, cell database failover, Stripe outage, and placement-cache staleness behavior.

Acceptance:

- An Account moves between cells with verified rows, objects, search state, workflows, entitlements, and an exercised rollback path.
- A cell failure affects only its assigned cohort and is identifiable from directory and telemetry state.

## 6. Phase 3 backlog — Work and Attention

### P3.1 Work domain

Implementation checkpoint: typed construction, lifecycle/role matrix, assignment and provenance values, maximum depth, optimistic versioning, private broker-backed active-item admission, routed create/transition/assignment contracts, and first creation/lifecycle browser controls are implemented. See [work-module.md](work-module.md). Persona ownership, assignment editing, and provenance attachment commands remain.

- Replace raw kind/status/priority/source/responsibility strings with validated value types.
- Specify transition matrix and role permissions.
- Specify parent/child rules, including maximum depth and cycle prevention.
- Specify assignment rules for user, persona, shared, and external responsibility.
- Define provenance value object for baseline, schedule, conversation, run, and creating actor.
- Add version for optimistic concurrency.

Acceptance:

- Table-driven tests cover every allowed and rejected transition.
- Invalid objects cannot be constructed through public commands.

### P3.2 Work persistence and queries

Implementation checkpoint: pooled-cell schema, forced RLS, Account-local numbering, composite parent constraints, optimistic create/update, direct children, stable cursor queue, summaries, mutation events, routed query/command transport, durable split-credential capacity release, execute-only audited dead-letter inspection/requeue, bounded completed-job retention with independent audit preservation, and non-owner isolation/concurrency tests are implemented. Representative query plans, provenance links, and Persona foreign keys remain.

- Repository commands for create, update status, assign, attach provenance, and link conversations.
- Cursor-based queue query with stable ordering.
- Direct indexed child query; do not load 250 items and filter in memory.
- Queue summaries either query efficiently or use a verified projection.
- Integration tests for concurrent assignment and completion.

Acceptance:

- Query plans meet agreed limits on representative account and cell data.
- Repository errors are classified and no pgx types escape.

### P3.3 Agent-work dispatcher

- Use a lease with owner, acquired time, expiry, and heartbeat rather than overloading `in_progress` and `updated_at`.
- Separate claim, start run, link run, heartbeat, release, resume, and reconcile commands.
- Make capacity accounting include active executions rather than only visible work status.
- Add jittered polling or event wakeup without losing periodic reconciliation.
- Test crashes before run creation, after run creation, after linking, and after dispatch.

Acceptance:

- Every crash point converges to one active run or one safely requeued work item.

### P3.4 Attention domain

- Separate `InformationRequest`, `WorkReview`, and `ConsequentialApproval` aggregates.
- Link information questions to explicit fact requirements with scope.
- Define completion and parent-resumption rules.
- Bind approvals to canonical payload bytes, hash algorithm version, evidence, proposer, and policy version.
- Define expiration and cancellation behavior.

Acceptance:

- Answering a shared fact completes exactly the eligible questions and resumes exactly the unblocked parents.
- Altering a proposal after review invalidates the decision.

### P3.5 Action ledger and executors

- Unique idempotency key and canonical request hash.
- Executor registry by action kind.
- State machine for prepared/executing/succeeded/failed/unknown.
- Reconciliation interface for providers that support lookup.
- Manual-resolution command with dual-control option for high-risk actions.
- Redacted action views and immutable audit history.

Acceptance:

- Repeated approve/execute requests perform at most one external effect.
- Timeout after provider acceptance produces `unknown`, never an automatic duplicate.

Implementation checkpoint: the runner gateway now requires distinct execute and side-effect-free reconcile methods for consequential definitions. A forced-RLS action authorization projection, ledger, and attempt history bind approval evidence to canonical input SHA-256, use the operation UUID as the stable provider idempotency key, serialize leases with cancellation, default uncertain errors to `unknown`, and force every expired/unknown/succeeded retry through reconciliation. A real consequential provider adapter, explicit failed retry, manual resolution/redacted views, and the Attention-owned approval aggregate remain before P3.5 acceptance can be claimed.

### P3.6 Transport migration

- Production HTTP queue, detail, mutation, attention, and decision endpoints.
- MCP tools over the same commands and queries.
- Prototype compatibility adapter and response comparator.
- React feature slices for Work and Your Turn.

Acceptance:

- HTTP and MCP produce the same authorization and domain outcomes.

## 7. Phase 4 backlog — Knowledge and Baseline

### P4.1 Knowledge facts

- Typed fact key, value, source, scope, sensitivity, confidence, version, confirmation, and stale time.
- Conservative deterministic matching for legacy questions.
- Explicit contradiction workflow instead of last-write-wins.
- Fact history and actor attribution.
- Authorization policy for sensitive fact scope.

Acceptance:

- Corrections preserve history and invalidate only affected derived answers.
- A heuristic match can suggest but cannot silently overwrite an explicit fact key.

### P4.2 Document lifecycle

- Upload session and object metadata.
- Filename/media validation, size limits, checksum, malware scan, extraction, indexing, ready/failed/quarantined states.
- Immutable revisions with author and change summary.
- Stable citations to exact revision/chunk.
- Retention, deletion, and Account export behavior.
- Agent write policy and duplicate/canonical-document detection.

Acceptance:

- Every stored binary and extracted body has provenance and bounded processing.
- Updating a document never breaks an existing historical citation.

### P4.3 Retrieval

- Keyword search baseline and embedding-search port.
- Document-scope intersection policy.
- Result budgets and sensitivity filtering.
- Query/result audit without copied content.
- Index-generation and reindex process.
- Recall and decoy-exclusion evaluation dataset.

Acceptance:

- Retrieval evaluations meet agreed recall and leakage thresholds.
- Unauthorized documents cannot affect scores, metadata, counts, or citations.

### P4.4 Baseline state machine

- Define commands, states, guards, and transition events.
- Version evidence catalog and scope-selection algorithm independently from assessments.
- Persist which catalog/scope-policy version produced each requirement.
- Separate interview parsing from state transition.
- Add explicit not-applicable reason and review policy.
- Use deterministic time for renewal/reassessment.

Acceptance:

- Old assessments remain explainable after catalog changes.
- Every transition can be replayed from commands in tests.

### P4.5 Evidence sources and planning

- Uniform `SourceItem` contract for uploads, email, Drive, and public research.
- Source-specific cursor/checkpoint and revocation behavior.
- Evidence candidate versus verified evidence distinction.
- Baseline plan preview with deterministic work commands.
- One transaction or idempotent saga for plan approval and child creation.

Acceptance:

- Repeating plan creation never duplicates parent or child work.
- Revoking a connector prevents future reads without erasing provenance needed for audit.

## 8. Phase 5 backlog — Workspace and Execution

### P5.1 Workspace domain and persistence

- Boardroom and persona configuration commands.
- Immutable persona-version creation and active-version selection.
- Conversation/run/message ownership and access policy.
- Attachment snapshots and targeted-persona runs.
- Read models for boardroom, conversation, and agent administration.

Acceptance:

- Editing an agent affects future plans only.
- Conversation messages have a deterministic, gap-free visible order under retries.

### P5.2 Run planning

- Manager-led and selected-agent orchestration modes.
- Deterministic delegation resolution against the snapshotted roster.
- Maximum turns and cycle prevention.
- Run plan hash and version.
- Idempotent prepare and delegation-expansion commands.

Acceptance:

- Repeated preparation yields the identical plan.
- Invalid or invented delegates are rejected before scheduling.

### P5.3 Context assembly

- Token estimator port and deterministic truncation.
- Separate budgets for instructions, conversation, facts, ticket context, and tool results.
- Newest-message preservation and summary policy.
- Sensitivity and grant filtering.
- Context manifest persisted for audit without duplicating secret content.

Acceptance:

- Golden fixtures prove deterministic context under the same inputs and policy version.

### P5.4 Provider contract

- Provider capabilities and normalized invocation/result/failure types.
- Conformance suite implemented by mock and real adapters.
- Cancellation and timeout semantics.
- Usage normalization and cost calculator version.
- Strict schema and safe provider metadata.

Acceptance:

- Switching provider adapters requires no domain or workflow change.

### P5.5 Tool loop

- Automatic document preflight expressed as application policy.
- Request ID uniqueness and replay behavior.
- Per-tool and aggregate call/result budgets.
- Recoverable tool errors versus terminal authorization/schema errors.
- Citation collection from authorized results only.
- Tool-result prompt-injection isolation.

Acceptance:

- Fuzz tests cannot escape grant conditions, schemas, domain allowlists, or byte limits.

### P5.6 Result policies

Implement separately tested policies for:

- Structured schema and confidence.
- Citation binding and required evidence.
- Delegation visibility.
- Unknown-answer escalation.
- Missing external credential handling.
- Owner questions becoming typed input.
- Parent work binding.
- Action suppression.
- Artifact publication.

Acceptance:

- Each policy is pure or has an explicit port and is independently testable.
- Policy order is named, versioned, and covered by combined fixtures.

### P5.7 Temporal workflow

- Versioned workflow input, signals, and activity names.
- Classified retry policies and heartbeat details.
- Attention wait and resume.
- Cancellation and failure projection.
- Replay corpus for every released workflow version.
- Operator commands for reset/retry/reconcile with authorization and audit.

Acceptance:

- CI replays all workflow histories after every workflow-code change.

## 9. Phase 6 backlog — Remaining modules

### P6.1 Scheduling

- Typed schedule expression and timezone validation.
- DST gap/overlap test matrix.
- Schedule creation idempotency and Temporal reconciliation.
- Trigger-now command separate from schedule mutation.
- Pause, resume, delete, and missed-run policy.

### P6.2 Finance

- Currency and minor-unit type; never floating point.
- Account types and posting permission.
- Balanced-entry validation.
- Draft concurrency, posting immutability, reversal, and void policy.
- Sequence allocation and audit history.
- Agent capability and human approval policy for financial mutations.

### P6.3 Email

- Credential reference and connection verification.
- Read scope and evidence extraction bounds.
- MIME hardening and attachment policy.
- Durable outbox, provider message reference, retry, and reconciliation.
- Explicit sending identity and anti-header-injection validation.

### P6.4 Google Drive

- OAuth state binding and callback authorization.
- Encrypted refresh-token storage and rotation.
- Folder allowlist, canonical IDs, shortcuts, exports, and size limits.
- Incremental sync checkpoint and revoked-access behavior.

### P6.5 Web research

- URL canonicalization and scheme restriction.
- DNS/IP checks before and after redirects.
- Private/link-local/metadata network denial.
- Search/read separation and source citation.
- Content-type, byte, redirect, and timeout limits.
- Provider outage and result-quality telemetry.

Acceptance for Phase 5:

- Each connector passes a shared degraded-mode and credential-redaction suite.
- Finance and scheduling pass concurrency and time-boundary suites.

## 10. Phase 7 backlog — Product surface

### P7.1 HTTP transport

- Feature route groups and generated request/response types.
- Consistent actor/Account/Membership/package middleware.
- Idempotency and optimistic concurrency middleware where applicable.
- Bounded multipart handling through document use cases.
- Problem-details errors and correlation IDs.
- No content negotiation that changes mutation semantics.

### P7.2 MCP transport

- Bearer/workload authentication and Account binding.
- Tool schemas generated or conformance-tested against use-case input.
- Read versus mutation classification.
- Capability and role mapping.
- Bounded result payloads and problem translation.
- Audit parity with HTTP.

### P7.3 React application foundation

- Router, layouts, session boundary, query client, error boundary, and design tokens.
- Account switcher, package-aware navigation, entitlement-denied/upgrade states, billing settings, and usage display.
- Generated API client and problem normalization.
- SSE manager with cursor resume and test harness.
- Accessible dialog, form, table/list, status, notification, and command components.
- Story and visual-regression environment.

### P7.4 Feature migration order

1. Home and inbox read models.
2. Work queue and ticket workspace.
3. Your Turn and approvals.
4. Documents and document detail.
5. Boardroom and conversation streaming.
6. Baseline interview and plan.
7. Agents.
8. Finance.
9. Team, schedules, email, Drive, and operations.

Each feature requires loading, empty, partial, stale, error, unauthorized, offline/reconnect, and reduced-motion behavior before cutover.

### P7.5 Legacy retirement

- Instrument compatibility route use by route and Account cohort.
- Prevent new product work in legacy templates.
- Remove a legacy route only after production E2E coverage and zero required traffic.
- Remove Templ generation and HTMX assets only after the last supported legacy route is gone.

Acceptance:

- One route has one canonical production mutation path.
- UI state survives recoverable stream/network failures without duplicate commands.

## 11. Phase 8 backlog — Operations and security

See [quality-security-operations.md](quality-security-operations.md) for full gates.

Key deliverables:

- Auth choice, MFA/session policy, recovery, ownership transfer, and administrative access.
- Central secret manager, workload identities, encryption-key rotation, and credential revocation.
- Rate limits and abuse controls by edge, Account, actor, package, capability, provider, and connector.
- Backup and point-in-time recovery for the global control database and every cell database.
- Object-store replication and restore.
- Account export and verified deletion across global records, cell data, objects, search, and backups.
- SLO dashboards and paging alerts.
- Run, action, migration, and connector operational consoles.
- Incident, Account restore, cell restore/evacuation, Stripe/provider outage, compromised credential, and cross-account isolation runbooks.
- Penetration test and threat-model closure.
- Load, soak, autoscaling, noisy-neighbor, chaos, cell-move, and cohort-migration tests.

## 12. Cutover playbook

For every migrated capability:

1. Deploy production read path disabled.
2. Shadow reads against prototype data or compatibility repository.
3. Compare semantic results and emit mismatch metrics without customer impact.
4. Resolve mismatches and establish a sustained clean window.
5. Enable production reads for internal Accounts.
6. Enable production writes in dual-observe mode where safe; avoid uncontrolled dual-write.
7. Reconcile domain counts, versions, hashes, and lifecycle state.
8. Move canary customer cohort.
9. Monitor SLOs, errors, support signals, costs, and mismatch metrics.
10. Expand cohorts with automated pause thresholds.
11. End compatibility writes after the rollback window.
12. Remove the old code in a separate change.

Rollback requirements:

- Schema remains backward compatible for the defined window.
- Every release notes minimum compatible schema and workflow versions.
- Writes performed by production are either readable by compatibility code or guarded from rollback with an explicit operator decision.
- Workflow rollback uses compatible workers or Temporal versioning, never history deletion.

## 13. Cross-cutting acceptance matrix

Every production use case must answer yes to the applicable questions:

### Domain

- Are states and values typed?
- Are transitions and invariants tested?
- Is actor intent recorded?
- Are time and ID generation deterministic in tests?

### Account isolation and security

- Are User identity, selected Account, Membership, cell placement, and resource Account verified at their boundaries?
- Is effective Feature Package access enforced in synchronous and asynchronous paths?
- Is authorization deny-by-default?
- Are sensitive fields classified and redacted?
- Is every consequential decision audited?

### Reliability

- Is the command idempotent or explicitly non-retryable?
- What happens if the process stops before, during, and after persistence?
- Can ambiguous external outcomes be reconciled?
- Does dependency failure remain contained?

### Data

- Who owns the data?
- Is optimistic concurrency required?
- Is migration additive and restartable?
- Are retention, export, and deletion defined?

### Contracts

- Is the API schema updated?
- Are HTTP and MCP outcomes consistent?
- Are errors stable and safe?
- Are pagination and payload bounds explicit?

### Operations

- Are metrics, traces, logs, and audit events sufficient?
- Is there an alert and owner for a stuck or failed state?
- Is there a runbook and safe operator action?
- Has rollback been exercised?

### User experience

- Are loading, empty, error, retry, stale, and offline states designed?
- Is the flow keyboard accessible and screen-reader understandable?
- Are long-running actions honest about progress and authority?
- Does retry avoid duplicate mutations?

## 14. Program completion checklist

- [ ] Capability inventory has no unowned retained surface.
- [ ] Data catalog has no cross-module direct write.
- [ ] Production dependency checks pass.
- [ ] Generated contracts have no drift.
- [ ] Public website, free signup, multi-Account switching, package enforcement, Stripe projection, and downgrade journeys pass.
- [ ] Shared-cell isolation, fairness, autoscaling, Account move, and one-Account restore exercises pass.
- [ ] Critical state-machine, repository, workflow, provider, and E2E suites pass.
- [ ] All security review findings are closed or explicitly risk-accepted.
- [ ] Restore, rollback, and disaster-recovery exercises meet targets.
- [ ] SLO dashboards and alerts have completed a staging burn-in.
- [ ] Canary and cohort rollout completed without unresolved mismatches.
- [ ] Compatibility traffic is zero for the agreed window.
- [ ] No production code, build, or deployment imports from `prototype/`.
- [ ] Prototype is archived with retention and access policy.
