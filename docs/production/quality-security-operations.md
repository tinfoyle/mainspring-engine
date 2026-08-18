# Quality, Security, and Operations Plan

- Status: Production gate specification
- Parent: [Production rewrite plan](README.md)

## 1. Quality strategy

Production confidence comes from complementary test layers. End-to-end tests alone are too slow and imprecise; unit tests alone cannot verify database, workflow, provider, or browser contracts.

### 1.1 Required test layers

| Layer | Runs | Primary purpose |
|---|---|---|
| Domain unit | Every change | Values, invariants, state transitions, policy ordering |
| Application unit | Every change | Use cases with fake ports, authorization, transaction behavior |
| Property/fuzz | Every change or scheduled by cost | Parsers, tool schemas, URLs, MIME, money, state-machine safety |
| Repository integration | Every change | Real PostgreSQL queries, constraints, concurrency, migrations |
| Contract | Every change | OpenAPI/TypeScript drift, MCP schema, provider conformance, connector behavior |
| Temporal replay | Every workflow change | Determinism and backward-compatible workflow evolution |
| Frontend unit/component | Every change | Rendering, forms, cache updates, accessibility, error states |
| Browser E2E | Pull request subset; full scheduled/release | Critical cross-surface user journeys |
| Security | Every change plus scheduled dynamic tests | Dependency, secret, static, authorization, SSRF, upload boundaries |
| Migration fleet | Release candidate | Upgrade representative Account snapshots and cell schemas; reconcile invariants |
| Load/soak/failure injection | Scheduled and release gate | Capacity, leaks, recovery, dependency isolation |
| Restore/DR exercise | Scheduled and major release | Prove backups and operational recovery |

### 1.2 Determinism

Tests use injected clocks, ID generators, fake providers, fixed pricing, deterministic embeddings/search fixtures, and controlled connector servers. Retry and timeout tests use virtual workflow time where supported.

No test depends on execution order, a developer mailbox, a real clock boundary, a live payment/provider service, or an unversioned remote response.

### 1.3 Test data

- Synthetic only; no production customer content in fixtures.
- Account A/Account B pairs sharing one cell exist for every authorization, RLS, queue, object, cache, search, and query suite.
- Representative small, medium, high-cardinality, and deliberately hot Account datasets are generated reproducibly.
- Document fixtures cover supported, malformed, oversized, compressed, encrypted, macro-bearing, and adversarial formats.
- Prompt/tool fixtures include injection attempts and forged citations.
- Migration snapshots are scrubbed and generated, with recorded schema versions and hashes.

### 1.4 Critical end-to-end journeys

The release suite must cover:

1. Visit `infiniteocean.net`, register a system User, create a free Account without billing, enter Spyglass, invite a member, switch Accounts, and enforce roles.
2. Upgrade through Stripe Checkout, process asynchronous billing state, enable paid packages, manage billing in Customer Portal, downgrade safely, and reconcile provider/local state.
3. Complete a business baseline and approve its work plan.
4. Upload evidence, retrieve it in an agent run, preserve citations, and recall it later.
5. Create agent-owned work, request owner input, reuse a fact, resume, review, and complete.
6. Run a manager-led boardroom with delegation and synthesis.
7. Propose, approve, execute, and reconcile an external action without duplication.
8. Schedule a run across timezone/DST behavior.
9. Connect and revoke an evidence connector with scoped reads.
10. Create, post, and reverse a balanced finance entry under correct authorization.
11. Perform equivalent authorized operations through MCP and deny unauthorized ones.
12. Lose and restore SSE/network connectivity without lost or duplicated visible state.
13. Expire a session during a draft and recover without leaking or silently submitting it.
14. Move one Account between cells and restore one Account from backup without affecting neighboring Accounts.

## 2. Security program

### 2.1 Threat-model scope

Assets include:

- System-wide User identity, Account identity, Membership, roles, and sessions.
- Published Catalog, billing projection, entitlement grants/snapshots, and package limits.
- Business facts, messages, documents, financial records, and email.
- Provider, OAuth, SMTP/IMAP, database, signing, and encryption credentials.
- Capability grants and approval authority.
- Workflow and action integrity.
- Docker/Kubernetes control plane and runner infrastructure.
- Audit records and backups.

Threat actors include unauthenticated internet clients, malicious Account members, billing/webhook spoofers, compromised agent/provider output, hostile document/web/email content, compromised connectors, leaked credentials, supply-chain compromise, and privileged operators exceeding intended authority.

Review trust boundaries for browser-to-public-site, browser-to-identity/account API, Stripe-to-webhook ingress, router-to-cell, Account-to-shared-cell-database, workflow-to-worker, worker-to-runner-controller, runner-to-provider, runner-to-broker, broker-to-connector, and global/cell backup/restore paths.

### 2.2 Authentication and sessions

Production authentication requires an ADR. Minimum requirements:

- Modern password hashing if passwords remain supported.
- User-verified passkey step-up for invitation and billing mutations; complete mandatory owner/platform-administrator enrollment, recovery-code, and factor-loss policy before release.
- Short-lived authenticated sessions with rotating opaque tokens.
- Secure, HttpOnly, SameSite cookies and CSRF protection for cookie mutations.
- Session inventory and remote revocation.
- Reauthentication for ownership transfer, credential changes, and high-risk approvals.
- Brute-force, credential-stuffing, enumeration, and recovery abuse controls.
- One-time invitation and recovery tokens stored as hashes with expiry and consumption audit.
- Platform administration isolated from Account roles and normal customer sessions.

### 2.3 Authorization

- Central policy vocabulary but module-owned object authorization.
- Deny by default for new commands, routes, MCP tools, and capabilities.
- Role plus relationship checks; role alone is insufficient for sensitive objects.
- Explicit actor propagation through background and workflow commands.
- Service identities are narrow and independently revocable.
- Authorization matrix tests cover User, selected Account, Membership role/state, package entitlement, object relationship, Account mismatch, ownership, assignment, capability grant, and object existence.

### 2.4 Account and cell isolation

- Immutable Account ID is verified from authenticated Membership and trusted cell routing, never request body, slug, or host alone.
- Global control data and cell business data use separate credentials and service roles.
- Shared cell connection pools set transaction-local Account context; serving roles cannot own protected tables or bypass RLS.
- Every customer-owned row has non-null `account_id`; account-local unique and foreign-key relationships include it.
- Cache keys, object keys, workflow IDs, queue messages, log attributes, controlled metrics, and search indexes include safe Account identity.
- Cross-account test cases exist for every repository, transport, workflow/job, cache, object, export, and search path.
- Support/operator access is time-bounded, reason-bound, audited, and customer-visible where policy requires.

### 2.5 Billing and entitlement security

- The application accepts local Offer IDs only; server-side Catalog mappings select allowlisted Stripe Prices.
- Checkout success redirects are advisory and never grant access.
- Stripe signatures are verified against the untouched raw body with mode-specific secrets and replay-age policy.
- Webhook events are persisted and deduplicated before asynchronous projection; duplicate and out-of-order delivery must converge.
- Checkout and Customer Portal sessions are created only after Account role checks and recent user-verified passkey proof; password confirmation is insufficient and return destinations are allowlisted.
- Runtime authorization reads a local immutable EntitlementSnapshot, not Stripe, browser claims, cached navigation, or plan names.
- Free, paid, trial, promotion, grandfathered, suspension, safety, and support-override grants have reviewed precedence and complete audit trails.
- Payment instrument details remain in Stripe; logs, traces, analytics, support exports, and audit events exclude sensitive payment data.
- Billing support actions are time/reason-bound, cannot silently weaken global safety ceilings, and trigger snapshot recomputation.

### 2.6 Agent and prompt security

- Provider output is untrusted data until schema and policy validation completes.
- Retrieved documents, web pages, and email are clearly delimited untrusted evidence.
- Tool calls require signed invocation claims and server-side schemas.
- Capability tokens are short-lived, audience-bound, invocation-bound, and never serialized to durable provider-visible output.
- Citation metadata is bound from returned authorized tool results, not accepted from model claims.
- Agent-produced instructions cannot alter persona grants, budgets, approval policy, or orchestration.
- Sensitive prompt logging is disabled by default and requires audited diagnostic enablement.

### 2.7 Network and SSRF controls

- Egress is deny-by-default for runners.
- Web research occurs through a controlled service boundary.
- URL parsing permits only intended schemes and ports.
- DNS answers and every redirect target are checked against private, loopback, link-local, multicast, metadata, and reserved ranges.
- Rebinding is mitigated by connection-level destination validation where supported.
- Response bytes, decompression, redirects, and duration are bounded.
- Internal service hostnames and credentials never enter public research requests.

### 2.8 File security

- Verify actual content type independently from filename.
- Enforce upload, expanded archive, page, entry, character, and extraction-time limits.
- Quarantine before use and scan with a production malware service.
- Extract in a sandbox with no network and read-only tooling.
- Never execute macros, scripts, active PDF content, or embedded objects.
- Escape or safely render extracted HTML and user-controlled filenames.
- Object storage is private, encrypted, Account-prefixed, and access logged.

### 2.9 Secret and key management

- No secrets in source, images, Compose files, logs, crash dumps, or workflow payloads.
- Managed secret store or workload identity in production.
- Separate keys by purpose: sessions, capability signing, credential encryption, RAG/service auth, and provider auth.
- Envelope encryption with key version recorded alongside ciphertext.
- Documented rotation and emergency revocation for every credential class.
- Production startup rejects placeholder, short, shared-purpose, or missing secrets.
- Secret scanner runs locally, in CI, and against release history.

### 2.10 Supply chain and runtime

- Pin and review base images and dependencies.
- Build in an isolated reproducible pipeline.
- Generate SBOM and signed provenance.
- Scan source, dependencies, containers, and infrastructure configuration.
- Run services as non-root with read-only roots and minimal Linux capabilities.
- Separate runner-controller privileges from normal application workloads.
- Admission policy accepts only signed images from the release pipeline.
- Critical vulnerability remediation policy has severity-based deadlines and emergency release procedure.

### 2.11 Audit

Audit events cover:

- Authentication, session, invitation, and recovery changes.
- Membership, role, ownership, and administrative access, including immutable actor/target/before/after/reason evidence for every Membership mutation.
- Account creation/state/cell assignment, Catalog publication, billing transitions, webhook projection/reconciliation, grants, overrides, snapshots, and package denials.
- Agent/persona configuration and immutable version creation.
- Capability issuance, tool allow/deny/error, and scope.
- Baseline fact/evidence changes and document revisions.
- Work assignment/lifecycle and human answers.
- Approval decisions and action execution/reconciliation.
- Integration connect, scope change, error, and revoke.
- Finance create/update/post/void/reversal.
- Platform placement, cell migration, support access, export, retention, and deletion.

Audit events are append-only, timestamped, actor-attributed, correlated, redacted, retained by policy, and exportable. Application administrators cannot silently rewrite them.

## 3. Reliability and recovery

### 3.1 Failure model

For every dependency, specify timeout, retry class, circuit behavior, degraded experience, alert, and recovery owner.

| Dependency | Expected degraded behavior |
|---|---|
| One cell PostgreSQL | Reject stateful requests for that cell; do not accept work that cannot be persisted; other cells continue |
| Global control PostgreSQL | Existing Account routes may continue within bounded signed/cache TTL; signup, identity changes, billing management, and placement pause |
| Stripe | Free/application access continues from local snapshots; Checkout, Portal, projection refresh, and reconciliation degrade visibly |
| Temporal | Persist commands where possible and show queued state; no local untracked durable substitute |
| Runner controller | Runs remain queued/retryable; Account reads and manual work remain available |
| Model provider | Circuit opens by provider; runs remain visible and recoverable |
| Knowledge/RAG | Agents declare evidence capability unavailable; no unsupported answer presented as sourced |
| Email/Drive/research | Only dependent tools degrade; credentials and last successful sync remain visible |
| Object storage | Upload/download pause; metadata remains consistent and no partial ready state |
| Telemetry backend | Application continues with bounded local buffering/drop policy; never block customer commands indefinitely |

### 3.2 Idempotency

Required for:

- Account creation/placement and inbound billing events.
- Conversation/run creation.
- Invocation attempts and message projection.
- Tool calls with mutations.
- Work and baseline plan creation.
- Human answer application and parent resume.
- Approval decision and external action execution.
- Email sending and connector imports.
- Schedule creation and scheduled-run creation.
- Finance posting/reversal.
- Document ingestion and agent publication.

Idempotency records include key, operation kind, actor/Account, canonical request hash, status, response reference, expiry/retention, and conflict behavior.

### 3.3 Reconciliation

Provide safe reconcilers for:

- Active workflows without corresponding active database runs and vice versa.
- Claimed work without a run, multiple runs for one lease, and expired leases.
- Pending attention with no blocked run or blocked run with no pending attention.
- External actions stuck executing or unknown.
- Email outbox and provider message IDs.
- Temporal schedules and database schedule definitions.
- Documents stuck scanning/extracting/indexing.
- Connector checkpoints and imported source items.
- Usage reservations without a live invocation.
- Account Directory/cell placement versus copied data and active workflows.
- Stripe Customer/subscription state versus local Billing projections, Entitlement Grants, and snapshots.

Reconcilers are bounded, observable, idempotent, and runnable in dry-run mode. They use application commands, not hidden cross-module SQL fixes.

### 3.4 Backup and restore

- Automated encrypted global-control and per-cell database backups with point-in-time recovery.
- Encrypted object-store versioning/replication according to retention policy.
- Backup catalog associates global/cell identity, included Account manifests, schema version, application compatibility, and encryption-key version.
- Restore into an isolated environment first.
- Run migrations only after verifying the restored schema and artifact compatibility.
- Reconcile Temporal state, schedules, actions, connector checkpoints, and indexes.
- Rotate exposed or environment-specific credentials during restore.
- Run synthetic Account journeys, RLS/cross-account probes, billing-entitlement checks, and data-integrity checks before reopening traffic.

Restore tests cover the global control plane, a complete cell, and one Account extracted/restored under policy across different sizes and schema ages. A backup that has not been restored successfully within the policy window is considered unverified.

### 3.5 Disaster recovery

The initial proposed targets are RPO 15 minutes and RTO 4 hours. Final targets require business approval and cost analysis.

Exercises cover:

- Loss of one application zone.
- Loss/corruption of one cell database and isolation from unaffected cells.
- Loss of control database.
- Loss of object storage region.
- Temporal cluster failure and recovery.
- Credential/key compromise.
- Bad application release and bad migration.

## 4. SLOs, metrics, and alerts

### 4.1 Proposed SLIs

- Successful authenticated Spyglass API requests divided by eligible requests, broken down by cell and workload class.
- Latency distributions for reads, writes, and durable command acceptance.
- Run time in queued/preparing/running/awaiting-attention states.
- Invocation success and retry rate by provider and failure category.
- Runner queue/start/duration/orphan counts.
- Tool success/deny/error/latency/result bytes by capability.
- Oldest pending human input, review, approval, action, outbox, and indexing job.
- Workflow task/activity latency and retry saturation.
- Database availability, pool saturation, lock waits, transaction retries, and replica/backup lag.
- SSE connection errors, reconnect success, cursor replay gaps, and event lag.
- Retrieval latency, empty results, citation failure, and evaluation quality.
- Cost and token usage by Account, package, run kind, provider, and model.
- Signup completion, entitlement projection age, Stripe webhook/reconciliation backlog, package-denial rate, and Account placement/move health.
- Cell capacity, queue age, scaling latency, fairness/throttling, database connection headroom, and hot-Account concentration.

### 4.2 Alert design

Alerts must identify user impact, scope, owning service, likely cause, and runbook. Page on sustained actionable symptoms rather than every retry.

Page-worthy conditions include:

- Cross-account isolation, entitlement, or authorization correctness signal.
- Verified billing projection divergence that may grant or revoke access incorrectly.
- Consequential action duplication or unexplained payload mismatch.
- Broad API SLO burn.
- Database unavailable or restore/backup outside objective.
- Workflow backlog with customer-visible delay.
- Runner orphan growth or controller compromise signal.
- Unknown external actions exceeding age threshold.
- Migration failures across an active rollout cohort.

Ticket-worthy conditions include gradual capacity saturation, provider cost regression, retrieval-quality regression, non-critical connector error increases, and accessibility/performance budget drift.

## 5. Performance and capacity

Define capacity in terms of active Accounts per cell, Users, HTTP concurrency, SSE connections, database rows/bytes/IOPS/connections, documents/bytes/chunks, work items, conversations/messages, schedules, concurrent invocations, provider tokens, connector sync volume, and queue age.

Required tests:

- API read/write load with representative database cardinality.
- Thousands of idle and active SSE connections.
- Burst of scheduled runs at common clock boundaries.
- Concurrent agent-owned work claims and owner-answer resumes.
- Large but valid document ingestion and reindex.
- Provider slowdown and rate limiting.
- Many-small-Account load, one-hot-Account fairness, burst free signup, cell startup/move/backup/reconciliation, and autoscaling response.
- 24-hour minimum soak for connection, goroutine, memory, and disk leaks before general availability.

Performance budgets include database query count and bytes, API payload size, JavaScript route chunk size, rendering responsiveness, and external call concurrency.

## 6. Privacy and data lifecycle

Classify at minimum:

- Public platform content.
- Account and Membership metadata.
- Confidential business content.
- Highly sensitive credentials and authentication material.
- Financial and personnel-related business records.
- Security/audit data.

For each class define collection purpose, authorized roles/services, encryption, logging rules, retention, export, deletion, backup expiry, and support access.

Account deletion is a durable workflow with owner/platform authorization, cooling-off policy, billing cancellation policy, active-run cancellation, connector revocation, cell-row/object/search deletion, global-record minimization, backup-expiry tracking, and a final auditable tombstone without retained business content.

## 7. Accessibility and UX quality

Target WCAG 2.2 AA unless an ADR establishes a different requirement.

Required checks:

- Semantic headings, landmarks, form labels, errors, and status announcements.
- Full keyboard navigation and visible focus.
- Dialog focus trap/return and escape behavior.
- Screen-reader behavior for streamed messages and changing work status.
- Color contrast and non-color status cues.
- Reduced motion and no motion-dependent comprehension.
- Responsive behavior at supported mobile/tablet widths.
- Draft preservation and clear retry after network/session failure.
- Honest progress language that distinguishes queued, running, waiting, proposed, approved, executing, unknown, and completed states.

Automated checks are necessary but not sufficient; release candidates receive manual keyboard and screen-reader review of critical journeys.

## 8. Operational tools and runbooks

Operators need supported tools rather than database access for:

- Account/cell health, capacity, placement, and move state.
- Stripe webhook/reconciliation health and account entitlement explanation/recompute.
- Workflow/run lookup and safe retry/cancel/reconcile.
- Stuck work lease inspection.
- Attention and action reconciliation.
- Provider circuit status and controlled reset.
- Connector health and credential revocation.
- Document ingestion/reindex retry.
- Migration cohort status and pause/rollback.
- Backup/restore execution and evidence.
- Account export/deletion progress across global and cell stores.
- Time-bounded support access with reason and audit.

Minimum runbooks:

1. API SLO burn.
2. User cannot authenticate or select an Account.
3. Suspected cross-account access or RLS failure.
4. PostgreSQL outage or corruption.
5. Temporal backlog or nondeterminism.
6. Runner controller failure or orphaned containers.
7. Provider outage, rate limit, or unexpected cost spike.
8. Tool broker authorization anomaly.
9. Unknown/duplicate external action.
10. Email or OAuth credential compromise.
11. Document malware or extraction incident.
12. Stripe webhook backlog or billing/entitlement divergence.
13. Hot Account/noisy-neighbor saturation or failed autoscaling.
14. Failed Account cell move or cell schema migration.
15. Restore one Account, one cell, or global control data.
16. Cell evacuation or regional disaster recovery.
17. Bad release rollback.

Runbooks list detection, immediate safety actions, diagnosis, recovery, validation, communication, and follow-up evidence.

## 9. Release gates

### Pull request

- Format/static/dependency/secret checks pass.
- Relevant unit, integration, contract, replay, and frontend tests pass.
- API and migration changes include compatibility analysis.
- Security-sensitive changes identify threat-model impact.
- New failure states include observability and user recovery.

### Release candidate

- Full test and browser suites pass from the built artifact.
- Migration fleet simulation passes.
- Temporal replay corpus passes.
- Vulnerability and license policy passes.
- Staging synthetic journeys and soak window pass.
- Rollback compatibility verified.
- Release notes include schema/workflow/API compatibility.

### General availability

- Production-readiness review signed by engineering, security, product, and operations owners.
- Backup restore and incident exercises meet targets.
- SLO dashboards and alerts have staging/canary evidence.
- Penetration-test findings are closed or explicitly risk-accepted.
- Canary Account/cell cohort meets stability, isolation, scaling, and reconciliation windows.
- Support and incident teams have exercised the runbooks.

## 10. Production-readiness review checklist

- [ ] Product invariants are implemented and traced to tests.
- [ ] Module ownership and dependency rules pass automatically.
- [ ] User, Account, Membership, entitlement, capability, object, and RLS authorization matrices pass.
- [ ] Free signup and Stripe duplicate/out-of-order/failure/reconciliation suites pass.
- [ ] Threat model is current and mitigations complete.
- [ ] Secrets and keys have rotation procedures.
- [ ] Provider and connector degraded modes are verified.
- [ ] Idempotency and reconciliation cover every retryable effect.
- [ ] Temporal replay corpus passes.
- [ ] Global/cell schema migration, Account move, cohort rollout, and application rollback are rehearsed.
- [ ] Backup and restore evidence is current.
- [ ] SLOs, dashboards, alerts, and runbooks are owned.
- [ ] Many-account load, noisy-neighbor, autoscaling, soak, and failure-injection results meet budgets.
- [ ] Accessibility review passes critical journeys.
- [ ] Privacy retention, export, and deletion are implemented.
- [ ] Artifacts are signed with SBOM and provenance.
- [ ] No production dependency imports or executes prototype code.
