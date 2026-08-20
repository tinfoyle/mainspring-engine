# Account movement operations

- Status: executable Phase 2.5 PostgreSQL foundation
- Process: short-lived `spyglass account-move-admin <action>` job
- Scope: one Account, one declared source cell and one declared destination cell
- Authority: signed, action/environment/reason/scope-bound operator authorization

## Safety model

The global database owns the durable workflow and placement authority. Each cell owns an Account-scoped checkpoint. The operator process never discovers or substitutes cell endpoints: the configured source and destination cell IDs must exactly match the durable move record.

The normal path is:

```text
prepared -> drained -> copied -> ready -> switched -> retiring -> completed
                | pause/resume at a stable pre-switch state |
switched -> rolled_back
```

Every `advance` call performs at most one durable phase. A lease and optimistic move version prevent concurrent operators from advancing the same move. Global move events are immutable and every state change carries a unique event ID, operator identity, bounded reason and environment.

### Freeze-first change boundary

The source namespace enters `moving` before data is read, which prevents new customer requests. The database refuses that transition while any capacity-release or runner invocation is unfinished, or while Agent dispatch/projection is non-terminal. This makes the captured PostgreSQL snapshot a zero-delta high-water mark rather than an eventually consistent best effort.

The copier discovers every `spyglass` table with an `account_id`, requires a primary key, computes the foreign-key dependency order, and fails if source and destination schemas differ. It copies in a repeatable-read source transaction and serializable destination transaction. A canonical per-table row manifest and SHA-256 content digest are recorded in both cells and globally. Reconciliation re-reads both cells; any source or destination mutation changes the evidence and blocks the placement switch.

Tables outside PostgreSQL are not silently assumed complete. Phase 3 must register copy, reconciliation, rollback and retirement handlers before enabling any new Account-owned object store, search index, workflow store or provider reference. Production movement certification covers every enabled handler.

### Placement and rollback

The global switch is one transaction: it compare-and-swaps the Account and directory generation, moves the exact cell capacity counters, records the rollback deadline and appends the event. Destination activation is retryable after that commit; a process crash cannot perform a second placement switch.

Rollback is allowed only before the recorded deadline. It performs another atomic global switch with a new generation—never generation reuse—then activates the retained source and freezes the destination. Activation can likewise be retried without repeating the global rollback.

After the rollback window expires, `retire` deletes the source Account rows in reverse dependency order, removes its namespace, retains a retired checkpoint, and marks the global move complete. A live retirement lease fails closed. After lease expiry, another operator job can resume the same idempotent retirement.

## Operator sequence

1. Confirm the destination cell is active, has capacity, uses the same applied cell schema and is included in monitoring.
2. Resolve all non-terminal source technical queues. Do not discard dead letters merely to make a move pass.
3. Run `prepare` with an exact Account confirmation and rollback window.
4. Run `inspect`; archive the content-free move identity, state, version, cells and generations.
5. Run `advance` once per phase and inspect between calls: `drained`, `copied`, `ready`, then `switched`.
6. Exercise the Account through the routed destination and monitor reconciliation, errors and queue health for the rollback window.
7. Run `rollback` before expiry if acceptance fails; otherwise run `retire` after expiry.
8. Verify final directory placement, cell capacity counts and cell checkpoints.

`pause` and `resume` require `SPYGLASS_ACCOUNT_MOVE_VERSION` from the most recent inspection. They are intended for reviewed holds before the global switch, not as an emergency stop after placement has changed.

## Configuration

All actions require:

- `SPYGLASS_GLOBAL_DATABASE_URL`
- `SPYGLASS_OPERATOR_ID`, `SPYGLASS_OPERATOR_REASON`
- `SPYGLASS_ENVIRONMENT` and matching `SPYGLASS_CONFIRM_ENVIRONMENT`
- the common signed operator-authorization values
- optional `SPYGLASS_ACCOUNT_MOVE_LEASE` (`30s` through `1h`, default `15m`)
- optional `SPYGLASS_ACCOUNT_MOVE_OPERATION_TIMEOUT` (`5m` through `24h`, default `1h`)

`prepare` additionally requires `SPYGLASS_ACCOUNT_ID`, matching `SPYGLASS_CONFIRM_ACCOUNT_ID`, `SPYGLASS_ACCOUNT_MOVE_DESTINATION_CELL_ID`, and an optional `SPYGLASS_ACCOUNT_MOVE_ROLLBACK_WINDOW` (`5m` through `168h`, default `24h`).

All other actions require `SPYGLASS_ACCOUNT_MOVE_ID`. `pause` and `resume` require the expected move version. `advance`, `rollback` and `retire` require the source/destination database URLs and exact source/destination cell IDs.

The operator authorization scope binds all of those target values plus the lease and rollback window. Changing an environment value requires a newly signed authorization.

## Database authority

Use a separate global credential and separate source/destination cell credentials in a short-lived job:

- the global role receives schema usage and execute rights only on the eight Account-movement functions;
- the source cell role receives execute rights on movement checkpoint functions plus read/delete rights on the enumerated Account-owned tables;
- the destination cell role receives execute rights plus read/insert/delete rights on those same tables;
- neither cell role receives `BYPASSRLS`, superuser, serving-session, provider, Stripe, SMTP or Kubernetes authority.

The direct cell table rights are deliberately visible: the generic copier cannot be execute-only while discovering future Account tables. Generate and review the exact grant inventory from the applied schema for each release. A newly added `account_id` table with no primary key, a dependency cycle, or source/destination drift fails closed.

## Failure diagnosis

| Failure | Meaning | Recovery |
|---|---|---|
| `source Account has unfinished technical work` | A source queue can still mutate durable Account state | Inspect and complete/recover the exact queue; retry the same `advance` |
| schema mismatch or missing primary key | Copy coverage is not deterministic | Align migrations or define the missing durable identity; do not bypass |
| reconciliation failed | Row manifests/digests differ or source changed after freeze | Keep placement on source, inspect both checkpoints, repair and repeat copy/reconcile |
| configured cells do not match durable move | Job endpoints could target the wrong databases | Correct configuration and obtain a new scoped authorization |
| lease/state conflict | Another operator owns the phase or the expected state/version changed | Inspect; wait for lease expiry only when the owner is confirmed dead |
| activation failure after switch/rollback | Global placement committed but a cell namespace has not converged | Retry the same action; durable state makes activation idempotent |

Never edit move records, generations, manifests, digests, cell counters or checkpoints manually. Preserve database and operator audit evidence without exporting customer row content.

## Verification evidence

Repository tests use disposable global, source and destination PostgreSQL 17 databases to prove queue gating, copy and digest reconciliation, placement switching, capacity accounting, destination activation, generation-safe rollback and post-window source retirement. Unit tests reconstruct the service after destination and rollback activation failures and prove that the committed global transition is not repeated.
