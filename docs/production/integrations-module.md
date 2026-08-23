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
3. Classified application services for connection lifecycle, credential rotation/revocation, health and bounded execution observability. **Constructed.**
4. Marketing activation-to-delivery preparation with exact release/approval/connector binding and current-package reauthorization. **Constructed.**
5. Dedicated connector worker, local mock adapters, bounded retry and reconciliation-only unknown handling. **Worker kernel and local mock constructed; authority, payload, broker and bootstrap adapters remain.**
6. Generated HTTP/MCP and private browser surfaces for connector setup, scope display, health, revocation and manual resolution.
7. Email inbound/threading and Google Drive sync lifecycle, hardened web research, retention, Stage recovery/erasure and production role grants.

## Kernel checkpoint

The typed kernel now owns manager-governed connections with immutable scope revisions, monotonically bound credential generations and immutable content-free health observations. The first executable scope shapes are deliberately narrow: email freezes one sender identity plus audience reference, and web publication freezes one HTTPS origin plus normalized path prefix. Capabilities are a closed connector-compatible set. Drive and research kinds are named for the launch architecture but cannot be configured until their reviewed scope shapes are implemented.

Credential bindings contain only a provider code, generation and SHA-256 attestation for an opaque secret-broker reference. Rotation ends the prior generation and creates exactly the next generation; revocation is monotonic. Activating a connection requires an available same-Account credential, and rebinding requires the next generation. Existing connection revisions and credential bindings remain unchanged when later authority is installed.

A Marketing execution freezes the exact release version, Attention approval, connection revision, credential generation, capability and canonical payload digest. Execute attempts may retry only after a reconciliation attempt proves the provider did not apply the effect. Ambiguous outcomes and lost leases become `unknown`, which admits reconciliation only; three total ambiguous attempts enter manual resolution. Successful, failed and cancelled states are terminal. Pure tests cover role and capability denial, scope normalization, revision immutability, credential rotation/revocation, exact execution binding, safe retry, lost leases and bounded uncertainty.

## Persistence checkpoint

Cell migration `000062_integrations_foundation.sql` adds Account-owned connection, immutable revision, credential binding, health observation, execution, immutable attempt and content-redacted event records under forced RLS, plus a private content-free execution queue. Deferred binding guards permit atomic connection setup and rotation without weakening monotonic revision or credential-generation rules. Historical execution authority is frozen through composite foreign keys to the exact connector revision and credential generation.

The execute-only claim boundary leases one due record with `SKIP LOCKED`, then rechecks the current Account placement, active Marketing campaign and exact approved release, release channel, unexpired `marketing.release.activate` Attention decision, active connector revision and active credential generation before returning any provider work. A definite no-effect reconciliation may schedule a bounded execute retry. Unknown results and expired leases reconcile only; an expired third lease enters manual resolution directly. Terminal and manual states remove their queue record.

All seven durable Account record families participate in the namespace write fence, and the queue remains private rather than receiving a customer RLS surface. The Account-movement preflight blocks unfinished external effects, movement copies every Integration table in dependency order, and whole-Account erasure reports exact counts for all eight tables. Fresh PostgreSQL 17 tests prove authority binding, immutable revision history, RLS isolation, credential revocation safety, execute/reconcile ordering, no early retry, third-lease manual resolution, movement and exact idempotent erasure.

## Connection application/repository checkpoint

The classified Integrations service now authorizes every read and manager mutation against the current Integrations package. Reads admit current Members; creation, scope revision, credential activation/rotation, disable/enable and revocation require an Owner or Administrator acting as a User. A workload cannot enter this management boundary. Connection pages have application-owned limits, stable `(updated_at DESC,id)` cursors and closed launch filters for email and web publication.

Credential commands contain a provider code and the SHA-256 attestation of an opaque broker reference only. They contain neither the broker reference nor provider material. Initial binding, monotonic rotation and connection revocation are atomic Account transactions; revocation ends the currently bound credential without rewriting prior bindings. Setup, revision and credential child identities derive deterministically from the route request identity, while the repository treats an exact retry as replay and rejects altered input.

The PostgreSQL adapter restores every connection, current immutable revision and credential through the typed kernel under Account RLS. It serializes lifecycle writes, appends only content-redacted events, preserves deferred binding invariants and keeps cross-Account records concealed. Fresh PostgreSQL 17 coverage proves exact replay, altered-replay conflict, normalized scope restoration, credential rotation, disable/enable/revoke, credential ending, stable pagination and cross-Account isolation.

## Observability checkpoint

Connection detail now combines the current connection, its exact immutable non-secret scope revision and latest content-free health observation. Separate bounded health history pages use stable `(checked_at DESC,id)` cursors. External execution pages filter only the closed delivery capabilities and state vocabulary; exact detail restores the frozen authority digest and immutable ordered attempts through the typed kernel. Provider responses, provider payloads, secret references and credentials remain absent.

All observability queries repeat Integrations package authorization and preserve cross-Account concealment. Malformed persisted health or attempt history fails restoration rather than escaping as a customer DTO. Fresh PostgreSQL 17 tests cover latest-health selection, health pagination, manual-resolution execution detail, ordered attempt outcomes, filtered execution listing and cross-Account denial. Construction step 3 is complete.

## Marketing preparation checkpoint

An Owner or Administrator may now prepare one delivery per active approved Marketing release capability by selecting an active Account connection. This separate Integrations mutation requires matching current Account placement and entitlement versions from enabled Marketing and Integrations package checks; read-only or suspended access cannot prepare an effect. It does not let an Agent, provider worker or Marketing aggregate choose a connector. The database independently rechecks the active campaign/release, exact release version and unexpired Attention approval, channel-compatible current connection revision and current credential generation.

Preparation freezes those authorities into one immutable execution and computes its SHA-256 idempotency digest from a canonical content-free manifest: release/version, delivery capability, connector revision, credential generation and the sorted immutable asset revision digests. The manifest itself is not stored in operational rows, and no creative body, audience value, broker reference or provider credential enters the execution or event. Exact retries return the original execution even after later connector changes; altered request reuse conflicts.

Fresh PostgreSQL 17 coverage now prepares the execution through the repository rather than seeding it directly, proves queue insertion, exact replay, altered-replay rejection and the full execute/reconcile/manual-resolution sequence. Construction step 4 is complete. The worker authority, payload, broker and bootstrap boundaries remain; no provider effect is executable yet.

## Connector worker kernel checkpoint

The dedicated connector-execution application now claims one leased immutable execution through the execute-only PostgreSQL functions, validates every frozen identity/version/digest and requires a current-authority check before loading any customer payload. A payload source must reproduce the exact canonical manifest digest and supplies provider material only in connector-runtime memory. Capability definitions are closed to email send and web publish with bounded timeouts; the generic app and Agent runners are not dependencies.

Execute and reconcile are separate connector methods. An execute adapter cannot report `not_applied`; only reconciliation may do so, with a bounded future retry instant. Invalid adapter output after a possible provider call is settled as unknown. Authority, manifest or adapter absence before any provider call is settled as a definite no-effect failure. Completion uses a context independent from the connector call so cancellation still attempts to record the outcome; failure to settle leaves the lease to expire into the existing unknown/reconcile path.

The local mock connector is deterministic, thread-safe and performs no network I/O. Per-execution scripts inject success, definite failure, ambiguous outcome and reconciliation absence while recording only execution/attempt identities and mode. Tests prove execute success, reconciliation-only retry, authority/manifest fail-closed behavior and malformed post-call uncertainty. The PostgreSQL repository mapping is exercised by the applied execution test. Step 5 remains open for the concrete dual-package/placement/health authority client, manifest/payload reconstruction, one-operation secret-broker adapter, worker bootstrap and local Docker wiring.

## Invariants

- Provider credentials never enter Marketing, Agent, browser, MCP output, events, logs or telemetry.
- A connector revision and credential generation are immutable once referenced by an execution.
- A workload cannot create, revise, rotate or revoke a connection or credential.
- An unknown external outcome is reconciled, never automatically executed again.
- Revocation prevents new effects without rewriting prior delivery evidence.
- Connector failure degrades only the dependent capability and never changes Marketing aggregate state.
