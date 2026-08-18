# Phase 2 Development Slice

This repository now contains the first executable production slice for Infinite Ocean: Spyglass.

## Included

- `website/`: the public Infinite Ocean site with Product, Packages, Pricing, signup entry, company, security, privacy, and terms routes.
- `cmd/spyglass`: a development-only composition for the account API.
- Identity domain: pending User registration and verified activation.
- Accounts domain: free Account creation and owner Membership.
- Catalog domain: versioned Feature Packages, Free/Team/Operating Plans, and a public offer projection that omits Stripe references.
- Entitlements domain: grants, deterministic priority evaluation, immutable snapshots, and read versus mutation checks.
- Placement domain: capacity-aware Account assignment to a shared cell.
- Registration application flow: verification challenge followed by atomic User/Account/Membership/placement/free-entitlement provisioning.
- PostgreSQL registration, published Catalog, session, access-state, and signed billing-inbox adapters.
- Production account-api composition that requires PostgreSQL, real verification delivery, a published Catalog, and a valid Stripe webhook configuration.
- Opaque rotating session tokens with absolute/idle expiry, user-owned active-session inventory, individual/device-wide revocation, a bounded user-visible security timeline, and password reauthentication for sensitive operations.
- Enumeration-resistant credential-recovery response bodies with identifier throttling, hashed single-use 30-minute links, atomic Argon2id credential replacement, User security-version advancement, and all-session revocation.
- Keyed, privacy-preserving network-actor derivation with explicit trusted-proxy CIDRs and PostgreSQL-backed login/recovery budgets shared across account-api replicas.
- Argon2id local credentials created atomically with verified identity and Account provisioning, plus generic login failures and durable identifier-hash lockouts.
- A shared authorization policy that keeps authentication, Account Membership, role, Account state, and Feature Package access separate.
- Explicit Account listing and selection; the selected browser Account is never treated as authorization without rechecking Membership and placement.
- Invitation creation and acceptance for existing system-wide identities. Acceptance creates a Membership, not a duplicate User or per-customer runtime.
- A responsive private application shell for signup, verification, login, Account switching, invitation acceptance, package visibility, and the operational overview.
- Raw-body Stripe signature verification, durable event deduplication, leased asynchronous processing, crash recovery, and bounded retry scheduling.
- Server-created Stripe Customer, hosted Checkout, and Customer Portal sessions using authorized Account roles, UUID idempotency keys, exact return origins, and private Offer-to-Price mappings.
- Durable Account-scoped Checkout reservations prevent parallel subscription attempts and make hosted-session retries resumable.
- Current-object Subscription projection: webhook events are invalidation signals, paid grants are replaced transactionally, snapshots only advance when effective access changes, and `past_due` becomes read-only.
- Leased reconciliation and verified-event replay boundaries for billing workers and future operator tooling.
- Executable `account-api` and `billing-worker` process modes with strict environment validation, independent connection caps, graceful shutdown, and dependency-aware readiness.
- An executable `notification-worker` process with encrypted durable identity-message envelopes, leased claims, crash recovery, bounded retries, terminal dead-letter state, and implicit-TLS SMTP delivery.
- Immutable Catalog draft, private Stripe mapping, independent review, effective publication, retirement, rollback, and same-transaction operator audit workflows exposed through a fail-closed one-shot command.
- Bounded account-api Catalog refresh that propagates effective publications and lower-version rollbacks across replicas without restarts while preserving one snapshot per operation.
- Durable existing-Account entitlement rollouts created atomically with Catalog publication, with bounded cursor seeding, leased claims, retries, dead letters, and late-Account drift repair.
- Free-plan reconciliation that uses the published package versions and default limits, preserves every independently sourced grant, and appends immutable snapshots only when effective access changes.
- An executable independently scalable `entitlement-worker` process with database-only authority, strict configuration bounds, graceful shutdown, and dependency-aware readiness.
- Typed Catalog limit definitions with explicit package ownership, unit, capacity kind, combination rule, optional recovery TTL, and strict governed-draft validation.
- Deterministic multi-source limit evaluation with stable equal-priority ordering and immutable enforcement policies embedded in Account snapshots.
- A transport-neutral package/usage admission service with specific `package_not_entitled`, `package_read_only`, `limit_not_defined`, and `limit_exceeded` decisions.
- PostgreSQL-backed capacity counters and UUID-keyed reservations with entitlement-version fencing, concurrency-safe admission, idempotent retry/release, conflict detection, and lease expiry reclamation.
- Embedded global, cell, and development PostgreSQL migrations with advisory locking, immutable checksums, an application ledger, and a one-shot production runner.
- Review-only Kubernetes reference resources for shared workload classes, autoscaling, disruption budgets, restricted pods, and default-deny networking.
- GitHub verification for Go format/test/race/vet, disposable PostgreSQL contracts, vulnerability scanning, and public-site build/lint/production dependency audit.

The development command is intentionally memory-backed and refuses to start unless `SPYGLASS_ENV=development`. Persistent `account-api`, `billing-worker`, `notification-worker`, `entitlement-worker`, and one-shot `catalog-admin` modes now exist and fail closed until their workload-specific PostgreSQL, Stripe, origin, encryption, operator, or TLS mail configuration is supplied by the environment.

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
GET  /api/v1/sessions
GET  /api/v1/security-events
DELETE /api/v1/sessions
DELETE /api/v1/sessions/{sessionID}
DELETE /api/v1/session
POST /api/v1/session/reauthenticate
GET  /api/v1/session/accounts
POST /api/v1/session/account
POST /api/v1/accounts/{accountID}/invitations
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
GET      /app/security
POST     /app/security/reauthenticate
POST     /app/security/sessions/revoke
POST     /app/security/sessions/revoke-all
POST     /app/account
POST     /app/invitations
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

1. Add passkeys/MFA, multi-version notification key rotation, retention/operator handling for dead letters, and scheduled cleanup for durable abuse-control state.
2. Wire the shared usage-admission boundary into each concrete Work/Agents/Knowledge/Finance/Marketing HTTP, MCP, schedule, job, and tool use case as those modules land; add periodic consumption and model/cost budget kinds through new governed semantics rather than overloading capacity.
3. Execute Stripe test-mode contract tests and add audited operator commands over the reconciliation/replay boundaries.
4. Implement app-router/app-api/billing-worker process modes, signed route context, directory caching, fair admission, custom scaling signals, and ephemeral runner control before promoting the reference manifests.
5. Replace the website signup handoff with the deployed application origin and generated API client, then complete end-to-end registration accessibility and security tests.

## Evidence and current limits

- `go test ./...`, `go vet ./...`, and `govulncheck ./...` pass with Go 1.26.6. Go 1.26.5 was rejected after the vulnerability scan found reachable standard-library advisories fixed by 1.26.6.
- Rendered browser journey coverage proves signup â†’ verification/password â†’ login â†’ Account shell, and API journey coverage proves invitation â†’ existing identity â†’ Membership â†’ Account list.
- The public website build, rendered-route tests, lint, and production dependency audit pass.
- The private website preview is deployed at `https://infinite-ocean-spyglass.tinfoyle.chatgpt.site`.
- Disposable PostgreSQL 17 tests execute all migration sets, verify idempotency and checksum drift rejection, exercise network-actor budgets, encrypted notification delivery, concurrent Catalog version allocation, four-eyes publication, immutable content/mappings, forward and lower-version entitlement rollout, unchanged-access drift repair, independent-grant preservation, governed limit propagation, concurrent capacity admission without oversubscription, UUID retry/release idempotency, expiry reclamation, stale-entitlement rejection, registration, Checkout reservation concurrency, and transaction-local RLS isolation through a non-owner serving role. Kubernetes resources remain review-only and have not been applied to a cluster.
- Stripe request translation, event ingestion, deduplication, out-of-order convergence, and queue behavior are tested with local fixtures; no Stripe account mutation has been performed.
- The private Account shell renders local billing status and paid offers and can enter Checkout/Portal in the persistent composition; no app-owned credentials or Account data were moved into the public Sites deployment.
