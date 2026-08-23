# Consequential action execution

Status: durable post-approval execution worker, versioned executor/retry registry, definite-failure retry, bounded unknown reconciliation, dual-controlled resolution, Stripe, Finance and Marketing adapters, content-free operational signals and authorized redacted HTTP/MCP/Your Turn recovery surfaces implemented

Attention owns the human approval aggregate and projects only an exact execute-only authorization. A broker-owned worker consumes that projection after the proposing runner has terminated; it never infers approval from an inbox state, accepts approval fields from a runner or depends on an expired runner identity.

## Execution and recovery contract

- The cell registry binds a capability to an immutable executor version and retry-policy version. A new action requires the one enabled version. Existing actions retain their exact version for reconciliation even after a successor is enabled.
- Approval inserts a content-free Account/operation queue item. The security-definer claim locks one due item across Accounts, sets the Account RLS scope, verifies the immutable Attention/authorization digest binding and returns the frozen payload to the broker. The broker independently hashes the returned bytes before execution.
- A durable proposal's approval window is independent of the runner credential lifetime. Authorization still proves that the originating invocation exchange exists and binds its exact identifiers/digests, but the worker never reuses that exchange or its expired pod identity.
- The operation UUID is also the provider idempotency key. First admission uses `execute`; a lost response, explicit unknown outcome, or expired execution lease can use only the handler's side-effect-free `reconcile` method.
- A handler may classify an error as definite only when the provider proved that no effect occurred. Only stable codes in the frozen registry policy enter `retry_wait`, with bounded exponential delay and attempt count. Other definite failures are terminal. Transport, 5xx, malformed-response and uncertain provider outcomes reconcile rather than execute again.
- Automatic reconciliation is delayed and bounded to three total attempts. An action that remains unknown then leaves the execution queue and can only enter manual resolution. One eligible human requests the exact `succeeded` or `failed` outcome with a SHA-256 evidence digest; a different eligible human must confirm it. The database rejects self-confirmation, changed replay and direct worker-table access.

## First provider executor

`stripe.customer.create` validates a bounded email/name object, creates the Customer with the operation UUID in Stripe's `Idempotency-Key`, and binds Account, operation and capability metadata. Its reconciliation path performs only an exact Stripe Customer Search for the Account and operation metadata. Zero results remain unknown because Stripe search can be eventually consistent; multiple or mismatched results fail closed. Only an HTTP 429 and request-level 4xx response are treated as proven no-effect outcomes, and only the registered rate-limit code is retryable.

The Stripe key exists only in the runner-broker process. Runner Jobs receive no provider, database or customer credential. Docker stage attaches brokers to the explicit provider-egress network; Kubernetes environments must supply reviewed Stripe egress in their overlay while the reference remains default-deny.

## First internal executor

`finance.entry.post` accepts only an entry UUID and expected draft version. It is available to a Persona as a proposed-action kind, not as a directly callable runner tool. After approval, the worker posts through the canonical Finance repository, records the approving User as the posting actor and uses the operation UUID as the Finance event UUID. Its reconciliation path performs only an exact lookup for that event, entry and version transition. Because the Finance transaction commits the state transition and event atomically, an absent event proves that the effect did not occur; a database read failure remains unknown.

## Marketing activation executor

`marketing.release.activate` accepts only an exact campaign identity/version and submitted release identity/version. It is a Persona proposed-action kind, never a directly callable runner tool. After an Owner or Administrator approves the Attention item, the worker derives two deterministic event identities from the approval operation, resolves the approval identity from the immutable authorization projection and atomically binds it to the release and activates the matching campaign. The database independently requires the same Account, campaign, frozen version and channel set, a still-approved/unexpired Attention decision and the approving User as actor.

Approval and activation commit in one serializable Account transaction, so the worker can never leave an approved-but-unactivated partial effect. Reconciliation is side-effect-free and succeeds only when both exact immutable Marketing events exist with the proposed before/after versions. An absent pair proves no effect; a database read failure remains unknown. The browser no longer accepts pasted Approval UUIDs: submitted releases direct the User to Your Turn, and the durable worker applies the approved proposal.

## Operations

The broker exposes numeric-only `runner-action` status metrics for executing, reconciling, retry-wait, unknown, manual-resolution, failed, succeeded and oldest-overdue-retry state. Sustained overdue retry or unresolved uncertainty pages the platform owner and links to the dual-control recovery procedure. Account erasure counts manual-resolution records through their ledger cascade, and Account movement applies the ordinary namespace write fence.

Owner and Administrator recovery queries now expose only operation/approval/invocation identifiers, capability, frozen executor/policy versions, state, attempts, stable error codes and timestamps. The routed HTTP API, optional MCP adapter composition and private Your Turn queue all reuse one application service and never return approved payloads, provider responses or human evidence text. Manual-resolution requests return only the evidence SHA-256 digest; confirmation requires a different eligible User and is replay-safe even when a routed retry receives a fresh server timestamp. Direct table mutation and interactive SQL remain prohibited.

Applied stage execution and final product-journey certification remain release evidence. Production MCP exposure still depends on the separately tracked token and Account-to-cell routing gateway.
