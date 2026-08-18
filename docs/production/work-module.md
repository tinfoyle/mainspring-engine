# Work module production design

Status: domain, application boundary, cell schema, PostgreSQL adapter, and isolation/concurrency contract implemented; trusted app transport and UI pending.

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

The private Account service must not connect directly to arbitrary cell data as a convenience. Work routes will land only after the app router can authenticate the system-wide session, resolve current placement, sign Account route context, and send it to the correct cell `app-api`. A cell service verifies the signature, placement generation, Account, resource scope, and local entitlement context before opening its RLS transaction.

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

Done or canceled work releases its active capacity. The cell row records `capacity_released_at` only after the global release succeeds. If global release or the checkpoint write fails after the visible transition, the service returns `ErrCapacityReleasePending`; the terminal row remains discoverable through the partial index for an idempotent reconciler. Reopen obtains a new reservation before the optimistic cell update and compensates it if another writer wins.

Before transport rollout, add the leased capacity-release reconciler and an operator-visible age/error metric. This closes the crash window without claiming distributed atomicity.

## Persistence and query contract

The cell migration adds:

- `work_item_number_counters`: one locked counter row per Account.
- `work_items`: the aggregate with composite keys, checks, forced RLS, queue/child/state/assignment indexes, and pending-release index.
- `work_item_events`: append-only created, transitioned, and assigned facts with actor, versions, correlation, reason, and redacted payload.

Queue pagination orders by `(updated_at DESC, id DESC)` and carries both values in the cursor. Filters are bounded to known states/kinds plus a 200-character search term. Direct children use `(account_id, parent_id, created_at, id)` rather than loading an arbitrary queue page and filtering in memory. The summary returns active, in-progress, waiting, urgent-active, and done counts from one Account-predicated query.

Repository errors are limited to not found, conflict, constraint, corruption, or unavailable. Public transports will map these to the shared problem-details vocabulary without exposing PostgreSQL messages or revealing resources in another Account.

## Product surface plan

The first Work screen should carry forward the prototype's strongest visual ideas inside the current Spyglass shell:

- A restrained header with active, in-progress, waiting, urgent, and done summary cards.
- Status tabs, kind filter, bounded search, and stable cursor continuation.
- Paper-like queue cards with `#0001`, kind/state/priority, provenance, responsibility, assignee, due time, and permitted inline transition actions.
- A right-side creation panel on wide screens and a focused sheet on small screens.
- A detail route with description, child list, lifecycle history, provenance, assignment, due time, and version-conflict recovery.
- Package-disabled and package-read-only states derived from the same server decision as the API, never navigation alone.

The UI will send idempotency keys on create and `If-Match`/expected version on mutation. A version conflict reloads the item, preserves the operator's draft where safe, and explains the winning change.

## Remaining delivery order

1. Add a leased cell scan/reconciliation command for terminal rows whose global capacity release is not checkpointed.
2. Replace the implemented signed route-context/static route boundary with the bounded directory cache and internal TLS identity described in [routing-boundary.md](routing-boundary.md).
3. Extend the executable cell `app-api` mode from its Account probe to Work commands and queries without adding global customer-data ownership.
4. Publish generated Work command/query contracts and problem mappings; add session-backed router endpoints.
5. Build the Work queue/detail surfaces in the Spyglass shell using the production API, not direct repository calls.
6. Add provenance attachment and conversation-link commands, transactional events, and authorization tests.
7. Add representative query-plan fixtures, pagination property tests, concurrent completion/assignment stress, and cross-Account API attack fixtures.
8. Characterize and migrate prototype Work data Account by Account; verify numbers, hierarchy, state, assignment, provenance, and active-capacity reconciliation before switching traffic.
9. Add Attention concepts—human input, review, approval, and external action—as separate aggregates that reference Work rather than expanding Work into another catch-all store.

## Current evidence and limits

Table-driven domain tests cover every state/role pair and reject invalid construction, stale versions, and missing reasons. Application tests cover role denial, capacity admission, failed-create compensation, and terminal release. The disposable PostgreSQL 17 contract applies every migration twice, runs through a non-owner role, proves guessed cross-Account Work IDs are invisible, exercises Account-local summary/list queries, and proves a stale writer loses after a competing transition.

There is not yet a production Work HTTP route, browser screen, release reconciler, Persona foreign key, or representative-scale query-plan result. The signed app-router/cell app-api Account probe is executable, but it does not yet expose Work. The Kubernetes resources remain review-only until the remaining operational boundaries exist.
