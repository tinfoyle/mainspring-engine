# Local, stage and production deployment report

- Plan date: 2026-08-20
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
| Backups | Configured and operated per environment by the project owner after application/database placement; deployment supplies hooks, checkpoints and restore tooling |
| Secrets | Local/stage files outside Git; SOPS/age-encrypted production manifests with private keys outside Git |
| Preview hosting | `.openai/hosting.json`, Sites/Cloudflare preview deployment and `chatgpt.site` are not release targets |

## Chosen origins

| Environment | Public website | Private application |
|---|---|---|
| Local | `https://web.infiniteocean.localhost:8444` | `https://app.infiniteocean.localhost:8444` |
| Stage | `https://stage.infiniteocean.net` | `https://app.stage.infiniteocean.net` |
| Production | `https://www.infiniteocean.net` | `https://app.infiniteocean.net` |

`https://infiniteocean.net` redirects to `https://www.infiniteocean.net`. Only the public website and private application origins are internet-facing. Internal APIs, databases, brokers, workers, model gateway, runners, metrics and administration jobs remain private.

Cross-cell Account movement uses the same short-lived `account-move-admin` image in Docker stage and Kubernetes production. It is never a standing service and never infers endpoints from DNS. The operator supplies separate global/source/destination credentials and exact cell identities under a signed authorization; see [Account movement operations](account-movement.md).

## Artifact model

### Application image

The existing root `Dockerfile` remains the shared Go image. Process arguments select Account API, router, cell API, workers, brokers, migrations and short-lived operator jobs. The release workflow publishes AMD64/ARM64 GHCR images with attached BuildKit SBOM/provenance and a keyless Cosign signature.

Current reviewed stage-candidate pair:

| Artifact | Tag | Immutable manifest |
|---|---|---|
| Application | `spyglass-v0.2.5-rc.3` | `ghcr.io/tinfoyle/spyglass-engine@sha256:fec4024a815472c95d74f08c7dcf41b75452cc80028b8e012f53d5b85fb9469d` |
| Website | `website-v0.2.5-rc.3` | `ghcr.io/tinfoyle/infinite-ocean-website@sha256:abbfc3d7753299c6f81b07b6bf18019bd6e3e8cb7b160705c51162d624754a08` |

Both were built from `90fa6730e94b94b3432561afd63a1fd9a6b4fba0`, completed their keyless Cosign steps, expose attached per-platform SPDX/SLSA attestations, and passed an authenticated digest pull, application identity/tool-router fail-closed check and website readiness check from `ubunturojo`. The exact pair is tracked in `deploy/releases/0.2.5-rc.3.env`. RC.2 is retained as the first successful signed publication and rollback-history candidate.

Remaining artifact work:

- add vulnerability/secret scan evidence to the release record;
- certify RC.2/RC.3 schema, Catalog and configuration compatibility through an actual stage rollback rehearsal.

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
- migration jobs for each database target;
- Account API, app router, two cell app APIs and private admission API;
- notification, billing, entitlement, lifecycle, identity-maintenance, route-receipt and Work reconciliation workers;
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

### Verified VPS baseline and remaining checks

The 2026-08-20 read-only inventory reached the configured host from `ubunturojo`: x86-64, 2 vCPU, 7.7 GiB RAM, 96 GiB ext4 with about 89 GiB available, Docker 29.1.3 and Compose 2.40.3. The deployment user is in the Docker group and has non-interactive sudo. Existing Infinite Ocean Caddy and Stalwart containers own ports 80/443 and the mail ports. Spyglass therefore joins the existing `infiniteocean_public` Docker network through its internal stage edge; it does not bind those ports or replace the existing project. Details and commands are in [Hostinger stage](environments/hostinger-stage.md).

The RC.3 release-record revision `ac7bb50821033bebd843397b9e2126c1e63ae54d` is prepared as a clean detached checkout at `/opt/spyglass-stage/releases/ac7bb50821033bebd843397b9e2126c1e63ae54d`; `/opt/spyglass-stage/secrets` exists, is empty, is owned by the deployment user and is mode 700. No `current` link, container or live edge configuration was changed. DNS still does not return addresses for `stage.infiniteocean.net` or `app.stage.infiniteocean.net`. Before first deployment, the owner/environment work is therefore explicit:

- create both DNS records and confirm firewall/certificate monitoring policy;
- create a mode-600 provider-input file outside the checkout, generate the non-overwritable stage environment and workload certificates with `prepare-stage-secrets.sh`, and select/activate the reviewed checkout only after verification;
- backup destination hooks and disk/certificate monitoring.

Do not modify or restart unrelated VPS services during inventory.

### Stage topology

The stage override uses the same service graph with production-mode process arguments:

- application and website images pulled from GHCR by digest;
- three PostgreSQL containers with private networks and persistent volumes;
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
6. Start internal services and workers; require readiness.
7. Update router, Account API, website and edge only after internal health passes.
8. Publish the reviewed stage Catalog and Stripe mappings through short-lived operator containers.
9. Run exact-origin and connected customer/provider certification.
10. Archive the release record and content-free evidence outside Git.

Deployments use an explicit Compose project name and `docker compose up -d --wait` or an equivalent health-gated sequence. They never copy a developer worktree or plaintext secrets.

### Stage evidence

Hostinger stage proves:

- public/private origin separation;
- identity, passkey, Account, Catalog, entitlement and Stripe journeys;
- real SMTP and provider behavior;
- two-cell routing and database isolation;
- container/database restart and durable queue recovery;
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
- suspended, separately credentialed migration/Catalog/release-identity Jobs.

After the kubeconfig and production age recipient are furnished, the Phase 3 cluster inventory must resolve the actual CNI, storage class, API-server CIDR, ingress/add-on namespaces, encrypted workload Secrets, custom-metric adapter, observability backend, sandbox RuntimeClass/node pool and signature/admission controller. Those cluster-bound values are intentionally not fabricated in Phase 2.5.

CI renders and policy-tests both overlays before a deployment credential is used.

## Promotion and production release

1. Complete every Phase 2.5 exit criterion.
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
