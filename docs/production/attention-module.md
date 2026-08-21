# Attention module

- Status: typed aggregate kernel implemented; persistence, application services, transports and product surface pending
- Phase: 3.1
- Owns: human information requests, Work review decisions and consequential approvals
- Does not own: Work lifecycle persistence, Knowledge facts, Agent invocation or provider execution

## Boundary

Attention is not a generic inbox row. It owns three Account-scoped aggregates because each has a different authority and completion rule:

1. `InformationRequest` binds one question to an exact fact key and scope plus one exact parent Work item. A fact reference can answer it only when the key, scope kind and scope identifier all match. Completion records the immutable fact identifier/version and responder; it does not infer that any other request or parent is unblocked.
2. `WorkReview` binds one assigned human reviewer to an exact Work identifier/version and proposal SHA-256. A decision is `approved` or `changes_requested`. Any later Work version or proposal-digest mismatch invalidates the decision while retaining it for audit.
3. `ConsequentialApproval` binds an operation and Agent invocation to canonical payload bytes, SHA-256/hash version, evidence digest, capability, proposer, policy version and bounded expiry. A payload, evidence or policy change invalidates even an approved aggregate. Only a currently approved, unexpired aggregate can emit the exact immutable projection consumed by the runner action ledger.

All aggregate constructors and persistence restore boundaries reject malformed identifiers, timestamps, states and cross-state field combinations. Every competing command carries an expected version. State-changing reasons are trimmed and bounded to 3-1000 characters.

## Information requirements

The initial scope vocabulary is intentionally small:

- `account`: shared across the Account and has no scope identifier;
- `work_item`: bound to one Work identifier;
- `conversation`: bound to one Conversation identifier.

The fact key is a bounded machine code. Scope matching is exact; an Account fact does not silently satisfy a Work-scoped request, and a fact for one Work item or Conversation cannot answer another. Application orchestration may complete multiple requests from one shared fact only by loading open candidates, applying this eligibility rule to each request and resuming exactly the parents that have no remaining blockers.

## Authorization matrix

| Command | Owner | Administrator | Member | Viewer / Billing administrator | Object rule |
|---|---:|---:|---:|---:|---|
| Answer information | yes | yes | yes | no | fact requirement must match exactly |
| Cancel information | yes | yes | requester only | no | request must still be open |
| Decide Work review | assigned reviewer | assigned reviewer | assigned reviewer | no | actor must be the assigned User |
| Cancel Work review | yes | yes | requester only | no | review must still be open |
| Approve/reject consequence | yes | yes | no | no | human User only; optional policy requires proposer separation |
| Cancel pending consequence | yes | yes | proposer only | no | proposal must still be open and unexpired |

Application services must first repeat Account/Membership/package authorization. The domain matrix is the narrower object-level decision and cannot broaden a package denial.

## Canonical payload version 1

Consequential input is a JSON object no larger than 256 KiB. Version 1 rejects empty/non-object documents, trailing content, duplicate keys at any depth and nesting beyond 64 levels. It removes insignificant whitespace, sorts object keys through deterministic JSON encoding, preserves array order and hashes the resulting bytes with SHA-256. JSON number lexemes remain part of the version-1 byte contract; producers must construct monetary and other precision-sensitive values with reviewed typed encoders rather than relying on binary floating-point conversion.

Approval lifetime is positive and at most 24 hours, matching the existing runner authorization projection. Authority cannot be projected before the recorded decision time or at/after expiry. The projected fields map one-to-one to `runner_action_authorizations`; the canonical customer payload itself is not copied into the execution ledger.

## Redaction and events

The aggregate contains customer-visible questions and canonical action input, but future mutation events and ordinary queue DTOs must remain content-safe:

- events carry aggregate/event identifiers, typed state changes, actor kind, reason classification, correlation identifier, versions and timestamps;
- list DTOs carry bounded presentation fields and never canonical payload bytes;
- detail DTOs expose proposal content only to an authorized decision use case;
- logs, metrics, traces and action-ledger views carry digests and stable error codes, not questions, answers, email bodies, recipients or evidence content.

## Remaining delivery order

1. Add forced-RLS cell tables, composite Account/Work/Conversation/Run references, optimistic updates and immutable redacted events for all three aggregates.
2. Implement package-authorized application command/query services, including exact eligible-request completion and exact parent-resumption planning.
3. Project approved/canceled `ConsequentialApproval` state into the existing execute-only runner authorization functions in the same durable command boundary.
4. Publish redacted HTTP and generated OpenAPI contracts, then add MCP parity over the same services.
5. Build the private Your Turn queue/detail/decision surface with draft, concurrency, keyboard, live-announcement and session-recovery behavior.
6. Prove RLS isolation, concurrent decisions, proposal invalidation, expiry, Account movement/erasure and action-ledger integration in disposable PostgreSQL.

The typed kernel and table-driven authorization/invalidation tests are complete. None of the remaining persistence, projection, transport or UI work is implied by that checkpoint.
