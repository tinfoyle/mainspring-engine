# Local, stage and production deployment report

- Plan date: 2026-08-20
- Stage activation date: 2026-08-21
- Phase 2 application baseline: `b195fae07b264ea4a609e424d6664f0b776eb6a4`
- Registry: GitHub Container Registry (GHCR)
- Source workflow: direct commits to `main`; immutable release tags and digests for deployment
- Environment sequence: `ubunturojo` Docker -> Hostinger Docker stage -> vanilla Linode Kubernetes Engine production
- Production rule: **all application construction in Phase 3 must be complete before release**

## Fixed deployment decisions

| Concern | Decision |
|---|---|
| Local execution | Docker Compose invoked from the `ubunturojo` WSL distribution |
| Stage | Docker Compose on the Hostinger VPS reached through SSH alias `infiniteocean` |
| Production | Shared, managed-control-plane vanilla Linode Kubernetes Engine cluster |
| Registry | GHCR, with immutable digest deployment |
| Application artifact | One multi-mode Go image: `ghcr.io/tinfoyle/spyglass-engine` |
| Website artifact | One standalone Node image: `ghcr.io/tinfoyle/infinite-ocean-website` |
| Databases | Containerized PostgreSQL 17 in every environment |
| Production database operator | CloudNativePG on LKE with LKE block storage and configurable replicas |
| Document objects | Private versioned S3 contract: encrypted MinIO volumes in local/Hostinger stage and managed Linode Object Storage in production |
| Malware scanning | Fail-closed ClamAV `INSTREAM` client over a digest-pinned, unexposed 4 GiB private service with persistent signatures; local/CI freeze signatures and stage enables FreshClam updates |
| Document extraction | Private digest-pinned Apache Tika service with no unsecure features, bounded parser lifecycle/resources and no host port; production replicas remain behind a ClusterIP |
| Document processing, retrieval and deletion | Content-free per-cell PostgreSQL lease queues, Account-scoped PostgreSQL full-text retrieval/exact citations, exact-version manifests/receipts and least-privilege workers; Hostinger uses the `knowledge-processing` Compose profile and LKE will use one scalable worker Deployment per cell boundary |
| Baseline state | Account-owned PostgreSQL cell tables with forced RLS, movement fencing and exact erasure/restore accounting; deployment is delivered by the ordinary cell migration job and adds no public service or alternate datastore |
| Scheduled Agent runtime | One cell-specific `schedule-execution-worker` per cell with an execute-only queue role and SPIFFE client identity; it reaches only admission-api and its exact private app-api |
| Backups | Configured and operated per environment by the project owner after application/database placement; deployment supplies hooks, checkpoints and restore tooling |
| Secrets | Local/stage files outside Git; SOPS/age-encrypted production manifests with private keys outside Git |
| Preview hosting | `.openai/hosting.json`, Sites/Cloudflare preview deployment and `chatgpt.site` are not release targets |

## Chosen origins

| Environment | Public website | Private application | MCP resource server |
|---|---|---|---|
| Local | `https://web.infiniteocean.localhost:8444` | `https://app.infiniteocean.localhost:8444` | `https://mcp.infiniteocean.localhost:8444` |
| Stage | `https://stage.infiniteocean.net` | `https://app.stage.infiniteocean.net` | `https://mcp.stage.infiniteocean.net` |
| Production | `https://www.infiniteocean.net` | `https://app.infiniteocean.net` | `https://mcp.infiniteocean.net` |

`https://infiniteocean.net` redirects to `https://www.infiniteocean.net`. Only the public website, private application, and protected MCP resource origins are internet-facing. Internal cell APIs, databases, brokers, workers, model gateway, runners, metrics and administration jobs remain private.

Cross-cell Account movement uses the same short-lived `account-move-admin` image in Docker stage and Kubernetes production. It is never a standing service and never infers endpoints from DNS. The operator supplies separate global/source/destination credentials and exact cell identities under a signed authorization; see [Account movement operations](account-movement.md).

## Artifact model

### Application image

The existing root `Dockerfile` remains the shared Go image. Process arguments select Account API, router, cell API, workers, brokers, migrations and short-lived operator jobs. The release workflow publishes AMD64/ARM64 GHCR images with attached BuildKit SBOM/provenance and a keyless Cosign signature.

Current active Phase 3 stage candidate:

| Artifact | Tag | Immutable manifest |
|---|---|---|
| Application | `spyglass-v0.3.0-rc.1` | `ghcr.io/tinfoyle/spyglass-engine@sha256:f37b9927870d7f697e639d7becdc6aa5b00bca793a601fac4b74a29873b48253` |
| Website | `website-v0.3.0-rc.1` | `ghcr.io/tinfoyle/infinite-ocean-website@sha256:2bd61f89e9ec049e92bd408fef780cc6c109aa2be99d752a14b39d4aa7e887ab` |

Both were built from `9f87fd2482b77eafe53c832cfde703db027d71a2`, passed the release workflows and are tracked in `deploy/releases/0.3.0-rc.1.env`. Application run `32591754241` and website run `32591754027` completed successfully; the ordinary main verification run `32591550152` also passed. The pair is immutable stage evidence for the completed Scheduling slice, not the final production release.

RC.2 predates the admission gate. RC.3 is signed but its website image failed the later independent scan with 5 critical and 48 high findings per platform. Both remain immutable history and neither is an approved rollback target. Admitted RC.4 is retained as the RC.5 rollback pair. `git diff --name-only spyglass-v0.2.5-rc.4..spyglass-v0.2.5-rc.5` contains no application, website, migration or Catalog code; the connected environment rehearsal confirmed the expected compatibility.

The actual connected-stage RC.5 -> RC.4 -> RC.5 rehearsal completed successfully against retained PostgreSQL volumes. RC.4 and the restored RC.5 each passed the same 11-check exact-origin/Catalog boundary certificate. RC.5 remains the Phase 2.5 platform rollback baseline, while Phase 3 RC.1 is active on stage. The final complete-product release will be a later immutable pair; neither is predeclared as the production release.

### Website image

Phase 2.5 replaces the preview-specific Vinext/Cloudflare runtime with a standalone Next.js Node 22 image:

- multi-stage build with `npm ci`, test, lint and production build;
- non-root minimal runtime;
- runtime-injected, exact HTTPS application and Catalog origins;
- server-side Catalog proxy, security headers and health endpoints;
- no `.openai/hosting.json`, Wrangler state or Cloudflare bindings;
- matching attached SBOM, BuildKit provenance, keyless signing and retention policy.

The same website digest must move through local validation, Hostinger stage and LKE production configuration without rebuilding.

## Revision-controlled deployment structure

```text
deploy/docker/spyglass/
  compose.yml
  compose.local.yml
  compose.stage.yml
  compose.stage-runner.yml
  Caddyfile.local
  Caddyfile.stage
  Caddyfile.hostinger-snippet
  Caddyfile.hostinger-security-headers
  deploy-stage.sh
  verify-stage.sh
  test-stage-contract.sh
  prepare-stage-secrets.sh
  env/local.env
  env/stage.example
  env/stage.providers.example
deploy/releases/
  0.2.5-rc.2.env
  0.2.5-rc.3.env
  0.2.5-rc.4.env
  0.2.5-rc.5.env
  0.3.0-rc.1.env
deploy/kubernetes/overlays/
  linode-common/
  linode-preproduction/
  linode-production/
website/Dockerfile
website/.dockerignore
.github/workflows/
  verify.yml
  release-image.yml
  release-website-image.yml
docs/production/environments/
  local-docker.md
  hostinger-stage.md
  linode-production.md
docs/production/release-artifacts.md
```

No plaintext secret, private key, database URL, session material, provider payload or customer identifier belongs in these artifacts.

## Local Docker environment

### Topology

The common Compose definition models final process boundaries:

- Caddy edge proxy;
- standalone public website;
- global, cell A and cell B PostgreSQL 17 containers with distinct volumes;
- private versioned MinIO document storage with server-side encryption and no host-published port;
- cell app APIs admit routed document uploads through bounded private-disk spools and write only verified immutable source objects; per-cell workers consume the same private bucket for scanning, extraction, indexing and receipt-backed exact-version deletion;
- migration jobs for each database target;
- Account API, app router, two cell app APIs and private admission API;
- notification, billing, entitlement, lifecycle, identity-maintenance, route-receipt, Work reconciliation and per-cell Baseline maintenance workers;
- Agent dispatch/projection in the ordinary local stack, plus a separately certified Docker launcher integration; the complete controller/broker/model/tool-router runner graph is enabled by the Hostinger stage override;
- Mailpit or equivalent SMTP capture;
- optional Prometheus, Grafana and OpenTelemetry profiles.

Local names resolve through the reserved `*.localhost` boundary. Caddy exposes only the two chosen origins; internal service ports remain on Compose networks unless a debugging override explicitly publishes them.

### Containerized test rule

Local certification runs inside Docker, not merely from the WSL host:

1. Go test image runs uncached unit and race suites.
2. PostgreSQL integration image receives a disposable test database URL.
3. Website image runs Node 22 build/test/lint/audit.
4. Contract image runs OpenAPI generation/compatibility checks.
5. Runtime smoke suite traverses Caddy through public/private origins.
6. Two-cell suite proves wrong-Account, wrong-cell, stale-generation and bounded outage behavior.

Host Go/Node commands may remain useful for fast iteration, but they are not release evidence.

### Local lifecycle

- `build`: build application, website and test images.
- `up`: start databases, migrate, then health-gate application services.
- `verify`: run containerized unit/integration/contract/journey tests.
- `down`: stop services while preserving named volumes.
- `reset`: explicitly verify the Compose project name, then remove only its containers, networks and volumes.

The implementation must provide these documented operations through Compose and/or a small Makefile; destructive reset is never the default.

## Hostinger Docker staging

### Verified VPS baseline and active state

The 2026-08-20 read-only inventory reached the configured host from `ubunturojo`: x86-64, 2 vCPU, 7.7 GiB RAM, 96 GiB ext4 with about 89 GiB available, Docker 29.1.3 and Compose 2.40.3. The deployment user is in the Docker group and has non-interactive sudo. Existing Infinite Ocean Caddy and Stalwart containers own ports 80/443 and the mail ports. Spyglass therefore joins the existing `infiniteocean_public` Docker network through its internal stage edge; it does not bind those ports or replace the existing project. Details and commands are in [Hostinger stage](environments/hostinger-stage.md).

On 2026-08-21 both stage names resolved to the VPS public address and the shared Caddy routes were activated with the repository's shared security-header policy, including HSTS. On 2026-08-22 the active clean checkout became `/opt/spyglass-stage/releases/263bb16d5cde51c7ce35ce6d19c7139f1750ee2a`; `/opt/spyglass-stage/current` selects it atomically. The immutable Phase 3 RC.1 images are deployed, 40 long-running containers are present, all 39 healthchecked workloads are healthy, the internal edge is running, and the global/cell A/cell B PostgreSQL services retain their volumes at migrations 53–55.

The provider input remains mode 600 outside Git. Generated secret set `/opt/spyglass-stage/secrets/2026-08-22-02` passes its mode, identity, certificate and restore-checkpoint verifier while carrying retained database/service credentials forward from `2026-08-21-02`. This corrected a failed first upgrade attempt that regenerated passwords already bound into persistent PostgreSQL volumes. Future additive topology upgrades must supply the previous active environment to `prepare-stage-secrets.sh`; full credential rotation needs coordinated datastore changes. Stripe sandbox access and webhook endpoint `we_1U6uGAPokWCfkh4CBSN0SjNI`, non-production OpenAI access, Stalwart implicit TLS and model-gateway health all passed the content-free provider certificate. RC.4 rollback and RC.5 restoration completed without database restoration. Owner-managed backup destinations/schedules and external disk/certificate alerting remain environment operations, as agreed; they are not application-construction blockers.

The required non-secret `SPYGLASS_OPENAI_MODEL_PRICING_JSON` provider input now contains reviewed current prices for every exact model enabled in immutable Personas. Stage secret preparation and the LKE model-gateway Secret fail closed when it is absent or malformed; neither actual prices nor provider credentials belong in Git because prices are environment-reviewed operational input.

### Stage topology

The stage override uses the same service graph with production-mode process arguments:

- application and website images pulled from GHCR by digest;
- three PostgreSQL containers with private networks and persistent volumes;
- private versioned MinIO document storage on a persistent volume with a generated static stage encryption key and distinct prefix-scoped admission, worker and one-shot prototype-migration credentials; production replaces this service with Linode Object Storage and corresponding scoped credentials;
- an internal Caddy router on the existing `infiniteocean_public` network; the existing Infinite Ocean Caddy remains the only public 80/443 and ACME owner;
- Stripe test mode, TLS SMTP and non-production provider credentials;
- a non-internal provider-egress network attached only to Account API, billing, notification and model-gateway workloads;
- workload-specific secrets/environment files stored outside the repository;
- resource limits, health checks, log rotation and content-safe telemetry;
- one mTLS `tool-router`, model gateway, and independent cell A/cell B runner controller, broker and Docker launcher paths;
- internal cell-specific runner networks that contain only the respective broker and launcher; dynamically launched runner containers join only that cell network.

The two stage-only launchers own Docker Engine authority behind narrow mTLS and disjoint bearer-authenticated APIs. Controllers and brokers never mount the Docker socket. Each cell has independent database credentials, encryption/signing keys, launcher tokens, certificates and a runner network. This trusted Docker substrate is never selectable in production; LKE uses Kubernetes Jobs and the sandbox RuntimeClass contract.

### Stage deployment sequence

1. Verify both signed GHCR digests and record them with source revisions.
2. Update a clean deployment checkout or transfer only reviewed Compose/proxy files.
3. Pull images by digest; never build on the VPS.
4. Record database/restore checkpoints and run owner-configured backup hooks when available.
5. Run global and cell migrations using separate one-shot credentials.
6. Provision the dedicated prototype-migration global/cell roles and source-write/extracted-read object policy, but keep the one-shot importer absent unless an approved sealed cohort is scheduled.
7. Start internal services and workers; require readiness.
8. Update router, Account API, website and edge only after internal health passes.
9. Publish the reviewed stage Catalog and Stripe mappings through short-lived operator containers once the Phase 3 Catalog is acceptance-ready.
10. Run exact-origin certification for every platform release and connected customer/provider certification for the Phase 3 product release.
11. Archive the release record and content-free evidence outside Git.

Deployments use an explicit Compose project name and `docker compose up -d --wait` or an equivalent health-gated sequence. They never copy a developer worktree or plaintext secrets.

### Stage evidence

The completed Phase 2.5 Hostinger gate proves:

- public/private origin separation;
- immutable image deployment, Catalog publication at the anonymous boundary and database-preserving release rollback;
- Stripe sandbox/webhook, non-production OpenAI, SMTP TLS and model-gateway readiness;
- process topology, isolated cell runner networks and persistent database placement;
- interruption-free application replica recovery;
- clean-checkout activation with secrets kept outside Git;

The 2026-08-22 Phase 3 Scheduling checkpoint additionally proves both identifier-only queues through real retry/terminal failure, signed inspection, exact requeue, worker restart/reclaim, negative authorization and immutable-audit checks, followed by erasure-fenced fixture cleanup. Its mode-600 certificate is `/opt/spyglass-stage/evidence/0.3.0-rc.1/schedule-queue-rehearsal.json`, SHA-256 `68a3e20c5a445ce21c1bdb33b01178af0b3af209550c2ba345180f2c4b795ae3`.

The final Phase 3 Hostinger release gate additionally proves:

- identity, passkey, Account, Catalog, entitlement and Stripe customer journeys;
- real email delivery and provider behavior through final product surfaces;
- Account-level two-cell routing and database isolation;
- container/database restart and durable queue recovery;
- document processing and physical-deletion replay, including exact-version receipt reconciliation after interruption;
- stage Docker runner execution/reconciliation;
- accessibility/device journeys;
- Docker-level load, fairness, restore fencing and rollback.

It does not prove Kubernetes NetworkPolicy, admission, HPA, Pod identity, RuntimeClass or node-loss behavior.

## Linode Kubernetes production

### Baseline platform

Assume a normal customizable LKE cluster with shared worker nodes and a managed control plane. The planned add-ons are:

- ingress-nginx for public/private ingress;
- cert-manager for ACME certificates;
- CloudNativePG for containerized PostgreSQL;
- metrics-server plus Prometheus/Grafana for metrics and HPA inputs;
- OpenTelemetry Collector for traces;
- SOPS/age for encrypted secret manifests;
- the LKE-supported NetworkPolicy CNI, verified before use;
- a dedicated runner node pool/runtime if the sandbox RuntimeClass requires host customization.

Do not assume these are installed. When kubeconfig is supplied, begin with a read-only inventory of cluster version, nodes/zones, CNI, storage classes, ingress, admission, metrics and existing namespaces/controllers.

### Containerized PostgreSQL

Create three CloudNativePG clusters:

- `spyglass-global-postgres`;
- `spyglass-cell-a-postgres`;
- `spyglass-cell-b-postgres`.

Use LKE block-storage classes, anti-affinity/topology rules, disruption budgets, connection caps and separate migration/runtime/operator roles. Replica counts remain environment-configurable; production values are selected after node/storage capacity review.

The project owner configures backup destinations, schedules and retention after the database/application placement exists. Spyglass supplies consistent checkpoint metadata, signed replay, quarantine restore procedures and readiness fencing. Customer production does not begin until the owner records that the environment backup/restore configuration has been exercised.

### LKE overlays

The credential-free pre-production and production overlays currently provide:

- exact application and website digests;
- isolated application/runner namespaces, ServiceAccounts and restricted Pod security;
- two complete cell workload sets and three CloudNativePG clusters with connection caps/anti-affinity;
- ingress and certificate resources;
- exact NetworkPolicies and provider/database/observability/API egress;
- a private TLS 1.3 `tool-router` Deployment and Service that admits only the exact cell runner-broker workload identities before routing a one-use signed tool context;
- resource requests/limits, connection caps, PDBs and topology spread;
- dedicated runner namespace and permissionless runner ServiceAccount;
- suspended, separately credentialed schema-migration, prototype-migration, Catalog and release-identity Jobs. The prototype Job has no Kubernetes API token, requires an environment-created private `spyglass-prototype-migration` PVC for its sealed bundle/certificate and an absent-by-default `spyglass-prototype-migration-secrets` object, and is never unsuspended as part of ordinary release rollout.

After the kubeconfig and production age recipient are furnished, the Phase 3 cluster inventory must resolve the actual CNI, storage class, API-server CIDR, ingress/add-on namespaces, encrypted workload Secrets, custom-metric adapter, observability backend, sandbox RuntimeClass/node pool and signature/admission controller. Those cluster-bound values are intentionally not fabricated in Phase 2.5.

CI renders and policy-tests both overlays before a deployment credential is used.

## Promotion and production release

1. Preserve the completed Phase 2.5 platform baseline and its evidence.
2. Complete every Phase 3 application, migration and operational criterion.
3. Produce and Hostinger-certify the exact application and website digest pair.
4. Deploy the same pair to a non-customer Linode pre-production namespace.
5. Apply CloudNativePG and application migrations through separate credentials.
6. Run Kubernetes-specific NetworkPolicy, workload identity, admission, autoscaling, pod/node loss, database failover, route-key/CA rollover and runner compromise tests.
7. Record owner verification of environment backup/restore configuration.
8. Complete full-product, provider, accessibility, isolation, load, security/privacy and rollback suites.
9. Promote the same digests to internal production Accounts.
10. Expand to a bounded customer canary, observe the agreed window, then release generally.

There is no reduced-scope production shortcut. All intended application phases must be complete.

## Rollback

- Retain at least two signed application/website digest pairs and their release records.
- Application rollback changes both images to a recorded compatible pair; it never rebuilds or retags.
- Database rollback prefers forward repair. Restore occurs only into quarantine, followed by checkpoint replay and full verification.
- Catalog rollback republishes a prior immutable Catalog and reconciles provider/local state.
- Account movement rollback uses a new placement generation; routers never guess a cell.
- Trust rotation retains overlapping keys/CAs until canaries pass.
- Stage rollback uses the retained Compose/release record; production rollback uses Kubernetes rollout by digest and recorded compatible configuration.

Both Hostinger and LKE rollback paths must be rehearsed before general availability.
