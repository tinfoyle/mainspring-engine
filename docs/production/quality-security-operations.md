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
| Migration fleet | Release candidate | Upgrade representative tenant snapshots and reconcile invariants |
| Load/soak/failure injection | Scheduled and release gate | Capacity, leaks, recovery, dependency isolation |
| Restore/DR exercise | Scheduled and major release | Prove backups and operational recovery |

### 1.2 Determinism

Tests use injected clocks, ID generators, fake providers, fixed pricing, deterministic embeddings/search fixtures, and controlled connector servers. Retry and timeout tests use virtual workflow time where supported.

No test depends on execution order, a developer mailbox, a shared tenant database, a real clock boundary, or an unversioned remote response.

### 1.3 Test data

- Synthetic only; no production customer content in fixtures.
- Tenant A/Tenant B pairs exist for every authorization and query suite.
- Representative small, medium, and high-cardinality tenant datasets are generated reproducibly.
- Document fixtures cover supported, malformed, oversized, compressed, encrypted, macro-bearing, and adversarial formats.
- Prompt/tool fixtures include injection attempts and forged citations.
- Migration snapshots are scrubbed and generated, with recorded schema versions and hashes.

### 1.4 Critical end-to-end journeys

The release suite must cover:

1. Provision tenant, create owner, authenticate, invite member, and enforce roles.
2. Complete a business baseline and approve its work plan.
3. Upload evidence, retrieve it in an agent run, preserve citations, and recall it later.
4. Create agent-owned work, request owner input, reuse a fact, resume, review, and complete.
5. Run a manager-led boardroom with delegation and synthesis.
6. Propose, approve, execute, and reconcile an external action without duplication.
7. Schedule a run across timezone/DST behavior.
8. Connect and revoke an evidence connector with scoped reads.
9. Create, post, and reverse a balanced finance entry under correct authorization.
10. Perform equivalent authorized operations through MCP and deny unauthorized ones.
11. Lose and restore SSE/network connectivity without lost or duplicated visible state.
12. Expire a session during a draft and recover without leaking or silently submitting it.

## 2. Security program

### 2.1 Threat-model scope

Assets include:

- Tenant identity and membership.
- Business facts, messages, documents, financial records, and email.
- Provider, OAuth, SMTP/IMAP, database, signing, and encryption credentials.
- Capability grants and approval authority.
- Workflow and action integrity.
- Docker/Kubernetes control plane and runner infrastructure.
- Audit records and backups.

Threat actors include unauthenticated internet clients, malicious tenant users, compromised agent/provider output, hostile document/web/email content, compromised connectors, leaked credentials, supply-chain compromise, and privileged operators exceeding intended authority.

Review trust boundaries for edge-to-control, edge-to-tenant, tenant-to-database, workflow-to-worker, worker-to-runner-controller, runner-to-provider, runner-to-broker, broker-to-connector, and backup/restore paths.

### 2.2 Authentication and sessions

Production authentication requires an ADR. Minimum requirements:

- Modern password hashing if passwords remain supported.
- MFA support for owners and platform administrators.
- Short-lived authenticated sessions with rotating opaque tokens.
- Secure, HttpOnly, SameSite cookies and CSRF protection for cookie mutations.
- Session inventory and remote revocation.
- Reauthentication for ownership transfer, credential changes, and high-risk approvals.
- Brute-force, credential-stuffing, enumeration, and recovery abuse controls.
- One-time invitation and recovery tokens stored as hashes with expiry and consumption audit.
- Platform administration isolated from tenant roles and normal tenant sessions.

### 2.3 Authorization

- Central policy vocabulary but module-owned object authorization.
- Deny by default for new commands, routes, MCP tools, and capabilities.
- Role plus relationship checks; role alone is insufficient for sensitive objects.
- Explicit actor propagation through background and workflow commands.
- Service identities are narrow and independently revocable.
- Authorization matrix tests cover role, state, tenant mismatch, ownership, assignment, grant, and object existence.

### 2.4 Tenant isolation

- Immutable tenant ID is derived from trusted routing and authenticated context, never request body alone.
- Physical tenant database credentials remain separate.
- Connection pools cannot be reused across tenant identity without a verified resolver boundary.
- Cache keys, object keys, workflow IDs, task queues, log attributes, metrics, and search indexes include safe tenant identity.
- Cross-tenant test cases exist for every repository and transport.
- Support/operator access is time-bounded, reason-bound, audited, and customer-visible where policy requires.

### 2.5 Agent and prompt security

- Provider output is untrusted data until schema and policy validation completes.
- Retrieved documents, web pages, and email are clearly delimited untrusted evidence.
- Tool calls require signed invocation claims and server-side schemas.
- Capability tokens are short-lived, audience-bound, invocation-bound, and never serialized to durable provider-visible output.
- Citation metadata is bound from returned authorized tool results, not accepted from model claims.
- Agent-produced instructions cannot alter persona grants, budgets, approval policy, or orchestration.
- Sensitive prompt logging is disabled by default and requires audited diagnostic enablement.

### 2.6 Network and SSRF controls

- Egress is deny-by-default for runners.
- Web research occurs through a controlled service boundary.
- URL parsing permits only intended schemes and ports.
- DNS answers and every redirect target are checked against private, loopback, link-local, multicast, metadata, and reserved ranges.
- Rebinding is mitigated by connection-level destination validation where supported.
- Response bytes, decompression, redirects, and duration are bounded.
- Internal service hostnames and credentials never enter public research requests.

### 2.7 File security

- Verify actual content type independently from filename.
- Enforce upload, expanded archive, page, entry, character, and extraction-time limits.
- Quarantine before use and scan with a production malware service.
- Extract in a sandbox with no network and read-only tooling.
- Never execute macros, scripts, active PDF content, or embedded objects.
- Escape or safely render extracted HTML and user-controlled filenames.
- Object storage is private, encrypted, tenant-prefixed, and access logged.

### 2.8 Secret and key management

- No secrets in source, images, Compose files, logs, crash dumps, or workflow payloads.
- Managed secret store or workload identity in production.
- Separate keys by purpose: sessions, capability signing, credential encryption, RAG/service auth, and provider auth.
- Envelope encryption with key version recorded alongside ciphertext.
- Documented rotation and emergency revocation for every credential class.
- Production startup rejects placeholder, short, shared-purpose, or missing secrets.
- Secret scanner runs locally, in CI, and against release history.

### 2.9 Supply chain and runtime

- Pin and review base images and dependencies.
- Build in an isolated reproducible pipeline.
- Generate SBOM and signed provenance.
- Scan source, dependencies, containers, and infrastructure configuration.
- Run services as non-root with read-only roots and minimal Linux capabilities.
- Separate runner-controller privileges from normal application workloads.
- Admission policy accepts only signed images from the release pipeline.
- Critical vulnerability remediation policy has severity-based deadlines and emergency release procedure.

### 2.10 Audit

Audit events cover:

- Authentication, session, invitation, and recovery changes.
- Membership, role, ownership, and administrative access.
- Agent/persona configuration and immutable version creation.
- Capability issuance, tool allow/deny/error, and scope.
- Baseline fact/evidence changes and document revisions.
- Work assignment/lifecycle and human answers.
- Approval decisions and action execution/reconciliation.
- Integration connect, scope change, error, and revoke.
- Finance create/update/post/void/reversal.
- Platform provisioning, migration, support access, export, retention, and deletion.

Audit events are append-only, timestamped, actor-attributed, correlated, redacted, retained by policy, and exportable. Application administrators cannot silently rewrite them.

## 3. Reliability and recovery

### 3.1 Failure model

For every dependency, specify timeout, retry class, circuit behavior, degraded experience, alert, and recovery owner.

| Dependency | Expected degraded behavior |
|---|---|
| Tenant PostgreSQL | Reject stateful requests; do not accept work that cannot be persisted |
| Control PostgreSQL | Existing resolved tenant runtimes continue within safe cache TTL; provisioning/admin pause |
| Temporal | Persist commands where possible and show queued state; no local untracked durable substitute |
| Runner controller | Runs remain queued/retryable; tenant reads and manual work remain available |
| Model provider | Circuit opens by provider; runs remain visible and recoverable |
| Knowledge/RAG | Agents declare evidence capability unavailable; no unsupported answer presented as sourced |
| Email/Drive/research | Only dependent tools degrade; credentials and last successful sync remain visible |
| Object storage | Upload/download pause; metadata remains consistent and no partial ready state |
| Telemetry backend | Application continues with bounded local buffering/drop policy; never block customer commands indefinitely |

### 3.2 Idempotency

Required for:

- Tenant provisioning and inbound billing events.
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

Idempotency records include key, operation kind, actor/tenant, canonical request hash, status, response reference, expiry/retention, and conflict behavior.

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
- Tenant provisioning resources versus registry state.

Reconcilers are bounded, observable, idempotent, and runnable in dry-run mode. They use application commands, not hidden cross-module SQL fixes.

### 3.4 Backup and restore

- Automated encrypted control and tenant database backups with point-in-time recovery.
- Encrypted object-store versioning/replication according to retention policy.
- Backup catalog associates tenant, schema version, application compatibility, and encryption-key version.
- Restore into an isolated environment first.
- Run migrations only after verifying the restored schema and artifact compatibility.
- Reconcile Temporal state, schedules, actions, connector checkpoints, and indexes.
- Rotate exposed or environment-specific credentials during restore.
- Run synthetic tenant journeys and data-integrity checks before reopening traffic.

Restore tests sample different tenant sizes and schema ages. A backup that has not been restored successfully within the policy window is considered unverified.

### 3.5 Disaster recovery

The initial proposed targets are RPO 15 minutes and RTO 4 hours. Final targets require business approval and cost analysis.

Exercises cover:

- Loss of one application zone.
- Loss/corruption of tenant database infrastructure.
- Loss of control database.
- Loss of object storage region.
- Temporal cluster failure and recovery.
- Credential/key compromise.
- Bad application release and bad migration.

## 4. SLOs, metrics, and alerts

### 4.1 Proposed SLIs

- Successful authenticated tenant API requests divided by eligible requests.
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
- Cost and token usage by tenant, run kind, provider, and model.

### 4.2 Alert design

Alerts must identify user impact, scope, owning service, likely cause, and runbook. Page on sustained actionable symptoms rather than every retry.

Page-worthy conditions include:

- Tenant isolation or authorization correctness signal.
- Consequential action duplication or unexplained payload mismatch.
- Broad API SLO burn.
- Database unavailable or restore/backup outside objective.
- Workflow backlog with customer-visible delay.
- Runner orphan growth or controller compromise signal.
- Unknown external actions exceeding age threshold.
- Migration failures across an active rollout cohort.

Ticket-worthy conditions include gradual capacity saturation, provider cost regression, retrieval-quality regression, non-critical connector error increases, and accessibility/performance budget drift.

## 5. Performance and capacity

Define capacity in terms of active tenants, users, HTTP concurrency, SSE connections, documents/bytes/chunks, work items, conversations/messages, schedules, concurrent invocations, provider tokens, and connector sync volume.

Required tests:

- API read/write load with representative database cardinality.
- Thousands of idle and active SSE connections.
- Burst of scheduled runs at common clock boundaries.
- Concurrent agent-owned work claims and owner-answer resumes.
- Large but valid document ingestion and reindex.
- Provider slowdown and rate limiting.
- Tenant-fleet startup, migration, backup, and reconciliation.
- 24-hour minimum soak for connection, goroutine, memory, and disk leaks before general availability.

Performance budgets include database query count and bytes, API payload size, JavaScript route chunk size, rendering responsiveness, and external call concurrency.

## 6. Privacy and data lifecycle

Classify at minimum:

- Public platform content.
- Tenant metadata.
- Confidential business content.
- Highly sensitive credentials and authentication material.
- Financial and personnel-related business records.
- Security/audit data.

For each class define collection purpose, authorized roles/services, encryption, logging rules, retention, export, deletion, backup expiry, and support access.

Tenant deletion is a durable workflow with owner/platform authorization, cooling-off policy, active-run cancellation, connector revocation, database/object deletion, backup-expiry tracking, and final auditable tombstone without retained business content.

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

- Tenant/runtime health and placement.
- Workflow/run lookup and safe retry/cancel/reconcile.
- Stuck work lease inspection.
- Attention and action reconciliation.
- Provider circuit status and controlled reset.
- Connector health and credential revocation.
- Document ingestion/reindex retry.
- Migration cohort status and pause/rollback.
- Backup/restore execution and evidence.
- Tenant export/deletion progress.
- Time-bounded support access with reason and audit.

Minimum runbooks:

1. API SLO burn.
2. Tenant cannot authenticate.
3. Suspected cross-tenant access.
4. PostgreSQL outage or corruption.
5. Temporal backlog or nondeterminism.
6. Runner controller failure or orphaned containers.
7. Provider outage, rate limit, or unexpected cost spike.
8. Tool broker authorization anomaly.
9. Unknown/duplicate external action.
10. Email or OAuth credential compromise.
11. Document malware or extraction incident.
12. Failed tenant migration.
13. Restore one tenant.
14. Regional disaster recovery.
15. Bad release rollback.

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
- Canary tenant cohort meets stability and reconciliation window.
- Support and incident teams have exercised the runbooks.

## 10. Production-readiness review checklist

- [ ] Product invariants are implemented and traced to tests.
- [ ] Module ownership and dependency rules pass automatically.
- [ ] Tenant and object authorization matrices pass.
- [ ] Threat model is current and mitigations complete.
- [ ] Secrets and keys have rotation procedures.
- [ ] Provider and connector degraded modes are verified.
- [ ] Idempotency and reconciliation cover every retryable effect.
- [ ] Temporal replay corpus passes.
- [ ] Schema migration, fleet rollout, and application rollback are rehearsed.
- [ ] Backup and restore evidence is current.
- [ ] SLOs, dashboards, alerts, and runbooks are owned.
- [ ] Load, soak, and failure-injection results meet budgets.
- [ ] Accessibility review passes critical journeys.
- [ ] Privacy retention, export, and deletion are implemented.
- [ ] Artifacts are signed with SBOM and provenance.
- [ ] No production dependency imports or executes prototype code.
