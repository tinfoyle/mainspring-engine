# Schedule Queue Operations

Status: executable operator contract and applied local and Hostinger stage rehearsals complete; production role grants remain a release gate

Recurring occurrences and trigger-now requests use independent identifier-only durable queues. A terminal row never retries forever and never disappears automatically. Operators can inspect one queue in one cell and, after correcting the underlying condition, requeue one exact recurring Schedule or trigger request.

## Safety contract

- Run `spyglass schedule-queue-admin inspect|requeue` as a short-lived controlled Job, never as a standing service.
- The credential receives `USAGE` on `public` and `EXECUTE` on only `spyglass_inspect_schedule_queue_dead_letters` and `spyglass_requeue_schedule_queue_dead_letter`. It receives no table grants.
- Every invocation requires exact environment confirmation and a phishing-resistant signed authorization whose scope binds action, queue, inspection limit, Account, Schedule and optional trigger.
- Inspection is capped at 100 records and returns technical identifiers, attempt count, bounded error code, occurrence time and audit timestamps only.
- Requeue accepts only a current `dead_letter`. It clears leases and failure metadata, resets attempts and makes the row immediately claimable. A repeated or stale action fails without mutation.
- Inspection—including an empty result—and requeue write immutable evidence in the same transaction. Account-attributed evidence participates in exact erasure and restore proofs.
- Neither command reads or logs the Schedule definition, prompt, subject, Persona selection, context references, Conversation, Agent result, credential or ciphertext.

## Invocation

Set the common operator variables described in [Platform Operator Authorization](operator-authorization.md), plus:

```text
SPYGLASS_CELL_DATABASE_URL=<execute-only cell operator role>
SPYGLASS_SCHEDULE_QUEUE=recurring|trigger
SPYGLASS_SCHEDULE_QUEUE_INSPECT_LIMIT=50
```

Inspect first, diagnose the bounded `last_error_code`, correct the dependency or policy condition, then obtain a new exact-scope authorization for requeue:

```text
SPYGLASS_SCHEDULE_ACCOUNT_ID=<uuid>
SPYGLASS_SCHEDULE_ID=<uuid>
SPYGLASS_SCHEDULE_TRIGGER_ID=<uuid> # trigger queue only
spyglass schedule-queue-admin requeue
```

Do not requeue deterministic authorization, package-access or validation failures until the underlying state has been reviewed. A recurring requeue retains its original `scheduled_for`; a trigger requeue retains its immutable request and `requested_for`. After requeue, confirm the relevant dead-letter gauge decreases, the ready gauge advances or the occurrence completes, and recurrence version/`next_run_at` changes only for a successful recurring occurrence.

## Stage certificate and production grant

The 2026-08-22 Hostinger rehearsal exercised populated and empty inspection for both queues, exact requeue, wrong-Account rejection, non-terminal/duplicate rejection, immutable-event mutation denial, worker restart/reclaim and Account erasure. Six successful audit batches and eight signed authorizations—including negative tests—were retained without customer content in `/opt/spyglass-stage/evidence/0.3.0-rc.1/schedule-queue-rehearsal.json`, SHA-256 `68a3e20c5a445ce21c1bdb33b01178af0b3af209550c2ba345180f2c4b795ae3`. The synthetic Account has zero remaining rows, the temporary operator role was dropped and both Schedule workers are healthy.

Before production use, create the same execute-only role through the production secret/role workflow, run the one-shot image as a short-lived LKE Job and bind its authorization verifier to production operator key custody. Restore replay remains part of the complete production recovery game day rather than a reason to retain a standing queue credential.
