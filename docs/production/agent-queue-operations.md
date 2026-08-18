# Agent Queue Operations

Status: executable operator contract; connected-cell rehearsal and production role grants remain release gates

Agent dispatch and result projection are independent durable queues. A terminal row never retries forever and never disappears automatically. Operators can inspect one queue in one cell and, after correcting the underlying condition, requeue exactly one Account/invocation pair.

## Safety contract

- Run `spyglass agent-queue-admin inspect|requeue` as a short-lived controlled Job, never as a standing service.
- The credential receives `USAGE` on `public` and `EXECUTE` on only `spyglass_inspect_agent_queue_dead_letters` and `spyglass_requeue_agent_queue_dead_letter`. It receives no table grants.
- Every invocation requires an exact environment confirmation and a phishing-resistant signed authorization whose scope binds action, queue, inspection limit, Account, and invocation.
- Inspection is capped at 100 records and returns technical identifiers, attempt count, bounded error code, and timestamps only.
- Requeue accepts only a current `dead_letter`. It clears leases and failure metadata, resets attempts, and makes the row immediately claimable. A repeated or stale operator action fails without mutation.
- Inspection—including an empty result—and requeue write immutable evidence in the same transaction. Evidence is Account-attributed, included in exact erasure counts, and deletable only inside the reviewed erasure function.
- Neither command decrypts or logs prompts, messages, model output, runner envelopes, credentials, or ciphertext.

## Invocation

Set the common operator variables described in [Platform Operator Authorization](operator-authorization.md), plus:

```text
SPYGLASS_CELL_DATABASE_URL=<execute-only cell operator role>
SPYGLASS_AGENT_QUEUE=dispatch|projection
SPYGLASS_AGENT_QUEUE_INSPECT_LIMIT=50
```

Inspect first, diagnose the bounded `last_error_code`, correct the dependency or data condition, then obtain a new exact-scope authorization for requeue:

```text
SPYGLASS_AGENT_ACCOUNT_ID=<uuid>
SPYGLASS_AGENT_INVOCATION_ID=<uuid>
spyglass agent-queue-admin requeue
```

Do not requeue deterministic validation failures until their cause is understood. Projection dead letters retain their encrypted terminal envelope; choose controlled recovery or Account erasure before the retention policy can be resolved. After requeue, confirm the relevant dead-letter gauge decreases, the ready gauge advances or the job completes, and the invocation reaches its expected durable state.

## Required production evidence

Before granting the role in production, exercise both queues against a staging cell: empty and populated inspection, exact requeue, wrong queue, wrong Account/invocation, non-dead-letter conflict, duplicate authorization, immutable-event mutation, worker crash after requeue, Account erasure, and restore replay. Archive the authorization ID, audit batch ID, bounded state transition, and monitoring evidence without customer content.
