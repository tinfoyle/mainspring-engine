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
- Opaque rotating session tokens with absolute/idle expiry and user-wide revocation.
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
- Implicit-TLS SMTP delivery for registration verification and Account invitations; production tokens are delivered rather than logged or returned.
- Global and cell PostgreSQL migration drafts, including Account-scoped row-level security.
- Review-only Kubernetes reference resources for shared workload classes, autoscaling, disruption budgets, restricted pods, and default-deny networking.
- GitHub verification for Go format/test/vet and public-site build/lint/production dependency audit.

The development command is intentionally memory-backed and refuses to start unless `SPYGLASS_ENV=development`. Persistent `account-api` and `billing-worker` modes now exist, but fail closed until PostgreSQL, Stripe, origin, and TLS mail configuration are supplied by the environment.

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
POST /api/v1/sessions
DELETE /api/v1/session
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
GET      /app
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

1. Execute the PostgreSQL migrations and repository contracts against disposable real PostgreSQL in CI; no PostgreSQL runtime is available in the current workstation environment.
2. Add passkeys/MFA, credential recovery, security-event history, reauthentication for sensitive operations, session-management UI, and distributed rate limiting by both identifier and network actor.
3. Add Catalog draft/review/publication administration and enforcement adapters for every HTTP/MCP/job/tool entry point.
4. Execute Stripe test-mode contract tests and add audited operator commands over the reconciliation/replay boundaries.
5. Implement app-router/app-api/billing-worker process modes, signed route context, directory caching, fair admission, custom scaling signals, and ephemeral runner control before promoting the reference manifests.
6. Replace the website signup handoff with the deployed application origin and generated API client, then complete end-to-end registration accessibility and security tests.

## Evidence and current limits

- `go test ./...`, `go vet ./...`, and `govulncheck ./...` pass with Go 1.26.6. Go 1.26.5 was rejected after the vulnerability scan found reachable standard-library advisories fixed by 1.26.6.
- Rendered browser journey coverage proves signup â†’ verification/password â†’ login â†’ Account shell, and API journey coverage proves invitation â†’ existing identity â†’ Membership â†’ Account list.
- The public website build, rendered-route tests, lint, and production dependency audit pass.
- The private website preview is deployed at `https://infinite-ocean-spyglass.tinfoyle.chatgpt.site`.
- PostgreSQL SQL and Kubernetes resources are reviewable but have not been integration-tested or applied from this workstation because neither PostgreSQL nor a Kubernetes/Docker runtime is installed.
- Stripe request translation, event ingestion, deduplication, out-of-order convergence, and queue behavior are tested with local fixtures; no Stripe account mutation has been performed.
- The private Account shell renders local billing status and paid offers and can enter Checkout/Portal in the persistent composition; no app-owned credentials or Account data were moved into the public Sites deployment.
