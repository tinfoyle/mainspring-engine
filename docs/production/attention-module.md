# Attention module

- Status: typed aggregate kernel, forced-RLS cell foundation and classified PostgreSQL repositories implemented; application services, transports and product surface pending
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

Migration `cell/000028_attention_foundation.sql` creates separate aggregate tables plus one polymorphic redacted event table with exact aggregate/event foreign-key shapes. Every table has forced RLS, an Account-local primary key, queue/detail indexes and the Account movement write fence. Events reject direct update/delete; aggregate-parent cascades remain available for governed Account lifecycle. Cascade deletion records exact per-table tombstone counts, so Work/Agent deletion order cannot silently omit Attention from erasure evidence. The movement copier discovers the new deterministic tables and their dependency order through the existing schema contract.

The PostgreSQL repository boundary now restores every loaded row through the typed kernel, classifies not-found/conflict/constraint/corruption outcomes, makes identical creates idempotent, applies expected-version writes and appends content-safe events in the same Account-scoped transaction. Queue reads use bounded stable `(updated_at,id)` keyset cursors and typed state/object filters. Disposable-PostgreSQL tests exercise create/replay/get/update/list for all three aggregates, stale writes, cross-Account reads, restored decision records, approval authorization and the absence of questions, fact identifiers, canonical payload content and human decision reasons from event data.

## Remaining delivery order

1. Implement package-authorized application command/query services, including exact eligible-request completion and exact parent-resumption planning.
2. Project approved/canceled `ConsequentialApproval` state into the existing execute-only runner authorization functions in the same durable command boundary.
3. Publish redacted HTTP and generated OpenAPI contracts, then add MCP parity over the same services.
4. Build the private Your Turn queue/detail/decision surface with draft, concurrency, keyboard, live-announcement and session-recovery behavior.
5. Prove concurrent decisions, proposal invalidation, expiry and action-ledger integration in disposable PostgreSQL; repository replay/conflict/isolation/redaction, forced-RLS isolation, cross-Account foreign keys, movement fencing, immutable events and exact erasure accounting are already covered.

The typed kernel, schema, repositories and their authorization/isolation/lifecycle tests are complete. No application-service, projection, transport or UI completion is implied by that checkpoint.
