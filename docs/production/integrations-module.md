# Integrations module

- Status: provider-neutral kernel, governed persistence, local worker execution and routed read observability constructed
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
5. Dedicated connector worker, local mock adapters, bounded retry and reconciliation-only unknown handling. **Worker kernel/bootstrap, current authority, payload assembler, one-operation broker boundary, restore-gated executable and ubunturojo Docker certification constructed; production object/secret/provider adapters remain.**
6. Generated HTTP/MCP and private browser surfaces for connector setup, scope display, health, revocation and manual resolution. **Generated HTTP and MCP reads/lifecycle mutations constructed; private browser and manual-resolution commands remain.**
7. Email inbound/threading and Google Drive sync lifecycle, hardened web research, retention, Stage recovery/erasure and production role grants.

## Kernel checkpoint

The typed kernel now owns manager-governed connections with immutable scope revisions, monotonically bound credential generations and immutable content-free health observations. The first executable scope shapes are deliberately narrow: email freezes one sender identity plus audience reference, and web publication freezes one HTTPS origin plus normalized path prefix. Capabilities are a closed connector-compatible set. Drive and research kinds are named for the launch architecture but cannot be configured until their reviewed scope shapes are implemented.

Credential bindings contain only a provider code, generation and SHA-256 attestation for an opaque secret-broker reference. Rotation ends the prior generation and creates exactly the next generation; revocation is monotonic. Activating a connection requires an available same-Account credential, and rebinding requires the next generation. Existing connection revisions and credential bindings remain unchanged when later authority is installed.

A Marketing execution freezes the exact release version, Attention approval, connection revision, credential generation, capability and canonical payload digest. Execute attempts may retry only after a reconciliation attempt proves the provider did not apply the effect. Ambiguous outcomes and lost leases become `unknown`, which admits reconciliation only; three total ambiguous attempts enter manual resolution. Successful, failed and cancelled states are terminal. Pure tests cover role and capability denial, scope normalization, revision immutability, credential rotation/revocation, exact execution binding, safe retry, lost leases and bounded uncertainty.

## Persistence checkpoint

Cell migration `000062_integrations_foundation.sql` adds Account-owned connection, immutable revision, credential binding, health observation, execution, immutable attempt and content-redacted event records under forced RLS, plus a private content-free execution queue. Deferred binding guards permit atomic connection setup and rotation without weakening monotonic revision or credential-generation rules. Historical execution authority is frozen through composite foreign keys to the exact connector revision and credential generation.

The execute-only claim boundary leases one due record with `SKIP LOCKED`, then rechecks the current Account placement, active Marketing campaign and exact approved release, release channel, unexpired `marketing.release.activate` Attention decision, active connector revision and active credential generation before returning any provider work. A definite no-effect reconciliation may schedule a bounded execute retry. Unknown results and expired leases reconcile only; an expired third lease enters manual resolution directly. Terminal and manual states remove their queue record.

All seven durable Account record families participate in the namespace write fence, and the queue remains private rather than receiving a customer RLS surface. The Account-movement preflight blocks unfinished external effects, movement copies every Integration table in dependency order, and whole-Account erasure reports exact counts for all eight tables. Fresh PostgreSQL 17 tests prove authority binding, immutable revision history, RLS isolation, credential revocation safety, execute/reconcile ordering, no early retry, third-lease manual resolution, movement and exact idempotent erasure.

Cell migration `000063_integration_execution_health_gate.sql` closes the remaining atomic claim gap. Immediately after all durable release and connector authority checks, each execute or reconcile claim selects the latest observation at or before the claim time for the exact frozen connection revision, credential identity and generation. A healthy or degraded observation no more than five minutes old admits work. A missing, stale or unavailable observation advances only the private queue by 30 seconds, returns no provider work and consumes no attempt; revocation and stale Marketing authority retain their existing fail-closed terminal behavior. Applied coverage proves stale and unavailable deferral, unchanged execution state and attempt count, then admission by a fresh degraded observation.

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

The local mock connector is deterministic, thread-safe and performs no network I/O. Per-execution scripts inject success, definite failure, ambiguous outcome and reconciliation absence while recording only execution/attempt identities and mode. Tests prove execute success, reconciliation-only retry, authority/manifest fail-closed behavior and malformed post-call uncertainty. The PostgreSQL repository mapping is exercised by the applied execution test, and the claim itself now atomically enforces exact connector health.

The concrete current-authority adapter reads one global workload snapshot per claim rather than independently reading each package. It requires an active Account assigned to the worker's exact cell, a positive placement generation, matching Account/snapshot entitlement versions and exactly one enabled Marketing plus Integrations package. Read-only, suspended, absent, duplicate or malformed package authority and cross-cell drift fail before payload access. No Membership role or browser/Agent identity enters this workload boundary. The manifest, payload, one-operation broker, worker bootstrap and local Docker path are now constructed; only the deterministic local adapter is enabled.

The canonical content-free delivery manifest is now a single shared byte-level builder used by preparation and runtime reconstruction. It validates every frozen release, connector revision and credential identity, requires one-to-100 unique immutable asset revision digests, sorts assets by revision identity and emits fixed-order JSON before SHA-256. A golden-byte test prevents field-order or encoding drift, and applied preparation now uses the same builder with a genuine content-bound Marketing asset fixture. The builder alone does not expose creative content.

## Delivery payload checkpoint

The runtime payload assembler now loads one repeatable-read Account-RLS snapshot of the exact approved release, approval, frozen connector revision and immutable asset revisions. It restores each typed revision, sorts assets, opens only its opaque content reference, bounds the aggregate and each read, requires the exact declared byte count and SHA-256, then rebuilds the shared manifest and compares it with the claimed digest. Any release, Account, scope, content or digest drift fails before connector invocation.

The provider envelope is versioned and bounded to 16 MiB. It contains the execution idempotency identity, closed capability, immutable non-secret connector scope and verified creative bytes; it excludes storage references, credentials and operational database fields. The local content reader copies a fixed object set and performs no network I/O, so local ambiguity/reconciliation certification cannot accidentally reach customer infrastructure. Applied PostgreSQL coverage proves exact snapshot restoration and stale release-version rejection. A production immutable-object reader and governed Marketing upload/reference lifecycle remain required before real provider execution.

## One-operation credential checkpoint

The worker now requests a credential lease only after current global authority and exact payload integrity succeed. The request binds Account, execution, attempt, mode, capability, connection, credential identity/generation and the database lease expiry. The broker returns at most 64 KiB of runtime material for that operation; the service copies it into the single connector call, zeroes the connector-visible slice immediately after return and closes the broker lease on every normal or panic path. A release failure is surfaced operationally without rewriting a valid provider outcome.

The local broker copies configured mock material, rejects unconfigured generations and records only execution/attempt/credential identities. Tests prove broker denial cannot reach a connector, the lease closes, the connector's retained view is zeroed and no material enters call history. Production still needs the environment-backed secret adapter and workload grant; neither the generic app nor Agent runtime receives the broker interface.

## Worker bootstrap checkpoint

The per-cell connector bootstrap now opens separate bounded global and cell pools, composes current global authority with the execute-only cell repository, exact payload assembler, injected broker/content boundaries and closed connector definitions, and owns polling, readiness, content-free processed/failure counters and shutdown. It has no browser, app API, tool router, Agent runner or generic outbound client fallback. Tests cover fail-fast composition, bounded idle polling, cancellation and content-free outcome accounting.

The reusable bootstrap deliberately does not select mock versus production adapters. The executable now selects a strict JSON-configured mock only when the reviewed environment is `local` or `local-secure`, opens both restore gates, and runs under the standard worker health lifecycle. Its opt-in Docker profile creates one worker per cell with separate non-owner global/cell credentials and internal-only networks. The ubunturojo certification seeds one isolated approved release and proves exact payload/credential execution settles `execute:succeeded`, while forbidden global User and direct cell credential-metadata reads fail.

This opens only the deterministic local certification path. Stage and production remain closed until governed immutable-object upload/read, environment secret brokerage and real closed-capability provider adapters are constructed and certified.

## Routed HTTP observability checkpoint

Five generated session-authenticated operations now route through the signed Account boundary and the canonical Integrations service: stable connection list, current connection/scope/latest-health detail, stable content-free health history, filtered external-execution list and exact execution/attempt detail. Connection detail carries a weak version ETag. All three cursor families are opaque, versioned and kind-bound, so a health cursor cannot be replayed against a connection or execution collection.

Execution payload digests are rendered as canonical lowercase SHA-256 hex. The responses contain immutable connector and credential identities/generations, non-secret scope, bounded machine error codes and timing evidence, but never broker references, attestation digests, provider credentials, creative bodies, provider payloads or provider responses. OpenAPI and generated Go/TypeScript route inventories covered 166 customer operations at this checkpoint. Contract tests prove response shape, Account concealment, query binding, cursor-family rejection and absence of secret/provider fields.

## Routed HTTP lifecycle checkpoint

Eight additional generated commands now expose the complete existing human-manager application boundary: connection creation and immutable scope revision; opaque credential-attestation activation and monotonic rotation; disable, enable and irreversible revocation; and preparation of one exact Marketing delivery execution. Every mutation binds the signed route operation to one UUID `Idempotency-Key`; versioned connection changes require a weak `If-Match`. Strict JSON rejects unknown and trailing fields, credential input accepts only a provider code plus lowercase SHA-256 attestation (never a broker reference or secret), and delivery preparation repeats both Marketing and Integrations authorization inside the application service.

Connection creation and execution preparation return canonical locations; every connection result returns its new version ETag. The generated customer contract now contains 174 typed operations. Transport tests cover all eight commands, exact operation/version mapping, digest decoding, response contracts, missing preconditions and mismatched idempotency authority. HTTP construction for the current email-send/web-publish lifecycle is complete; MCP, the private workspace, dual-controlled external-execution manual resolution, production adapters and later inbound/sync/research lifecycles remain.

## MCP lifecycle checkpoint

Thirteen typed MCP tools now expose the same five reads and eight governed commands as HTTP over the shared Integrations service. The global gateway registry classifies every tool as an Integrations read or mutation before cell routing, and the cell handler repeats exact Account/package authorization. Mutation tools require an operation UUID and optimistic version where applicable; credential tools decode only a lowercase SHA-256 broker-reference attestation. Execution output normalizes the frozen payload digest to hex and uses kind-bound opaque cursors identical in meaning to HTTP.

Tool schemas, annotations and classifications are complete and deterministic. Protocol tests prove all thirteen tools are registered, read-versus-mutation authority is exact, operation/digest/query mapping reaches the canonical service, output contains no broker reference or provider material, missing versions fail safely and repository details are redacted. The shared production MCP gateway composes this service automatically. The private browser workspace, execution manual-resolution commands and applied external-client journey remain before construction step 6 is complete.

## Invariants

- Provider credentials never enter Marketing, Agent, browser, MCP output, events, logs or telemetry.
- A connector revision and credential generation are immutable once referenced by an execution.
- A workload cannot create, revise, rotate or revoke a connection or credential.
- An unknown external outcome is reconciled, never automatically executed again.
- Revocation prevents new effects without rewriting prior delivery evidence.
- Connector failure degrades only the dependent capability and never changes Marketing aggregate state.
