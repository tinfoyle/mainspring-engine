# Consequential action execution

Status: versioned executor/retry registry, definite-failure retry, unknown reconciliation, dual-controlled resolution, first Stripe adapter, content-free operational signals and authorized redacted HTTP/MCP/Your Turn recovery surfaces implemented

Attention owns the human approval aggregate and projects only an exact execute-only authorization. The runner action boundary consumes that projection; it never infers approval from an inbox state or accepts approval fields from a runner.

## Execution and recovery contract

- The cell registry binds a capability to an immutable executor version and retry-policy version. A new action requires the one enabled version. Existing actions retain their exact version for reconciliation even after a successor is enabled.
- The operation UUID is also the provider idempotency key. First admission uses `execute`; a lost response, explicit unknown outcome, or expired execution lease can use only the handler's side-effect-free `reconcile` method.
- A handler may classify an error as definite only when the provider proved that no effect occurred. Only stable codes in the frozen registry policy enter `retry_wait`, with bounded exponential delay and attempt count. Other definite failures are terminal. Transport, 5xx, malformed-response and uncertain provider outcomes reconcile rather than execute again.
- Automatic reconciliation that remains unknown may enter manual resolution. One eligible human requests the exact `succeeded` or `failed` outcome with a SHA-256 evidence digest; a different eligible human must confirm it. The database rejects self-confirmation, changed replay and direct worker-table access.

## First provider executor

`stripe.customer.create` validates a bounded email/name object, creates the Customer with the operation UUID in Stripe's `Idempotency-Key`, and binds Account, operation and capability metadata. Its reconciliation path performs only an exact Stripe Customer Search for the Account and operation metadata. Zero results remain unknown because Stripe search can be eventually consistent; multiple or mismatched results fail closed. Only an HTTP 429 and request-level 4xx response are treated as proven no-effect outcomes, and only the registered rate-limit code is retryable.

The Stripe key exists only in the runner-broker process. Runner Jobs receive no provider, database or customer credential. Docker stage attaches brokers to the explicit provider-egress network; Kubernetes environments must supply reviewed Stripe egress in their overlay while the reference remains default-deny.

## Operations

The broker exposes numeric-only `runner-action` status metrics for executing, reconciling, retry-wait, unknown, manual-resolution, failed, succeeded and oldest-overdue-retry state. Sustained overdue retry or unresolved uncertainty pages the platform owner and links to the dual-control recovery procedure. Account erasure counts manual-resolution records through their ledger cascade, and Account movement applies the ordinary namespace write fence.

Owner and Administrator recovery queries now expose only operation/approval/invocation identifiers, capability, frozen executor/policy versions, state, attempts, stable error codes and timestamps. The routed HTTP API, optional MCP adapter composition and private Your Turn queue all reuse one application service and never return approved payloads, provider responses or human evidence text. Manual-resolution requests return only the evidence SHA-256 digest; confirmation requires a different eligible User and is replay-safe even when a routed retry receives a fresh server timestamp. Direct table mutation and interactive SQL remain prohibited.

Applied stage execution and final product-journey certification remain release evidence. Production MCP exposure still depends on the separately tracked token and Account-to-cell routing gateway.
