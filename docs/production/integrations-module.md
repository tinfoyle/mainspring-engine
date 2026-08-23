# Integrations module

- Status: scope, provider-neutral typed kernel and governed forced-RLS execution persistence constructed
- Package boundary: Integrations
- Decision: [ADR-0008](decisions/0008-integration-connector-execution.md)

## Launch contract

Integrations owns Account-scoped connector identity, immutable non-secret scopes, credential-binding lifecycle, connector health and external execution evidence. It does not own Marketing campaign intent, Attention decisions, Knowledge facts, provider credentials, provider payload archives or arbitrary Agent egress.

The first Marketing delivery capabilities are `email.send` and `web.publish`. Email scope freezes a sender identity and one audience reference; web publication freezes one HTTPS origin and normalized path prefix. These are connector authorizations, not creative content. A prepared delivery binds one exact Marketing release and approval to one immutable connection revision and credential generation.

## Construction sequence

1. Provider-neutral connection, scope, credential-binding and external-execution state machines. **Constructed.**
2. Account-owned forced-RLS persistence, immutable revisions/attempts/events, claim leasing, movement fencing and exact erasure/restore participation. **Constructed.**
3. Classified application services for connection lifecycle, credential rotation/revocation, health and bounded execution observability.
4. Marketing activation-to-delivery preparation with exact release/approval/connector binding and current-package reauthorization.
5. Dedicated connector worker, local mock adapters, bounded retry and reconciliation-only unknown handling.
6. Generated HTTP/MCP and private browser surfaces for connector setup, scope display, health, revocation and manual resolution.
7. Email inbound/threading and Google Drive sync lifecycle, hardened web research, retention, Stage recovery/erasure and production role grants.

## Kernel checkpoint

The typed kernel now owns manager-governed connections with immutable scope revisions, monotonically bound credential generations and immutable content-free health observations. The first executable scope shapes are deliberately narrow: email freezes one sender identity plus audience reference, and web publication freezes one HTTPS origin plus normalized path prefix. Capabilities are a closed connector-compatible set. Drive and research kinds are named for the launch architecture but cannot be configured until their reviewed scope shapes are implemented.

Credential bindings contain only a provider code, generation and SHA-256 attestation for an opaque secret-broker reference. Rotation ends the prior generation and creates exactly the next generation; revocation is monotonic. Activating a connection requires an available same-Account credential, and rebinding requires the next generation. Existing connection revisions and credential bindings remain unchanged when later authority is installed.

A Marketing execution freezes the exact release version, Attention approval, connection revision, credential generation, capability and canonical payload digest. Execute attempts may retry only after a reconciliation attempt proves the provider did not apply the effect. Ambiguous outcomes and lost leases become `unknown`, which admits reconciliation only; three total ambiguous attempts enter manual resolution. Successful, failed and cancelled states are terminal. Pure tests cover role and capability denial, scope normalization, revision immutability, credential rotation/revocation, exact execution binding, safe retry, lost leases and bounded uncertainty.

## Persistence checkpoint

Cell migration `000062_integrations_foundation.sql` adds Account-owned connection, immutable revision, credential binding, health observation, execution, immutable attempt and content-redacted event records under forced RLS, plus a private content-free execution queue. Deferred binding guards permit atomic connection setup and rotation without weakening monotonic revision or credential-generation rules. Historical execution authority is frozen through composite foreign keys to the exact connector revision and credential generation.

The execute-only claim boundary leases one due record with `SKIP LOCKED`, then rechecks the current Account placement, active Marketing campaign and exact approved release, release channel, unexpired `marketing.release.activate` Attention decision, active connector revision and active credential generation before returning any provider work. A definite no-effect reconciliation may schedule a bounded execute retry. Unknown results and expired leases reconcile only; an expired third lease enters manual resolution directly. Terminal and manual states remove their queue record.

All seven durable Account record families participate in the namespace write fence, and the queue remains private rather than receiving a customer RLS surface. The Account-movement preflight blocks unfinished external effects, movement copies every Integration table in dependency order, and whole-Account erasure reports exact counts for all eight tables. Fresh PostgreSQL 17 tests prove authority binding, immutable revision history, RLS isolation, credential revocation safety, execute/reconcile ordering, no early retry, third-lease manual resolution, movement and exact idempotent erasure. Application repositories, credential-broker integration, worker/adapters and customer surfaces remain closed.

## Invariants

- Provider credentials never enter Marketing, Agent, browser, MCP output, events, logs or telemetry.
- A connector revision and credential generation are immutable once referenced by an execution.
- A workload cannot create, revise, rotate or revoke a connection or credential.
- An unknown external outcome is reconciled, never automatically executed again.
- Revocation prevents new effects without rewriting prior delivery evidence.
- Connector failure degrades only the dependent capability and never changes Marketing aggregate state.
