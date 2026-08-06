# ADR-0008: Record and reconcile external side effects

Status: Proposed  
Date: 2026-08-06

## Context

Temporal Activities and provider calls can fail ambiguously. An email, invoice, ticket, or billing operation may succeed externally while the worker loses the response. Blind retries can duplicate customer-visible or financial effects.

## Decision

Create an action ledger for every external mutation. Each action has a stable idempotency key protected by a database uniqueness constraint.

```text
prepared
  -> awaiting_approval
  -> executing
  -> succeeded

executing
  -> failed
  -> unknown

unknown
  -> reconciled
  -> manual_review
```

The idempotency key is derived from immutable business intent, for example:

```text
tenant:{tenantID}:run:{runID}:action:{actionID}
```

When an external provider supports idempotency keys, Mainspring passes the same key on every retry. When a provider does not support safe idempotency or reconciliation, an `unknown` result is not retried automatically.

Temporal retry policies distinguish read-only operations, safely idempotent writes, and non-idempotent writes. Approval records bind the approver, proposed payload hash, action identifier, and expiration so the executed action cannot silently differ from what was approved.

Billing and webhook events are also deduplicated using provider event identifiers and processed transactionally.

## Consequences

- External mutations are traceable independently of conversational output.
- Duplicate prevention is enforced by storage rather than only application checks.
- Some ambiguous outcomes require reconciliation jobs or human review.
- Integration adapters must expose provider references and idempotency capabilities.
- Action payloads need canonical serialization or hashing for approval integrity.

## Alternatives considered

- **Retry every failed call:** rejected because timeouts and network loss do not prove that the provider rejected the operation.
- **Never retry writes:** rejected because providers with idempotency support can safely recover from transient failures.
- **Keep action state only in Temporal:** rejected because customer-visible audit, reconciliation, and uniqueness constraints belong in the application database.

## Revisit when

- A new integration has unusual transaction or reconciliation semantics.
- Mainspring begins executing payments or regulated accounting operations.
- Action volume requires partitioning or archival policies.

