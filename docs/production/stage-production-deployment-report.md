# Local, stage and production deployment report

- Audit date: 2026-08-20
- Audited baseline: `b195fae07b264ea4a609e424d6664f0b776eb6a4`
- Corrected environment plan: local Docker in `ubunturojo`; Hostinger VPS staging over SSH alias `infiniteocean`; Linode Kubernetes production
- Verdict: **the application image exists, but the Docker deployment package, website image and environment-specific release automation still need to be built**

## Correction to the earlier report

`website/.openai/hosting.json` and the existing `chatgpt.site` URL are preview-era metadata. They are not part of the intended Spyglass release architecture. OpenAI Sites is not a local, staging or production deployment target and must not appear in promotion, certification or rollback procedures.

The public website and private Spyglass application will run in the same infrastructure tiers:

1. Docker inside the `ubunturojo` WSL distribution for local integration testing.
2. Docker Compose on the Hostinger VPS for connected staging.
3. Kubernetes on Linode for production and production-like cluster certification.

The preview metadata can remain temporarily while unrelated website work is in progress, but release automation must ignore it. Remove it in a separate reviewed website change once the container deployment path replaces the preview workflow.

## Current repository state

Implemented:

- The root `Dockerfile` builds the static, non-root, multi-mode Go application image.
- CI verifies Go, race, vet, OpenAPI drift, vulnerabilities, website build/lint/audit, PostgreSQL migrations, the Kubernetes reference, observability configuration and the application image contract.
- `.github/workflows/release-image.yml` can publish the application as one immutable AMD64/ARM64 GHCR digest with SBOM, provenance, attestation and Cosign signature.
- `deploy/kubernetes/reference/` defines the intended production workload and isolation base.
- Docker Engine 29.1.3 is available from `ubunturojo`.
- The `infiniteocean` SSH target is available from `ubunturojo`.

Missing:

- No root Spyglass Docker Compose definition exists; only prototype Docker assets are present.
- No production Dockerfile or release image exists for `website/`.
- No local/stage reverse-proxy configuration binds the public and private origins.
- No Hostinger deployment workflow/runbook exists.
- No Linode staging/production overlay exists.
- No application deployment or promotion workflow exists; only verification and application-image publication are automated.
- No first reviewed pair of application/website digests or rollback evidence is archived.

## Environment topology

| Boundary | Local — `ubunturojo` Docker | Stage — Hostinger VPS | Production — Linode Kubernetes |
|---|---|---|---|
| Public origin | Local reverse-proxy port/name | `https://staging.infiniteocean.net` | `https://infiniteocean.net` |
| Private app origin | Separate local reverse-proxy port/name | `https://app.staging.infiniteocean.net` | `https://app.infiniteocean.net` |
| Orchestrator | Docker Compose | Docker Compose over SSH target `infiniteocean` | Linode Kubernetes Engine |
| Databases | Three PostgreSQL containers: global, cell A, cell B | One global and two cell PostgreSQL services with durable volumes/backups, or managed equivalents | Independent production global/cell database fleet |
| Providers | Captures/fixtures; optional Stripe CLI and local SMTP sink | Stripe test mode, TLS SMTP and non-production provider accounts | Stripe live mode and production providers after approval |
| TLS | Local development certificates or HTTP on loopback only | Caddy/Traefik with public ACME certificates | Kubernetes Ingress and managed certificates |
| Secrets | Ignored local environment files or Docker secrets | Hostinger-owned environment/secrets outside Git | Kubernetes External Secrets/secret manager; no literal Git Secrets |
| Evidence | Fast integration and deterministic failure tests | Connected customer/provider journeys, restore and Docker failure tests | Kubernetes policy, identity, scaling, failure, canary and GA evidence |

Hostinger staging is appropriate for functional and provider certification, but it cannot prove Kubernetes NetworkPolicy, HPA, admission, RuntimeClass, Pod identity, node-loss or runner-isolation behavior. Those gates must run in a non-customer Linode namespace or equivalent production-like Linode environment before any customer production traffic.

## Revision-controlled deployment artifacts to add

```text
deploy/docker/spyglass/compose.yml
deploy/docker/spyglass/compose.local.yml
deploy/docker/spyglass/compose.stage.yml
deploy/docker/spyglass/Caddyfile.local
deploy/docker/spyglass/Caddyfile.stage
deploy/docker/spyglass/env/local.example
deploy/docker/spyglass/env/stage.example
deploy/kubernetes/overlays/linode-preproduction/
deploy/kubernetes/overlays/linode-production/
website/Dockerfile
website/.dockerignore
.github/workflows/release-website-image.yml
.github/workflows/deploy-hostinger-stage.yml
.github/workflows/promote-linode-production.yml
docs/production/environments/local-docker.md
docs/production/environments/hostinger-stage.md
docs/production/environments/linode-production.md
docs/production/release-record-template.md
```

Do not commit environment files containing credentials, private keys, database URLs, cookies, passkey material, provider payloads or synthetic customer identifiers.

## Local deployment in `ubunturojo`

### Required Compose stack

The common Compose file should model production process boundaries with shared images, not run the memory-backed development process as the only service:

- `public-website` from a Node 22-based website image;
- `edge` reverse proxy exposing public and private origins;
- `global-postgres`, `cell-a-postgres` and `cell-b-postgres` as separate services/volumes;
- one-shot global and per-cell migration services;
- `account-api`, `app-router`, one `app-api` per cell and private `admission-api`;
- notification, billing, entitlement, lifecycle, route-receipt and Work reconciliation workers;
- Agent dispatch/projection, runner controller/broker and model gateway behind an optional profile until those packages are under test;
- a local SMTP capture service and optional observability profile.

The local override may build from the checkout and publish developer-friendly loopback ports. The common definition must still use workload-specific environment blocks and database roles so local integration detects accidental authority sharing.

### Local workflow

1. From `ubunturojo`, build the application and website images with Docker Compose.
2. Start databases and run the one-shot global/cell migrations.
3. Start control-plane, cell, worker and reverse-proxy services.
4. Publish a deterministic local Catalog and test Account placements for both cells.
5. Run health/readiness, signup, routing, Work replay, wrong-Account and stale-placement tests through the proxy rather than directly against containers.
6. Run destructive/failure tests by restarting containers and disabling one cell without guessing a fallback.
7. Tear down containers normally; remove volumes only through an explicit reset command because they contain local test state.

The default local workflow should preserve volumes for debugging. A separate clearly named reset target may remove only the verified project-scoped Compose volumes.

## Website container requirement

The current root application image intentionally excludes `website/`, so the website needs its own immutable image.

Required image contract:

- Node 22.13+ build stage using `npm ci` and the existing lockfile;
- build, rendered-route tests and lint before publication;
- minimal non-root runtime with only built server/static output and required production dependencies;
- health endpoint suitable for Compose and Kubernetes probes;
- no `.openai/hosting.json`, Wrangler state, source credentials or preview deployment metadata in the runtime image;
- runtime configuration for the app handoff and anonymous Catalog origin.

Verify whether `NEXT_PUBLIC_SPYGLASS_APP_ORIGIN` is compiled into browser assets. If it is, replace it with a safe runtime configuration mechanism before requiring the same website digest in stage and production. Otherwise stage and production would need different builds and separate exact-artifact certification.

## Hostinger staging deployment

### One-time VPS bootstrap

1. Create a restricted deployment user behind the existing `infiniteocean` SSH target; disable password/root deployment.
2. Install and pin supported Docker Engine and Compose plugin versions.
3. Configure firewall rules for SSH, HTTP and HTTPS only; keep databases and internal services on private Docker networks.
4. Configure DNS for the stage public/private origins and Caddy/Traefik ACME storage.
5. Create durable data, backup and release directories with explicit ownership and capacity alerts.
6. Install a secret-delivery mechanism outside the Git checkout.
7. Configure off-host encrypted backups and a tested restore location.
8. Configure logs, metrics, alerts, paging and disk/certificate expiry monitoring.

### Stage release workflow

1. Produce reviewed application and website image digests once; verify signatures, attestations, SBOMs, scans and embedded revisions.
2. Record both digests, migration set, Compose revision and intended Catalog version in the release record.
3. Connect over `ssh infiniteocean` from `ubunturojo` or a protected CI environment.
4. Transfer only the reviewed Compose/proxy configuration or update a clean deployment checkout; never copy the developer worktree or environment file.
5. Pull both images by digest.
6. Back up databases and record restore checkpoints.
7. Run global and cell migrations as one-shot Compose jobs with independent migration credentials.
8. Start internal services and workers, verify readiness, then update edge/public services.
9. Publish the reviewed stage Catalog and Stripe test mappings through short-lived operator containers.
10. Run [staging-certification.md](staging-certification.md) and archive content-free evidence outside Git.

Use Compose project names and explicit directories so deployment and rollback cannot target an unrelated VPS stack. Deploy with `docker compose up -d --wait` or an equivalent health-gated sequence, not ad hoc `docker run` commands.

## Stage certification scope

Hostinger stage must cover:

1. anonymous public/private origin boundary;
2. real SMTP identity, Account and passkey journeys;
3. Stripe test-mode success, duplicate, disorder, failure, recovery, Portal, replay and refresh;
4. two-cell isolation, stale placement and Docker-network denial;
5. container restart, process loss, provider outage, database restart and durable queue recovery;
6. backup restore, restore checkpoint fencing and signed directive replay;
7. public/private accessibility and supported-device matrix;
8. bounded load/fairness and stage burn-in.

Stage cannot close Linode/Kubernetes-specific launch gates.

## Linode production deployment

### Inputs required when kubeconfig is furnished

Before mutating the cluster, inspect read-only cluster state and record:

- Kubernetes version, nodes, zones, storage classes and ingress controller;
- namespaces, NetworkPolicy implementation and admission-policy engine;
- certificate, External Secret and metrics-adapter availability;
- monitoring/logging integration and paging route;
- sandbox RuntimeClass support for ephemeral runners;
- cluster API CIDR and approved managed-service/provider egress;
- database endpoints, connection budgets, backup/failover and restore targets.

Do not weaken the reference manifests to fit missing cluster capabilities. Install or choose the required controllers explicitly, then encode the result in the Linode overlays.

### Production overlay

The Linode overlay must supply:

- exact application and website image digests;
- production namespaces, labels, replica floors, resources and connection caps;
- External Secret and certificate references with workload-specific identities;
- public/private Ingress routes, WAF/rate limits and trusted proxy ranges;
- exact database, provider, observability, secret-controller and Kubernetes API egress;
- authenticated monitors, HPA/custom metrics, SLO rules, dashboards and paging;
- tested `spyglass-sandboxed` RuntimeClass and runner node placement;
- restore checkpoints, Catalog/environment identifiers and admission policy verifying workflow identity, signatures, digests and scans.

CI must render and policy-test the Linode pre-production and production overlays before cluster access is used.

### Production promotion

1. Deploy the exact stage-certified digests into a non-customer Linode namespace or internal cohort.
2. Apply forward-compatible production migrations with separate migration credentials.
3. Run Kubernetes-specific NetworkPolicy, workload identity, admission, HPA, node/pod loss, route-key/CA rollover and runner-compromise certification.
4. Confirm every P0 item in [production-readiness-audit.md](production-readiness-audit.md) has current evidence.
5. Obtain engineering, security, product and operations approval.
6. Deploy the same digests with production configuration while keeping traffic limited to internal Accounts.
7. Run non-destructive boundary, health, signature, Catalog, billing-mismatch, isolation and observability checks.
8. Expand to a bounded customer cohort, observe the agreed window, then broaden traffic only if no mismatch, isolation, duplicate-effect, restore or accessibility failure remains.

Production may launch before all later product phases only if incomplete packages and claims are removed from the Catalog, website, entitlements and transports. A full advertised Work/Attention product requires [phase-3-report.md](phase-3-report.md) completion.

## Rollback path

- **Local:** return to the previous digests while preserving test volumes for diagnosis; reset only through the explicit project-scoped reset procedure.
- **Hostinger stage:** retain at least two release records and Compose configurations, switch both images back by digest, and restore data only into quarantine.
- **Linode application:** roll the Account cohort back to previously retained, verified digests whose schema/configuration compatibility is recorded.
- **Database:** prefer forward repair; never improvise destructive down migrations. Restore into quarantine and satisfy replay checkpoints before ingress.
- **Catalog/billing:** republish a prior immutable Catalog and reconcile local/provider state; never authorize directly from Stripe.
- **Placement:** stop or roll back through a new placement generation; never point a router at a guessed cell.
- **Trust:** retain overlapping route keys/CAs until canaries prove new material, then retire it by runbook.

Rollback becomes a launch gate only after the Docker stage and Linode production paths have each been rehearsed with two real retained artifact pairs.
