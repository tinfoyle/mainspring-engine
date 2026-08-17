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
- Global and cell PostgreSQL migration drafts, including Account-scoped row-level security.

The running adapter is intentionally memory-backed and refuses to start unless `SPYGLASS_ENV=development`. It proves domain composition and contracts; it is not a production persistence substitute.

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
```

Development registration responses include `development_verification_token` because there is no email delivery adapter yet. The composition refuses to expose this behavior outside the explicit development environment.

Run the public site separately:

```powershell
cd website
npm run dev
```

## Next production slices

1. PostgreSQL repositories and transaction-scoped RLS context for the global and cell schemas.
2. Hardened authentication sessions, verification delivery, recovery, and Account switching.
3. Published Catalog administration and package-feature enforcement adapters.
4. Stripe Checkout, Customer Portal, signed webhook inbox, asynchronous projection, and reconciliation.
5. Kubernetes manifests for pooled workload classes, cell routing, autoscaling, fairness, and ephemeral runners.
6. Replace the website signup handoff with the deployed private application origin and generated API client.
