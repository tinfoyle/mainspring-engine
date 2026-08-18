# Phase 2 Development Slice

This repository now contains the first executable production slice for Infinite Ocean: Spyglass.

## Included

- `website/`: the public Infinite Ocean site with Product, Packages, Pricing, signup entry, company, security, privacy, and terms routes.
- `cmd/spyglass`: development, persistent global/cell API, worker, migration, and operator process modes.
- Identity domain: pending User registration and verified activation.
- Accounts domain: free Account creation and owner Membership.
- Catalog domain: versioned Feature Packages, Free/Team/Operating Plans, and a public offer projection that omits Stripe references.
- Entitlements domain: grants, deterministic priority evaluation, immutable snapshots, and read versus mutation checks.
- Placement domain: capacity-aware Account assignment to a shared cell.
- Registration application flow: verification challenge followed by atomic User/Account/Membership/placement/free-entitlement provisioning.
- PostgreSQL registration, published Catalog, session, access-state, and signed billing-inbox adapters.
- Production account-api composition that requires PostgreSQL, real verification delivery, a published Catalog, and a valid Stripe webhook configuration.
- Opaque rotating session tokens with absolute/idle expiry, user-owned active-session inventory, individual/device-wide revocation, a bounded user-visible security timeline, and password reauthentication for sensitive operations.
- System-wide WebAuthn passkeys with discoverable user-verified login, recent-auth enrollment/removal, passkey reauthentication, encrypted durable credentials/ceremonies, single-use three-minute challenges, atomic authenticator-counter fencing, and identity security events shared across account-api replicas.
- User-scoped passkey recovery with one-time 128-bit codes stored only as hashes, passkey-protected set replacement, password-plus-code consumption, session-bound ten-minute replacement grants, concurrent replay prevention, and no Account or operator authority.
- Mandatory customer-owner factor enrollment enforced in the shared Account authorizer: at least one passkey and one unused recovery code are required before Account selection or any owner operation. A safe global posture endpoint and Account-choice flag drive the prototype-informed browser onboarding state without becoming authorization inputs.
- Customer-visible factor-loss guidance states the password/code/replacement sequence and the fail-closed support boundary. The replacement-code form remains available when authenticators are physically unavailable but their durable credential records correctly remain registered.
- Externally issued Ed25519 platform-operator authorization bound to phishing-resistant assurance, actor, exact action/environment/reason/scope, and a ten-minute lifetime across every administrator process, with explicit incident/two-approver/deployment-confirmed break glass.
- Typed durable session assurance that independently records initial and most-recent password/passkey proof, exposes safe active-session classifications, and prevents password confirmation from satisfying a user-verified cryptographic step-up policy.
- Shared application-layer strong-auth policy requiring recent User-bound passkey assurance for Membership, ownership, invitation, Checkout, and Customer Portal mutations before persistence or provider calls; successful first passkey enrollment establishes that assurance after password bootstrap/recovery.
- Enumeration-resistant credential-recovery response bodies with identifier throttling, hashed single-use 30-minute links, atomic Argon2id credential replacement, User security-version advancement, and all-session revocation.
- Keyed, privacy-preserving network-actor derivation with explicit trusted-proxy CIDRs and PostgreSQL-backed login/recovery budgets shared across account-api replicas.
- Argon2id local credentials created atomically with verified identity and Account provisioning, plus generic login failures and durable identifier-hash lockouts.
- A shared authorization policy that keeps authentication, Account Membership, role, Account state, and Feature Package access separate.
- Explicit Account listing and selection; the selected browser Account is never treated as authorization without rechecking Membership and placement.
- Invitation creation and acceptance for existing system-wide identities. Acceptance creates a Membership, not a duplicate User or per-customer runtime.
- Account Membership governance with owner/administrator active-and-suspended roster visibility, owner-only non-owner role changes, bounded removal and suspension authority, role-preserving reactivation, non-owner self-service leave, optimistic versions, atomic ownership transfer, transactional actor/target rechecks, and immutable before/after audit events.
- Recoverable Account closure with owner/passkey request and cancellation, optimistic Account versions, immediate access freeze, seven-day cooling-off, projected-billing preflight/retry blockers, immutable lifecycle events, irreversible logical close, and an explicit post-close retention deadline. A shared leased worker performs due transitions; physical erasure remains a separate operator-governed workflow.
- A responsive private application shell for signup, verification, login, Account switching, invitation acceptance, package visibility, and the operational overview.
- Raw-body Stripe signature verification, durable event deduplication, leased asynchronous processing, crash recovery, and bounded retry scheduling.
- Server-created Stripe Customer, hosted Checkout, and Customer Portal sessions using authorized Account roles, UUID idempotency keys, exact return origins, and private Offer-to-Price mappings.
- Durable Account-scoped Checkout reservations prevent parallel subscription attempts and make hosted-session retries resumable.
- Current-object Subscription projection: webhook events are invalidation signals, paid grants are replaced transactionally, snapshots only advance when effective access changes, and `past_due` becomes read-only.
- Leased reconciliation and verified-event replay boundaries for billing workers.
- Signed `billing-admin` inspection, verified-event replay, and current-subscription refresh controls with exact Stripe-mode/environment/target binding, execute-only security-definer functions, and immutable same-transaction audit evidence.
- Executable `account-api`, `billing-worker`, and `account-lifecycle-worker` process modes with strict environment validation, independent connection caps, graceful shutdown, and dependency-aware readiness.
- An executable `notification-worker` process with encrypted durable identity and Account-ownership envelopes, leased claims, independent per-recipient delivery, crash recovery, bounded retries, terminal dead-letter state, and implicit-TLS SMTP delivery. Ownership transfer atomically inserts previous/new-owner notices with the role swap and immutable event, so SMTP availability cannot split authority from notification intent.
- Immutable Catalog draft, private Stripe mapping, independent review, effective publication, retirement, rollback, and same-transaction operator audit workflows exposed through a fail-closed one-shot command.
- Bounded account-api Catalog refresh that propagates effective publications and lower-version rollbacks across replicas without restarts while preserving one snapshot per operation.
- Durable existing-Account entitlement rollouts created atomically with Catalog publication, with bounded cursor seeding, leased claims, retries, dead letters, and late-Account drift repair.
- Free-plan reconciliation that uses the published package versions and default limits, preserves every independently sourced grant, and appends immutable snapshots only when effective access changes.
- An executable independently scalable `entitlement-worker` process with database-only authority, strict configuration bounds, graceful shutdown, and dependency-aware readiness.
- Typed Catalog limit definitions with explicit package ownership, unit, capacity kind, combination rule, optional recovery TTL, and strict governed-draft validation.
- Deterministic multi-source limit evaluation with stable equal-priority ordering and immutable enforcement policies embedded in Account snapshots.
- A transport-neutral package/usage admission service with specific `package_not_entitled`, `package_read_only`, `limit_not_defined`, and `limit_exceeded` decisions.
- PostgreSQL-backed capacity counters and UUID-keyed reservations with entitlement-version fencing, concurrency-safe admission, idempotent retry/release, conflict detection, and lease expiry reclamation.
- A typed Work aggregate and transport-neutral application boundary with exhaustive lifecycle/role policy, three-level hierarchy, assignment/provenance values, optimistic versions, Work package enforcement, and governed active-item admission.
- Pooled-cell Work persistence with Account-local numbers, composite relationships, forced RLS, mutation events, cursor queue/direct-child/summary queries, explicit capacity-release checkpoints, and classified adapter errors.
- Executable global `app-router` and cell `app-api` modes with session-backed Account authorization, allowlisted cell destinations, short-lived method/target/body-bound route signatures, rotating verification keys, credential stripping, bounded proxying, shared replay receipts, placement-generation enforcement, one-retry same-cell connection recovery with fresh route IDs and stable mutation identity, idempotent admission connection recovery, and content-free route/admission transport status.
- Executable shared per-cell `route-receipt-worker` replicas with identifier-only lease coordination, bounded forced-RLS pruning, concurrent-insert schedule fencing, crash recovery, and content-free backlog status.
- A three-database PostgreSQL 17 routing contract with two independently composed routers and two cell API replicas, covering system-wide session continuity across a cold router handoff, shared replay defense, two physical cells, Account placement and generation-advanced movement, wrong-cell/cross-Account proof attacks, stale pre-move proofs, bounded two-attempt assigned-cell outage without cross-cell fallback, and fail-closed disabled-cell cache refresh.
- A one-shot `route-canary` that tests candidate signing keys and workload certificates through the protected internal Account route and admission verifier, plus overlapping-CA client/server rollover and retired-leaf rejection evidence.
- Signed Account-scoped Work list, summary, detail, and direct-child reads with opaque cursors, ETags, explicit safe DTOs, a route-claim application authorizer, and a prototype-informed package-aware queue/detail shell.
- An independently scalable `work-reconciler` with atomic cell outbox enqueue, unique crash-recovery leases, idempotent global release, Account-RLS checkpointing, bounded retry/dead-letter behavior, split least-privilege database credentials, content-free backlog status, and SKIP-LOCKED retention batches for completed technical jobs.
- An executable private `admission-api` plus HTTP client adapter that re-verifies signed Work operation proofs, reauthorizes current global access, fences entitlement versions through a no-PUBLIC-execute lock function, and keeps global SQL credentials out of app-api.
- Routed Work create, transition, and assignment contracts with body/semantic-header binding, UUID idempotency, ETag preconditions, safe interactive assignment bounds, exact-retry handling for ambiguous commits, and prototype-informed creation/lifecycle browser controls.
- A one-shot `work-release-admin` command with exact environment confirmation, bounded content-free dead-letter inspection, exact-target requeue, immutable same-transaction audit, and an execute-only security-definer database boundary that cannot read customer Work or directly mutate the queue.
- Embedded global, cell, and development PostgreSQL migrations with advisory locking, immutable checksums, an application ledger, and a one-shot production runner.
- Review-only Kubernetes reference resources for shared workload classes, autoscaling, disruption budgets, restricted pods, default-deny networking, and an explicit zero-unavailable/node-spread app-router rollout.
- GitHub verification for Go format/test/race/vet, disposable PostgreSQL contracts, vulnerability scanning, Kubernetes reference rendering, and public-site build/lint/production dependency audit.

The development command is intentionally memory-backed and refuses to start unless `SPYGLASS_ENV=development`. Persistent `account-api`, `app-router`, `app-api`, `admission-api`, `route-receipt-worker`, `billing-worker`, `notification-worker`, `entitlement-worker`, `account-lifecycle-worker`, `work-reconciler`, and one-shot `route-canary`/`catalog-admin`/`work-release-admin` modes now exist and fail closed until their workload-specific PostgreSQL, route proof, Stripe, origin, encryption, operator, environment-confirmation, workload TLS, or TLS mail configuration is supplied by the environment.

## Run locally

```powershell
$env:SPYGLASS_ENV = 'development'
.\.tools\go\bin\go.exe run .\cmd\spyglass
```

The development API listens on `:8080` by default:

```text
GET  /health/live
GET  /health/ready
GET  /api/v1/catalog/public
POST /api/v1/registrations
POST /api/v1/registrations/verify
POST /api/v1/recovery-challenges
POST /api/v1/recovery-challenges/complete
POST /api/v1/sessions
POST /api/v1/passkey-login/challenges
POST /api/v1/passkey-login/challenges/{ceremonyID}/complete
GET  /api/v1/passkeys
POST /api/v1/passkey-registrations
POST /api/v1/passkey-registrations/{ceremonyID}/complete
DELETE /api/v1/passkeys/{credentialID}
POST /api/v1/passkey-reauthentications
POST /api/v1/passkey-reauthentications/{ceremonyID}/complete
GET  /api/v1/recovery-codes
POST /api/v1/recovery-codes
POST /api/v1/recovery-codes/consume
GET  /api/v1/security-posture
GET  /api/v1/sessions
GET  /api/v1/security-events
DELETE /api/v1/sessions
DELETE /api/v1/sessions/{sessionID}
DELETE /api/v1/session
POST /api/v1/session/reauthenticate
GET  /api/v1/session/accounts
POST /api/v1/session/account
GET  /api/v1/account-closures
POST /api/v1/accounts/{accountID}/closure
DELETE /api/v1/accounts/{accountID}/closure
POST /api/v1/accounts/{accountID}/invitations
GET  /api/v1/accounts/{accountID}/memberships
PATCH /api/v1/accounts/{accountID}/memberships/{membershipID}
DELETE /api/v1/accounts/{accountID}/memberships/{membershipID}
POST /api/v1/accounts/{accountID}/memberships/{membershipID}/suspensions
DELETE /api/v1/accounts/{accountID}/memberships/{membershipID}/suspensions
DELETE /api/v1/accounts/{accountID}/membership
POST /api/v1/accounts/{accountID}/ownership-transfers
POST /api/v1/accounts/{accountID}/checkout-sessions
POST /api/v1/accounts/{accountID}/billing-portal-sessions
GET  /api/v1/accounts/{accountID}/billing
POST /api/v1/invitations/accept
POST /webhooks/stripe                 # only when a development webhook secret is configured
```

Browser routes on the same private application origin are:

```text
GET|POST /signup
GET|POST /verify
GET|POST /login
GET|POST /forgot-password
GET|POST /reset-password
GET      /app
GET      /app/work
GET      /app/security
GET      /app/account-closures
POST     /app/account-closures/request
POST     /app/account-closures/cancel
POST     /app/security/reauthenticate
POST     /app/security/sessions/revoke
POST     /app/security/sessions/revoke-all
POST     /app/account
POST     /app/invitations
POST     /app/memberships/role
POST     /app/memberships/remove
POST     /app/memberships/suspend
POST     /app/memberships/reactivate
POST     /app/memberships/leave
POST     /app/ownership-transfer
POST     /app/billing/checkout
POST     /app/billing/portal
GET|POST /invitations/accept
POST     /logout
```

Browser mutations require an allowlisted exact Origin. Production cookies use the `__Host-` prefix, `Secure`, `HttpOnly`, `SameSite=Lax`, and root-only scope. Development uses visibly named non-secure cookies so browsers do not silently reject invalid `__Host-` combinations on localhost.

Development registration responses include `development_verification_token` because there is no email delivery adapter yet. The composition refuses to expose this behavior outside the explicit development environment.

Run the public site separately:

```powershell
cd website
npm run dev
```

## Next production slices

1. Complete customer-visible factor-loss/support review and real-device accessibility validation around the executable mandatory owner enrollment, privileged-operation step-up, and self-service recovery-code path; continue the audited post-retention Account export/physical-erasure workflow with external directive publication/archive, external-store attestations, and deployment grants now that signed platform-operator authorization, dual-approved break glass, leased cross-store execution, atomic database finalization, content-free checkpoint ledgers, runtime restore quarantine, and signed ordered database replay are executable; then add multi-version notification key rotation, retention/operator handling for dead letters, and scheduled cleanup for durable abuse-control state.
2. Apply the proven private admission and reconciliation boundaries to Agents, Knowledge, Finance, and Marketing use cases as those package slices become executable.
3. Execute live Stripe test-mode contract journeys through the audited reconciliation/replay controls; local request translation, database contracts, and signed operator commands are executable.
4. Complete the trust/deployment side of fair asynchronous runner control before promoting reference manifests. The transport-neutral scheduler, identifier-only durable queue, weighted Account fairness, concurrency fencing, crash-recovery leases, fail-closed ambiguous-create reconciliation, due-time terminal inspection, digest-bound fixed-profile Kubernetes Job launch, Account-bound durable cancellation, preconditioned foreground Job deletion, terminal capacity release, least-privilege database role contract, Account-erasure integration, broker-audience Pod token projection, and online Pod→Job→invocation identity verifier are executable. Encrypted broker payload/result exchange, runner execution client, custom scaling signals, environment-specific RBAC/NetworkPolicy/RuntimeClass, and load evidence remain.
5. Replace the website signup handoff with the deployed application origin and generated API client, then complete end-to-end registration accessibility and security tests.

## Evidence and current limits

- `go test ./...`, `go vet ./...`, and `govulncheck ./...` pass with Go 1.26.6. Go 1.26.5 was rejected after the vulnerability scan found reachable standard-library advisories fixed by 1.26.6.
- Rendered browser journey coverage proves signup → verification/password → password/passkey login surfaces → Account shell, identity security/passkey management, Membership lifecycle controls, locked/read-only Work package behavior, entitled Work queue structure, and enabled create controls; API journey coverage proves invitation → existing identity → Membership role/suspension/reactivation/ownership/self-leave → Account-list revocation and signed Work read/command contracts.
- The public website build, rendered-route tests, lint, and production dependency audit pass.
- The private website preview is deployed at `https://infinite-ocean-spyglass.tinfoyle.chatgpt.site`.
- Disposable PostgreSQL 17 tests execute all migration sets, verify idempotency and checksum drift rejection through a schema-qualified canonical ledger, exercise network-actor budgets, encrypted Account-attributed notification persistence, attributed provider-event persistence, post-erasure provider-retry suppression behind the shared Account-erasure fence, four-eyes Account-erasure preparation, leased split-role cell/global execution, exact Account deletion with other-Account/User preservation, content-free tombstones, one-time capacity decrement and idempotent completion, fair runner admission with noisy-neighbor concurrency isolation and stale-lease recovery, runner-control erasure/replay coverage, passkey persistence, single-use passkey ceremonies, credential-counter fencing, cross-User isolation, active-plus-retained passkey keys, old-envelope re-encryption and old-key retirement, Account closure billing/freeze/recovery/block/retry/retention invariants, concurrent Catalog version allocation, four-eyes publication, immutable content/mappings, forward and lower-version entitlement rollout, unchanged-access drift repair, independent-grant preservation, governed limit propagation, concurrent capacity admission without oversubscription, UUID retry/release idempotency, expiry reclamation, stale-entitlement rejection, registration, Checkout reservation concurrency, broker-backed Work creation/compensation through split global/cell roles, Work optimistic concurrency, direct Account-scoped Work queries, and transaction-local RLS isolation through non-owner roles. Kubernetes resources remain review-only and have not been applied to a cluster.
- Stripe request translation, event ingestion, deduplication, out-of-order convergence, and queue behavior are tested with local fixtures; no Stripe account mutation has been performed.
- The private Account shell renders local billing status and paid offers and can enter Checkout/Portal in the persistent composition; no app-owned credentials or Account data were moved into the public Sites deployment.
