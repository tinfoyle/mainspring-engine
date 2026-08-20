# Stage and production deployment report

- Audit date: 2026-08-20
- Audited revision: `b195fae07b264ea4a609e424d6664f0b776eb6a4`
- Verdict: **the repository can publish a signed image, but it cannot yet deploy a stage or production application environment**

## Current deployment state

Implemented:

- CI verifies Go, race, vet, OpenAPI drift, vulnerabilities, website build/lint/audit, PostgreSQL migrations, Kubernetes reference rendering, observability configuration and the static image contract.
- `.github/workflows/release-image.yml` can publish one immutable AMD64/ARM64 GHCR digest with SBOM, provenance, GitHub attestation and keyless Cosign signature.
- `deploy/kubernetes/reference/` defines shared workload classes, isolation defaults, HPAs, PDBs, NetworkPolicies, runner RBAC and an observability gateway.
- `cmd/staging-cert` binds anonymous-origin evidence to exact origins, revision, image digest and Catalog.
- The public website is a stateless Sites/Vinext application with one existing project binding and a private preview.

Missing:

- No `staging` or `production` Kubernetes overlay exists.
- No application deployment/promotion workflow exists; only verification and image publication workflows are checked in.
- The reference deliberately contains placeholder images and omits secrets, certificates, ingress, exact egress, managed-service endpoints, RuntimeClass, metrics adapter, monitors, paging and connection caps.
- No first reviewed image digest, admission enforcement, applied-cluster evidence or two-digest rollback evidence is archived.
- The public Sites project has no checked-in stage/production separation or automated promotion record.

Do not apply the reference manifests directly.

## Target environment separation

| Boundary | Stage | Production |
|---|---|---|
| Public origin | `https://staging.infiniteocean.net` | `https://infiniteocean.net` |
| Private app origin | `https://app.staging.infiniteocean.net` | `https://app.infiniteocean.net` |
| Sites access | Private/restricted synthetic testing | Reviewed public access and production domain |
| Kubernetes | Dedicated stage cluster or strongly isolated stage environment | Dedicated production cluster/account |
| Databases | One stage global PostgreSQL plus two stage cell databases | Independent production global/cell fleet |
| Providers | Stripe test mode, non-production TLS SMTP, non-production model/provider accounts | Stripe live mode and production provider accounts only after approval |
| Trust | Independent route keys, workload CAs, envelope keys and operator authorization | Independent production keys/CAs; never copied from stage |
| Catalog | Reviewed synthetic/test price mappings | Reviewed live mappings and only completed packages |
| Evidence | Full destructive/failure certification permitted | Non-destructive release checks plus controlled canary evidence |

Use distinct cloud accounts/projects, secret stores, databases, provider credentials, DNS zones/delegations and telemetry destinations wherever practical. Never promote mutable configuration or secrets by copying a stage Secret object into production.

## Revision-controlled deployment artifacts to add

Create, review and verify these without committing credentials:

```text
deploy/kubernetes/overlays/staging/
deploy/kubernetes/overlays/production/
.github/workflows/deploy-staging.yml
.github/workflows/promote-production.yml
docs/production/environments/staging.md
docs/production/environments/production.md
docs/production/release-record-template.md
```

Each overlay must provide:

- the exact `ghcr.io/tinfoyle/spyglass-engine@sha256:...` image;
- environment labels, replica floors, resource bounds and database connection caps;
- External Secret/certificate-controller references and workload-specific identities;
- ingress/WAF/DNS/TLS routing, with browsers never routed directly to cell `app-api`;
- exact database, provider, observability, secret-controller and Kubernetes API egress;
- a tested `spyglass-sandboxed` RuntimeClass and runner-node placement;
- metrics adapter mappings for every referenced external metric;
- authenticated Pod/Service monitors, SLO rules, dashboards and paging routes;
- restore checkpoint ConfigMaps, Catalog/environment identifiers and safe feature flags;
- admission policy that verifies the expected release workflow identity, signature, digest and scan result.

CI must render and policy-test both overlays. Deployment workflows should use protected GitHub Environments, workload identity rather than long-lived cloud keys, concurrency locks, an immutable release record and human approval before production.

## One-time environment bootstrap

1. Select the cloud, regions, Kubernetes distribution, managed PostgreSQL service, secret/certificate controllers, ingress/WAF, telemetry backend, paging service and sandbox runtime.
2. Create the stage and production trust boundaries and least-privilege deployment identities.
3. Provision global and initial cell databases with backup, point-in-time recovery, quarantine restore targets, connection budgets and failover policy.
4. Create separate migration, serving, worker, operator and restore-replay database roles. Serving roles must not own protected tables and must not have `SUPERUSER` or `BYPASSRLS`.
5. Configure DNS/TLS, provider allowlists, Stripe endpoints, TLS SMTP, workload CAs, route-signing keys, passkey RP IDs/origins and exact trusted proxy ranges.
6. Connect the metrics adapter, authenticated scraping, Collector backend, dashboards, alerts and paging before customer traffic.
7. Define owners, support/escalation, retention, legal/privacy, incident and change-approval procedures.

Infrastructure credentials, private keys, provider secrets, synthetic customer identifiers and raw certification payloads remain outside Git. Git contains references, schemas, policies and evidence indexes only.

## Release and stage deployment path

### 1. Produce the candidate once

1. Start from a clean, reviewed commit with required CI green.
2. Tag `spyglass-v<semver>` or use the protected release dispatch.
3. Capture the workflow's immutable manifest digest.
4. Verify GitHub attestation and Cosign identity, inspect SBOM/provenance, scan the digest and run `spyglass version`.
5. Record revision, digest, migration set, configuration/overlay digest and intended Catalog version.

The same digest must move through stage, internal canary, customer canary and production. Never rebuild for promotion.

### 2. Prepare databases and Catalog

1. Back up and record restore checkpoints.
2. Run the shared image as `spyglass migrate` with the independent migration credential: global first, then each cell.
3. Verify migration checksums, forced RLS and constrained role grants.
4. Publish the reviewed stage Catalog and Stripe test-price mappings through short-lived, signed operator jobs.

Use forward-compatible migrations. Rollback normally changes the application digest and configuration; it does not run destructive down migrations.

### 3. Deploy stage workloads

Apply the stage overlay in dependency order:

1. namespaces, ServiceAccounts, policies, certificates, runtime configuration and observability;
2. global Account API and global workers;
3. per-cell app API, admission/routing boundary and receipt worker;
4. Work/Agent reconciliation, dispatch/projection, runner controller/broker and model gateway only for enabled staged packages;
5. app router and ingress after readiness and trust canaries succeed.

Run route-key and workload-certificate canaries before retiring old trust material. Confirm readiness, restore gates, database connection headroom, exact egress and HPA metric availability.

### 4. Deploy the public stage site

The public site uses Sites, not the application image. Build it under Node 22.13+ with:

- `NEXT_PUBLIC_SPYGLASS_APP_ORIGIN=https://app.staging.infiniteocean.net`;
- hosted `SPYGLASS_ACCOUNT_API_ORIGIN` pointing to the stage anonymous Catalog boundary.

Use a separate restricted stage Sites project from the public production project. Package and save the exact validated source version, deploy it privately/restricted, then bind the stage domain. The current single private-preview project must not be silently treated as production. Keep D1/R2 disabled unless the public site's stateless architecture deliberately changes.

### 5. Certify stage

Run the complete sequence in [staging-certification.md](staging-certification.md):

1. anonymous exact-origin gate;
2. identity, Account, SMTP and real-device passkey journey;
3. Stripe test-mode failure/recovery/reconciliation journey;
4. isolation, read/write load and fairness;
5. pod/node/provider/database failure and queue recovery;
6. quarantine restore plus signed directive replay;
7. public/private accessibility and device matrix;
8. internal then bounded customer-like canary and burn-in/game day.

Any code, image, Catalog, policy, secret, ingress, migration or environment change invalidates affected evidence and requires a new release record.

## Production promotion path

1. Confirm every P0 item in [production-readiness-audit.md](production-readiness-audit.md) has current evidence from the exact digest and target-equivalent stage configuration.
2. Complete external security/privacy review and obtain engineering, security, product and operations approval.
3. Verify production backups/restore targets, production Catalog/live Stripe mappings, legal/support ownership and rollback compatibility.
4. Apply forward-compatible production migrations with the same image and migration set.
5. Deploy the same digest to production with routes closed or limited to internal Accounts.
6. Deploy the exact public-site version to the production Sites project with production runtime values and reviewed public access/domain.
7. Run non-destructive production boundary, health, signature, Catalog, billing-mismatch, isolation and observability checks.
8. Expand from internal Accounts to a bounded customer cohort, observe the agreed window, then broaden traffic only if no mismatch, isolation, duplicate-effect, restore or accessibility failure remains.

Production may launch before all later product phases only if incomplete packages and claims are removed from the Catalog, website, entitlements and transports. A full advertised Work/Attention product requires [phase-3-report.md](phase-3-report.md) completion.

## Rollback path

- **Application:** route the cohort to a previously retained, verified digest whose schema and configuration compatibility are recorded.
- **Database:** prefer forward repair; never improvise a destructive down migration. Restore only into quarantine, satisfy replay checkpoints, and rerun isolation/release tests before ingress.
- **Catalog/billing:** republish a prior immutable Catalog version and reconcile local/provider state; never authorize directly from Stripe.
- **Placement:** stop or roll back through a new placement generation; never point a router at a guessed cell.
- **Trust:** retain overlapping route keys/CAs until canaries prove the new material, then retire by runbook.
- **Public site:** redeploy the previously retained Sites version and verify its app handoff and Catalog proxy against the compatible application release.

Rollback is a launch gate only after it has been rehearsed between two real retained artifacts and its evidence is attached to the release record.
