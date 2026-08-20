# Phase 3 report — Work and Attention

- Audit date: 2026-08-20
- Audited revision: `b195fae07b264ea4a609e424d6664f0b776eb6a4`
- Source of truth: Phase 3 in [delivery-plan.md](delivery-plan.md)
- Verdict: **P3.1 and P3.2 are advanced, P3.5 has a security foundation, and P3.3, P3.4 and most P3.6 remain**

## Current implementation

| Work package | Status | Repository finding |
|---|---|---|
| P3.1 Work domain | Mostly implemented | Typed values, construction, state/role matrix, depth, assignment values, provenance values and optimistic versions exist. Persona-backed assignment, assignment editing and provenance attachment remain. |
| P3.2 Persistence and queries | Mostly implemented | Forced-RLS schema, Account-local numbering, stable queue cursors, child query, summaries, events, routed commands, capacity reconciliation and dead-letter tooling exist. Persona foreign keys, provenance/conversation links, representative query plans, pagination properties and concurrency stress remain. |
| P3.3 Agent-work dispatcher | Not implemented as a Work boundary | Agent Run dispatch and generic runner control exist, but there is no Work-owned claim/start-run/link/heartbeat/release/resume/reconcile state machine. The Agent dispatch queue is not a substitute. |
| P3.4 Attention domain | Not implemented | No production Attention package, persistence, routes or UI exists for `InformationRequest`, `WorkReview` or `ConsequentialApproval`. Placeholder “Your Turn” copy is not a domain boundary. |
| P3.5 Action ledger | Partial | Forced-RLS authorization, action ledger, attempts, execute/reconcile separation and unknown-outcome handling exist. Attention-owned approvals, a real consequential adapter, failed retry policy, manual resolution, redacted views and operator/customer surfaces remain. |
| P3.6 Transport migration | Partial | Seven routed Work HTTP operations and a private server-rendered/JavaScript Work surface exist. There is no production MCP implementation, no Attention HTTP contract, no HTTP/MCP parity suite, and no private React Work/Your Turn slice. |

The OpenAPI document currently contains 62 operations. Production source contains only a comment mentioning MCP; MCP tools still live in the prototype. The delivery plan's React requirement therefore needs either implementation or an explicit architecture decision changing the target.

## Required delivery sequence

### Slice 1 — finish the Work vertical slice

- Add Persona ownership/foreign keys and safe Persona assignment policy.
- Add assignment-editing UI with expected-version handling and accessible announcements.
- Add provenance-attachment and conversation-link commands, immutable events and authorization tests.
- Preserve create/edit drafts across navigation and replace prompt-like reason handling with an accessible dialog.
- Add representative-cardinality `EXPLAIN (ANALYZE, BUFFERS)` fixtures, stable-pagination property tests, and concurrent assignment/completion stress.
- Characterize prototype Work data and define Account-by-Account migration, capacity reconciliation and rollback checks.

Exit: P3.1/P3.2 acceptance tests pass without relying on prototype runtime code.

### Slice 2 — implement typed Attention

Create `internal/modules/attention`, an application service and cell persistence for three separate aggregates:

1. `InformationRequest`: explicit fact requirements, Account scope, eligible answers, expiration/cancellation and exact parent resumption.
2. `WorkReview`: reviewed Work/version, decision, reviewer authority, evidence and reopen/resume semantics.
3. `ConsequentialApproval`: canonical payload bytes, hash/version, capability, proposer, policy version, evidence, approver separation, expiry and cancellation.

All tables require immutable `account_id`, composite Account-scoped relationships, forced RLS, optimistic versions and immutable events. Answering one fact may complete only eligible requests; changing a proposal must invalidate its approval.

Exit: domain matrices, PostgreSQL isolation/concurrency contracts and redacted query DTOs pass for every aggregate.

### Slice 3 — connect Work to Agent execution durably

Implement a Work-owned dispatcher distinct from the existing Agent invocation dispatcher:

- claim with owner, lease, expiry and heartbeat;
- start one immutable Agent Run with a deterministic operation identity;
- link Work and Run transactionally or through a reconciled outbox;
- release, resume and reconcile commands;
- capacity accounting based on active executions;
- event wakeup plus jittered periodic recovery;
- crash tests before Run creation, after creation, after link, during heartbeat loss and after completion.

Exit: every crash point converges to one active linked Run or one safely requeued Work item, with no duplicate customer-visible effect.

### Slice 4 — finish consequential actions

- Make `ConsequentialApproval` the owner of the existing runner authorization projection.
- Add an executor registry and at least one sandbox/test provider adapter with real idempotency and side-effect-free reconciliation.
- Define explicit retry rules for definite failure versus unknown outcome.
- Add dual-control manual resolution for high-risk/unknown actions.
- Add redacted customer/operator views, immutable audit history, alerting and a recovery runbook.
- Test timeout after provider acceptance, duplicate approval/execute, cancellation races, lease expiry and reconciliation after restart.

Exit: repeated approval/execution performs at most one effect, and ambiguous acceptance remains `unknown` until reconciled or manually resolved.

### Slice 5 — complete transport and product parity

- Publish Attention queue/detail/answer/review/approve/cancel/action-status HTTP operations through the same commands and queries.
- Implement production MCP tools for Work and Attention over those same application boundaries.
- Add HTTP/MCP authorization and outcome parity tests.
- Build the private React Work and Your Turn slices, or accept and document a replacement architecture before claiming P3.6 complete.
- Add compatibility fixtures against prototype behavior without importing or executing prototype code in production.
- Extend accessibility certification to dynamic status announcements, dialogs, error recovery and streamed Agent/Attention updates.

Exit: every customer-visible use case has one canonical application implementation, stable generated contracts and equivalent HTTP/MCP outcomes.

### Slice 6 — staging certification and cutover

- Apply migrations through constrained roles in connected staging.
- Run multi-Account isolation, representative query/load, queue crash recovery, runner compromise and provider degradation exercises.
- Rehearse Work/Agent/Attention/action dead-letter and unknown-action runbooks.
- Migrate a synthetic and then internal Account from prototype fixtures; reconcile numbers, hierarchy, state, assignments, provenance, active capacity, Attention and action history.
- Canary by Account cohort with rollback to the retained artifact and compatible schema.

## Phase 3 definition of done

Phase 3 is complete when:

- all P3.1-P3.6 acceptance statements in [delivery-plan.md](delivery-plan.md) pass;
- Work, Attention, Agent-linked execution and external actions survive every documented crash boundary;
- HTTP and MCP share authorization, commands, queries, errors and audit behavior;
- private product surfaces are accessible and recover drafts/state after session, network and concurrency failures;
- representative cell data meets query, connection, latency and fairness budgets; and
- staging/canary evidence is tied to the exact signed artifact and published Work entitlement.

Knowledge/Baseline, Workspace orchestration, Finance, Marketing and other packages remain later phases. Existing Agent foundations reduce Phase 3 implementation cost but do not change the Phase 3 acceptance boundary.
