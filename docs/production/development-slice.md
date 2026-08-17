# Phase 2 Development Slice

This repository now contains the first executable production slice for Infinite Ocean: Spyglass.

## Included

- `website/`: the public Infinite Ocean site with Product, Packages, Pricing, signup entry, company, security, privacy, and terms routes.
- `cmd/spyglass`: a development-only composition for the account API.
- Identity domain: pending User registration and verified activation.
- Accounts domain: free Account creation and owner Membership.
- Catalog domain: versioned Feature Packages, free Plan, and a public offer projection that omits Stripe references.
- Entitlements domain: grants, deterministic priority evaluation, immutable snapshots, and read versus mutation checks.
- Placement domain: capacity-aware Account assignment to a shared cell.
- Registration application flow: verification challenge followed by atomic User/Account/Membership/placement/free-entitlement provisioning.
- PostgreSQL registration, published Catalog, session, access-state, and signed billing-inbox adapters.
- Production account-api composition that requires PostgreSQL, real verification delivery, a published Catalog, and a valid Stripe webhook configuration.
- Opaque rotating session tokens with absolute/idle expiry and user-wide revocation.
- A shared authorization policy that keeps authentication, Account Membership, role, Account state, and Feature Package access separate.
- Raw-body Stripe signature verification, durable event deduplication, leased asynchronous processing, crash recovery, and bounded retry scheduling.
- Global and cell PostgreSQL migration drafts, including Account-scoped row-level security.
- Review-only Kubernetes reference resources for shared workload classes, autoscaling, disruption budgets, restricted pods, and default-deny networking.
- GitHub verification for Go format/test/vet and public-site build/lint/production dependency audit.

The running development command is intentionally memory-backed and refuses to start unless `SPYGLASS_ENV=development`. A persistent account-api composition now exists under `internal/bootstrap/accountapi`, but it deliberately has no executable fallback until real email delivery and environment secret wiring are supplied.

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
POST /webhooks/stripe                 # only when a development webhook secret is configured
```

Development registration responses include `development_verification_token` because there is no email delivery adapter yet. The composition refuses to expose this behavior outside the explicit development environment.

Run the public site separately:

```powershell
cd website
npm run dev
```

## Next production slices

1. Execute the PostgreSQL migrations and repository contracts against disposable real PostgreSQL in CI; no PostgreSQL runtime is available in the current workstation environment.
2. Add authentication identities, credential/passkey verification, recovery, CSRF-safe cookie transport, invitation and Account-switching endpoints around the session/access primitives.
3. Add Catalog draft/review/publication administration, Offer allowlisting, and enforcement adapters for every HTTP/MCP/job/tool entry point.
4. Add Stripe Checkout and Customer Portal adapters, current-object projection, Subscription/Grant convergence, reconciliation, and operator replay tooling.
5. Implement app-router/app-api/billing-worker process modes, signed route context, directory caching, fair admission, custom scaling signals, and ephemeral runner control before promoting the reference manifests.
6. Replace the website signup handoff with the deployed application origin and generated API client, then complete end-to-end registration accessibility and security tests.

## Evidence and current limits

- `go test ./...` and `go vet ./...` pass with Go 1.26.5.
- The public website build, rendered-route tests, lint, and production dependency audit pass.
- The private website preview is deployed at `https://infinite-ocean-spyglass.tinfoyle.chatgpt.site`.
- PostgreSQL SQL and Kubernetes resources are reviewable but have not been integration-tested or applied from this workstation because neither PostgreSQL nor a Kubernetes/Docker runtime is installed.
- Stripe event ingestion and queue behavior are tested with signed fixtures; no live/test Stripe account mutation has been performed.
