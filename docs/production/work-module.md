# Work module production design

Status: domain, command/query application boundaries, cell schema, PostgreSQL adapter, signed read transport, first queue/detail browser surface, and durable capacity-release reconciler implemented. Mutation admission/transport remain.

## Purpose

Work is Spyglass's authoritative operational queue. It gives people and governed Personas one visible place for quick to-dos and structured tickets. Temporal or another workflow engine may coordinate execution, but workflow history never becomes the source of truth for the state a customer sees.

The Mainspring prototype remains the experience reference. Its useful concepts survive; its per-customer database assumption, raw strings, unrestricted status updates, cross-module SQL effects, and in-memory child filtering do not.

| Prototype concept | Production Spyglass treatment |
| --- | --- |
| One queue for to-dos and tickets | Preserved as typed `todo` and `ticket` kinds |
| Open, in progress, waiting, done, canceled | Preserved behind an explicit transition table and role policy |
| Low, normal, high, urgent | Preserved as validated priority values |
| Human/agent responsibility | Expanded to user, Persona, shared queue, and bounded external reference |
| Parent and subtasks | Composite Account foreign key, immutable parent, maximum depth three, direct indexed query |
| Source badges | Structured creation provenance with optional baseline, schedule, conversation, and run references |
| Sequential ticket references | Account-local monotonic numbers, never a global sequence |
| Inline state actions | Retained in the future UI, backed by expected-version commands |
| Summary cards and queue filters | Efficient Account-scoped aggregate and cursor queries |

## Ownership and deployment boundary

The global control database owns User identity, Account membership, cell placement, Catalog, Entitlements, and usage counters. A cell database owns Work items for many Accounts. Every Work table includes immutable `account_id`, every relationship includes `account_id`, and every adapter operation runs through a transaction that sets `app.account_id` locally for forced PostgreSQL RLS.

Work code is split into three boundaries:

- `internal/modules/work`: values, construction invariants, state machine, assignment policy, and optimistic version behavior. It has no PostgreSQL or HTTP dependency.
- `internal/application/work`: authenticated commands and bounded queries, shared package enforcement, role checks, active-item admission, and cross-database compensation semantics.
- `internal/adapters/postgres/work.go`: explicit cell SQL, Account-scoped transactions, persistence mapping, cursor queries, event append, and error classification. No pgx type escapes this adapter.

The private Account service does not connect directly to cell data. Work reads pass through the app router, which authenticates the system-wide session, resolves current placement, signs Account route context, and sends the request to the correct cell `app-api`. The cell service verifies the signature, placement generation, Account, resource scope, and local entitlement context before the repository opens its RLS transaction.

## Aggregate and invariants

A Work item contains:

- Account ID, UUID ID, and Account-local display number.
- Optional immutable parent and derived depth.
- Kind, title, bounded description, state, priority, assignment, and due time.
- Creation provenance and creating actor.
- Capacity reservation identity and release checkpoint.
- Completion time, optimistic version, and timestamps.

Construction rejects malformed UUIDs, self-parenting, invalid enum values, titles outside 2–240 characters, descriptions over 20,000 characters, malformed provenance references, inconsistent assignments, and invalid capacity reservations. Persistence restoration repeats domain validation so a corrupt row is classified rather than exposed.

Assignment is exactly one of:

- `user`: one system-wide User UUID.
- `persona`: one cell-local Persona UUID.
- `shared`: no individual assignee; the Account queue owns it.
- `external`: a 2–200 character reference, without pretending the external party is a Spyglass identity.

Creation provenance has one primary source: manual, system, baseline, schedule, conversation, or run. Source-specific creation requires its corresponding UUID. Other provenance links may be added later without rewriting the historical creation source.

Parent is immutable in the initial release. A new child must reference a visible parent in the same Account and may not exceed depth three. Because a new UUID can only point to an already-persisted parent and parent cannot later move, cycles are impossible through public commands. The database independently verifies parent depth and the composite Account relationship.

## Lifecycle and roles

Allowed transitions are:

```text
open -> in_progress -> waiting -> in_progress -> done
  |          |           |
  +----------+-----------+----> canceled

done -- authorized reopen with reason --> open
```

Canceled is terminal. Waiting, cancellation, and reopen require a bounded operational reason. Every command supplies actor, expected version, correlation identity, and time. Owners and administrators may use every valid transition. Members may start, wait, resume, and complete ordinary work, but may not cancel or reopen. Viewers and billing administrators cannot mutate Work. Package read-only mode permits queries and rejects mutations before customer data is loaded.

Every update uses `WHERE account_id = ? AND id = ? AND version = ?`. The winning write increments the version and appends a redacted Work event in the same cell transaction. A stale assignment or completion returns a stable conflict; it never becomes last-write-wins.

## Package and capacity admission

All Work reads require the Work package. Create, assign, and transition require Work mutation access. Creation reserves one governed `work.active_items` unit in the global control database using the caller's UUID request as both the idempotency identity and Work item ID. The cell create is idempotent for the exact same draft and rejects reuse with different content.

This is intentionally not represented as a distributed transaction:

1. Authorize current Account membership, package mode, and role.
2. Reserve capacity with entitlement-version fencing.
3. Create the cell Work item and mutation event.
4. If cell creation fails, release the reservation using the same idempotency key.

Done or canceled work releases its active capacity. The cell row records `capacity_released_at` only after the global release succeeds. If global release or the checkpoint write fails after the visible transition, the service returns `ErrCapacityReleasePending`. Reopen obtains a new reservation before the optimistic cell update and compensates it if another writer wins.

The terminal Work update and an identifier-only `work_capacity_release_queue` row commit in the same cell transaction through a database trigger. Shared `work-reconciler` replicas claim jobs with unique leases and `FOR UPDATE SKIP LOCKED`, release the global reservation idempotently, then enter Account RLS scope to checkpoint the matching Work row. A crash after global release is safe because the next lease repeats the same release key. A stale worker cannot complete a reclaimed lease. If an item reopens before its old release completes, the old job completes without marking the new reservation released.

The reconciler has separate cell and global pools. Its cell credential can lease only the technical outbox and update Account-scoped Work checkpoints; its global credential can only read Account existence and release usage reservation/counter rows. App-api receives neither global credential nor cross-database release code. `/health/status` exposes pending, processing, dead-letter, and oldest-pending-age values without customer content.

## Persistence and query contract

The cell migration adds:

- `work_item_number_counters`: one locked counter row per Account.
- `work_items`: the aggregate with composite keys, checks, forced RLS, queue/child/state/assignment indexes, and pending-release index.
- `work_item_events`: append-only created, transitioned, and assigned facts with actor, versions, correlation, reason, and redacted payload.
- `work_capacity_release_queue`: identifier-only leased technical outbox, retry/dead-letter state, and completion checkpoint; it contains no title, description, assignment, provenance, or other customer content.

Queue pagination orders by `(updated_at DESC, id DESC)` and carries both values in the cursor. Filters are bounded to known states/kinds plus a 200-character search term. Direct children use `(account_id, parent_id, created_at, id)` rather than loading an arbitrary queue page and filtering in memory. The summary returns active, in-progress, waiting, urgent-active, and done counts from one Account-predicated query.

Repository errors are limited to not found, conflict, constraint, corruption, or unavailable. The read transport maps these to the shared problem-details vocabulary without exposing PostgreSQL messages or revealing resources in another Account.

The first public read contract is Account-scoped and bounded:

```text
GET /api/v1/accounts/{accountID}/work-items
GET /api/v1/accounts/{accountID}/work-items/summary
GET /api/v1/accounts/{accountID}/work-items/{itemID}
GET /api/v1/accounts/{accountID}/work-items/{itemID}/children
```

List filters accept repeated known `state` and `kind` values, one bounded search term, one page limit, and one opaque versioned cursor. Detail emits a weak ETag from the aggregate version. Explicit response DTOs exclude capacity reservation and release bookkeeping.

## Product surface plan

The first Work screen carries forward the prototype's strongest visual ideas inside the current Spyglass shell:

- A restrained header with active, in-progress, waiting, urgent, and done summary cards.
- State and kind filters, bounded search, and stable cursor continuation.
- Paper-like queue cards with `#0001`, kind/state/priority, responsibility, and due time.
- A sticky detail surface with description, direct children, provenance, responsibility, due time, and aggregate version-backed detail reads.
- Package-disabled and package-read-only states derived from the same server decision as the API, never navigation alone.

Creation and inline mutation controls remain absent until command admission can reserve global capacity without giving app-api a broad global credential. At that point the UI will send idempotency keys on create and `If-Match`/expected version on mutation. A version conflict will reload the item, preserve the operator's draft where safe, and explain the winning change.

## Remaining delivery order

1. Design command admission so app-api can coordinate global capacity without receiving a broad global database credential; then publish create, assign, and transition HTTP contracts.
2. Add audited operator inspection/requeue for Work release dead letters and retention for completed technical jobs.
3. Add inline transition and creation surfaces with idempotency keys, ETags/expected versions, preserved drafts, and explicit conflict recovery.
4. Replace the static route map with the bounded directory cache and internal TLS identity described in [routing-boundary.md](routing-boundary.md).
5. Add provenance attachment and conversation-link commands, transactional events, and authorization tests.
6. Add representative query-plan fixtures, pagination property tests, concurrent completion/assignment stress, and two-cell cross-Account API attack fixtures.
7. Characterize and migrate prototype Work data Account by Account; verify numbers, hierarchy, state, assignment, provenance, and active-capacity reconciliation before switching traffic.
8. Add Attention concepts—human input, review, approval, and external action—as separate aggregates that reference Work rather than expanding Work into another catch-all store.

## Current evidence and limits

Table-driven domain tests cover every state/role pair and reject invalid construction, stale versions, and missing reasons. Application tests cover role denial, capacity admission, failed-create compensation, and terminal release. The disposable PostgreSQL 17 contract applies every migration twice, runs through a non-owner role, proves guessed cross-Account Work IDs are invisible, exercises Account-local summary/list queries, and proves a stale writer loses after a competing transition.

Signed Account-scoped list, summary, detail, and direct-child routes now run through app-router and cell app-api. The browser shell renders locked package state without a data request and loads entitled queues only through those routes. Transport tests cover malformed filters and cursors, safe DTO fields, cross-Account paths, package authority, ETags, and response naming. Reconciliation tests prove atomic terminal enqueue, idempotent global release, lease exclusion/reclaim, stale-lease rejection, synchronous checkpoint completion, least-privilege role separation, and reopen-before-old-release safety. There is not yet a Work mutation HTTP surface, audited dead-letter requeue command, Persona foreign key, representative-scale query-plan result, or applied Kubernetes environment.
