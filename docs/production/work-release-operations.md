# Work Release Dead-Letter Operations

Status: executable one-shot operator workflow

Work capacity release crosses a cell database and the global control database. The normal `work-reconciler` owns automatic retries. A human operator intervenes only after a job reaches `dead_letter`, using `spyglass work-release-admin`; direct SQL is not an operator workflow.

## Safety boundary

The command exposes only Account, Work-item, and reservation UUIDs; attempt count; bounded machine error code; and technical timestamps. It does not read titles, descriptions, assignments, provenance, Memberships, Users, Billing, or other customer content.

The production database role receives only:

```sql
GRANT USAGE ON SCHEMA public, spyglass TO spyglass_work_release_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_inspect_work_capacity_release_dead_letters(uuid,text,text,text,integer)
    TO spyglass_work_release_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_requeue_work_capacity_release_dead_letter(uuid,uuid,uuid,uuid,text,text,text)
    TO spyglass_work_release_operator;
```

Do not grant this role `SELECT`, `INSERT`, `UPDATE`, or `DELETE` on the queue, audit table, Account namespaces, or Work tables. The security-definer functions validate bounded operator metadata and perform inspection/audit or requeue/audit atomically. PostgreSQL supplies the audit and scheduling timestamp; the caller cannot backdate it. PUBLIC has no execute grant. Audit rows reject update and deletion, including by the table owner.

The short-lived job verifies the signed phishing-resistant envelope in [Platform Operator Authorization](operator-authorization.md) before opening the cell database. `SPYGLASS_OPERATOR_ID` records the bound external identity; it is not authentication by itself. The job must use the target cell's operator credential and no global, serving, Stripe, SMTP, or route-signing secret.

## Inspect

Set an environment label and repeat it exactly as an intentional-target check:

```powershell
$env:SPYGLASS_CELL_DATABASE_URL = '<cell operator credential>'
$env:SPYGLASS_OPERATOR_ID = 'release-operator@example.com'
$env:SPYGLASS_OPERATOR_REASON = 'Investigate terminal capacity release failures after database recovery'
$env:SPYGLASS_ENVIRONMENT = 'production-us-east-cell-01'
$env:SPYGLASS_CONFIRM_ENVIRONMENT = 'production-us-east-cell-01'
$env:SPYGLASS_WORK_RELEASE_INSPECT_LIMIT = '50'
spyglass work-release-admin inspect
```

Inspection is bounded to 100 oldest dead letters and is itself audited. Every returned target gets an audit row; an empty inspection records a target-free audit row so use of the privileged surface is still visible. Logs contain the same technical fields and must be routed to the restricted platform-operations sink.

Diagnose the machine error before requeueing:

| Error code | Required action |
|---|---|
| `global_release_unavailable` | Confirm the global database and release credential are healthy; verify backlog trend before requeue |
| `cell_checkpoint_failed` | Confirm the cell database and Account-RLS checkpoint path are healthy |
| `reservation_not_found` | Do not blindly requeue; reconcile migration/import history and capacity facts first |
| `reservation_conflict` | Treat as data-integrity investigation; requeue only after the conflicting identity is resolved |
| `usage_corrupt` | Stop automated intervention and follow the capacity-ledger incident procedure |
| `invalid_job` | Treat as a schema/data-integrity incident; the exact identifiers failed validation |

## Requeue

Copy all three UUIDs from one inspected record. Requeue is allowed only while that exact row is still `dead_letter`:

```powershell
$env:SPYGLASS_OPERATOR_REASON = 'Global database recovered and reservation identity was verified'
$env:SPYGLASS_WORK_ACCOUNT_ID = '<account UUID>'
$env:SPYGLASS_WORK_ITEM_ID = '<work item UUID>'
$env:SPYGLASS_WORK_RESERVATION_ID = '<reservation UUID>'
spyglass work-release-admin requeue
```

The transaction records the previous attempt count and error code, changes the row to `pending`, resets the automatic attempt counter to zero, clears lease/error fields, and schedules the next attempt immediately. It does not release capacity itself and does not alter the Work item. The normal reconciler must perform the idempotent global release and Account-scoped checkpoint.

## Verification and incident closure

1. Confirm the command logged one `requeued` result for the exact triple.
2. Watch `/health/status`; pending should drain and dead-letter count should not rise again.
3. Confirm the operator audit sink contains both the inspection batch and requeue event with actor, reason, environment, prior error, and prior attempt count.
4. Confirm the Work item remains terminal and the usage reservation is released through normal reconciliation evidence. Do not query customer content with the operator credential.
5. Link the immutable audit batch IDs and monitoring evidence to the incident/change record.

Repeated requeue after the row has left `dead_letter` fails with a state conflict and writes no second requeue audit event. If the job dead-letters again, perform a new inspection and use a new reason that records what changed since the prior attempt.
