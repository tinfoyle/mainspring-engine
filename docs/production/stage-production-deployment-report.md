# Local, stage and production deployment report

## Operations Console deployment hold

The Operations Console is locally feature-complete but is not part of Stage RC.36. Do not add it to Hostinger Stage or LKE until the product owner authorizes that separate surface. A future deployment requires a dedicated Operations subdomain/origin, separate passkey-only cookie boundary, five unique least-authority PostgreSQL credentials, offline staff-role provisioning, environment-specific monitoring/backups and completion of the gates in [Operations Console operations](operations-console-operations.md). Its customer, public and staff origins must remain distinct.

- Plan date: 2026-08-20
- Stage activation date: 2026-08-21
- Phase 2 application baseline: `b195fae07b264ea4a609e424d6664f0b776eb6a4`
- Registry: GitHub Container Registry (GHCR)
- Source workflow: direct commits to `main`; immutable release tags and digests for deployment
- Environment sequence: `ubunturojo` Docker -> Hostinger Docker stage -> vanilla Linode Kubernetes Engine production
- Production rule: **all application construction in Phase 3 must be complete before release**

## Current connected Stage checkpoint (2026-09-04)

RC.36 hardens Baseline Agent recovery from source revision `803def5a9d2480c0fba895fc5a8d779834274134` and tracked manifest/checkout revision `f145d80c46fbfbf1ab8662b26157e49646dcfdb6`. The application, public UI and private UI are pinned by digest in `deploy/releases/0.3.0-rc.36.env`, carry BuildKit SBOM/provenance attestations, and passed the publisher's high/critical vulnerability and secret scans with zero findings. The provider schema now enforces the same continuing/ready invariants as the domain validator; the API exposes content-free invocation failure codes and preserves the originating run on user messages; and the client follows retry chains across reloads, automatically retries malformed output twice, then offers an explicit retry without duplicating the answer. The RC.35 keyboard behavior and earlier reply/Work reconciliation repairs remain intact. The protected configuration verified before and during deployment. All 48 healthchecked workloads are healthy among 49 running containers; the three retained PostgreSQL ledgers remain at global migration 68 and cell migrations 80/80. Public, login and MCP protected-resource metadata return `200`, and all three live image identities equal the manifest digests. Authenticated browser acceptance reopened the historical 03:38 failed turn, preserved its single user message, received the replacement Agent response at 04:27 and advanced the Baseline to established with its prior Knowledge and Work intact.

This is an authenticated owner-review candidate. It now certifies connected provider-backed Knowledge capture, approved Work creation, failed-output recovery and completion of the adaptive interview for the reviewed fixture. The Your Turn continuation remains a separate acceptance step, and this candidate does not authorize LKE or production deployment.

## Historical connected Stage checkpoint (2026-08-27)

The matched RC.9 Go application, Nuxt public UI and Vue private UI have passed the local exact-artifact gate, were pushed to GHCR, and are deployed by digest to Hostinger Stage. The active checkout is `ebe0c4505d239d484d7540c94880022b98ce2ed8`; the three images were built from source revision `4e9b45e69aa7882ba8cb35eaf11c3185bc1bd529` and are recorded in `deploy/releases/0.3.0-rc.9.env`. RC.9 retains the notification and landing corrections, adds the UbuntuRojo GHCR publisher, and replaces the Features card wall with a Your Turn-led product story. Stage has 47 long-running containers, all 46 healthchecked workloads are healthy, and its retained databases report global migration 61 and cell migrations 79/79.

Public smoke acceptance covers the landing, features, pricing, privacy and affiliate-terms pages; login/signup; all 17 private Vue routes and direct deep links; the privacy-consent API; cell API routing; MCP protected-resource metadata; and HSTS across the three Stage origins. Catalog v3 is published through the governed draft/review/approval path with Stripe test-mode Prices for the $50 monthly team subscription, optional $250 commissioning package and $10/10,000-token starter package. This deployment corrects the shared Caddy routing so the Vue application and its private API families are actually reachable.

RC.9 was produced by the revision-controlled UbuntuRojo publisher. GitHub Actions is now disabled at the repository setting and all workflow YAML has been removed; it is no longer part of verification, publication, or deployment. All three AMD64 OCI indexes carry attached BuildKit SBOM/maximal provenance and passed the pinned Trivy high/critical vulnerability and secret gate with zero findings. They remain unsigned and single-platform, so RC.9 is an explicit Stage review artifact rather than a production-promotable release. LKE and live Stripe remain untouched. Production still requires a trusted operator-signed, multi-architecture complete-product release after the remaining signed-in, provider, browser/accessibility and rollback evidence is complete.

The exact-artifact checkpoint records 363 applicable browser passes and eleven intentional compact-menu profile skips. It includes active and suspended Affiliate dashboard states, proposal-only referral-link handling across unauthenticated sign-in, consent-denied acquisition silence, accurate direct-Checkout entry measurement, passkey-confirmed exact-version public-code replacement with permanent retired-code non-reuse, referred-customer concealment, consent-independent self-referral denial recovery and single-flight browser privacy erasure, while the Go suite explicitly covers lost-dispute reversal and the encrypted signup/verification return handoff. Every private Vue form-bearing surface participates in shared route/unload protection, including one-time recovery codes and unresolved Billing, Checkout, Privacy and Affiliate requests. A dedicated pinned-Chromium project additionally asserts a 1280×900 surface, 320×225 CSS viewport and DPR 4 before certifying the seventeen-route private and public inventories at 400% browser scale. Those checks remain synthetic/adversarial: Affiliate program flags and settlement remain closed, while hosted Stripe and physical-device/assistive-technology journeys remain uncertified. The Stage deployment authorizes human review only, not LKE promotion.

Affiliate-code replacement deployment amendment (2026-08-25): migration 49 and generated API operation 208 are local construction inputs only. They will ride the eventual complete Phase 3 artifact/migration set; there is no standalone Stage or production rollout for this slice. Hostinger Docker, GHCR and LKE remain untouched.

Affiliate-statement deployment amendment (2026-08-25): the locked-subscription aggregate and UTC monthly statement presentation are likewise local Phase 3 construction. They do not authorize an image publication or environment rollout; Hostinger Docker, GHCR and LKE remain untouched.

Affiliate acquisition-continuity deployment amendment (2026-08-25): the same-origin signup/verification and password-recovery/reset `return_to` handoffs plus the corrected private-entry analytics taxonomy are source changes in the local Phase 3 artifact set. They require the normal matched Go/public/private exact-artifact gate and will not be released independently. GHCR, Hostinger Stage and LKE remain untouched.

Affiliate risk-review deployment amendment (2026-08-26): migration 50 and the `affiliate-admin inspect-risk` action are local Phase 3 construction only. They add an execute-only, content-free manual-review boundary and do not enable Affiliate flags, change settlement policy, publish an image or authorize an environment rollout. GHCR, Hostinger Stage and LKE remain untouched.

Affiliate validation-budget deployment amendment (2026-08-26): migrations 51–52 extend the existing distributed network-actor limiter with the Account-scoped Affiliate code-validation scope and bounded stale-row retention through the existing identity-maintenance worker. They are local Phase 3 construction, introduce no new provider or infrastructure dependency, and will move only with the eventual complete product migration set. GHCR, Hostinger Stage and LKE remain untouched.

Owner-policy closure deployment amendment (2026-08-26): launch product policy, including delegated remaining AI Token defaults and the account-credit-first Affiliate model with Support-assisted checks, is complete as a local specification. It does not authorize publication or deployment. The effective Catalog, Stripe mappings, token ledger/admission behavior and exact `account_credit_with_support_check` runtime mode must be implemented and pass UbuntuRojo Docker certification before any matched artifact may move to GHCR, Hostinger Stage or LKE; legal/controller/provider/device and environment evidence remains required afterward.

Local provider preparation is isolated from deployment input. Compose accepts an optional Stripe-only override from a validated mode-600 file on UbuntuRojo's native filesystem; the checked-in environment remains deliberately fake and provider-free. The Hostinger webhook secret is endpoint-specific and must not be reused by a local listener. No Stage secret was copied and no deployed resource was changed. Hosted local Checkout remains pending real Stripe test Prices, governed local Catalog mappings, the listener-issued secret and externally authorized Catalog operations.

UI7 consolidated the release inputs into three matched immutable artifacts built from one source revision: the Go application, public Nuxt UI and private Vue UI. Local Docker certified the exact routed set and those digests now run on Hostinger Stage. Every launch package—including Marketing, Baseline, Work, Knowledge, Agents, Schedules, Finance, Integrations, Account administration, Billing, Security, exports and Account lifecycle—has a generated-boundary Vue route. LKE remains gated on human Stage review, hosted cross-feature product journeys, physical-device/assistive-technology evidence, a signed multi-architecture build and final rollback certification.

## Fixed deployment decisions

| Concern | Decision |
|---|---|
| Local execution | Docker Compose invoked from the `ubunturojo` WSL distribution |
| Stage | Docker Compose on the Hostinger VPS reached through SSH alias `infiniteocean` |
| Production | Shared, managed-control-plane vanilla Linode Kubernetes Engine cluster |
| Registry | GHCR, with immutable digest deployment |
| Application artifact | One multi-mode Go image: `ghcr.io/tinfoyle/spyglass-engine` |
| Public UI artifact | Final target: one standalone Nuxt/Node image, `ghcr.io/tinfoyle/infinite-ocean-public-ui`; existing `infinite-ocean-website` releases remain historical until UI7 cutover |
| Private UI artifact | One static Vue SPA served by its minimal non-root Go runtime, `ghcr.io/tinfoyle/infinite-ocean-private-ui` |
| Databases | Containerized PostgreSQL 17 in every environment |
| Production database operator | CloudNativePG on LKE with LKE block storage and configurable replicas |
| Document objects | Private versioned S3 contract: encrypted MinIO volumes in local/Hostinger stage and managed Linode Object Storage in production |
| Account portability artifacts | Separate private versioned bucket and workload identity: encrypted MinIO locally/on Hostinger, S3-compatible production storage on LKE; no public bucket policy or shared document credential |
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

The existing root `Dockerfile` remains the shared Go image. Process arguments select Account API, router, cell API, workers, brokers, migrations and short-lived operator jobs. The UbuntuRojo publisher creates immutable GHCR images with attached BuildKit SBOM/provenance and pinned Trivy admission; production mode must select AMD64/ARM64 and apply a trusted operator-managed Cosign signature.

Current active Stage review candidate:

| Artifact | Tag | Immutable manifest |
|---|---|---|
| Application | `0.3.0-rc.36` | `ghcr.io/tinfoyle/spyglass-engine@sha256:a2e6d5087f2e33090a0e1a2b11108d57a49a596d52bcbcf7f3e393739b835163` |
| Public UI | `0.3.0-rc.36` | `ghcr.io/tinfoyle/infinite-ocean-public-ui@sha256:8abeccf4cbdc6e95d4de1eb63590d2b929dab28707d563ad6273a526584313c6` |
| Private UI | `0.3.0-rc.36` | `ghcr.io/tinfoyle/infinite-ocean-private-ui@sha256:7a083cb6d31002efa0d92cb1b0c1ca3f8b72ab5b7b3b4ba7e0d67746dc4abda7` |

The RC.36 triple was built from source revision
`803def5a9d2480c0fba895fc5a8d779834274134` and is tracked in
`deploy/releases/0.3.0-rc.36.env`. It passed local AMD64 admission and connected
Stage deployment verification with attached SBOM/provenance. It remains unsigned,
lacks multi-architecture evidence, and still requires remaining authenticated owner acceptance.

RC.2 predates the admission gate. RC.3 is signed but its website image failed the later independent scan with 5 critical and 48 high findings per platform. Both remain immutable history and neither is an approved rollback target. Admitted RC.4 is retained as the RC.5 rollback pair. `git diff --name-only spyglass-v0.2.5-rc.4..spyglass-v0.2.5-rc.5` contains no application, website, migration or Catalog code; the connected environment rehearsal confirmed the expected compatibility.

The historical connected-stage RC.5 -> RC.4 -> RC.5 rehearsal completed successfully against retained PostgreSQL volumes. RC.4 and the restored RC.5 each passed the same 11-check exact-origin/Catalog boundary certificate. RC.5 remains the Phase 2.5 platform rollback baseline; RC.36 is active on Stage, and its expanded schema does not inherit that earlier downgrade proof. The final production release will be a later signed immutable triple; no current candidate is predeclared as that release.

MCP deployment checkpoint (2026-08-22): RC.2 is applied on Hostinger with migrations 33-34, the least-privilege gateway role, generated password/client certificate, public `mcp.stage.infiniteocean.net` ingress and two healthy gateway replicas. The account application origin publishes OAuth metadata and consent/token/revocation endpoints; the MCP origin publishes protected-resource metadata and the Account-scoped Streamable HTTP endpoint. Both discovery documents and the RFC 9728 Bearer challenge pass through public DNS/TLS. A controlled public-edge certificate returned the expected response for 128/128 requests with both replicas, 128/128 with each replica removed in turn and 128/128 after restoration. The remaining activation gate is a signed-in external-client authorization, routed tool call, refresh rotation and revocation certificate; the executable surface is tracked in the package inventory, while that acceptance evidence remains open.

### Public and private UI images

Phase 3 replaces the prototype web surface with a standalone Nuxt/Node public acquisition image and a separately deployable Vue private SPA:

- multi-stage builds with locked dependencies, tests, lint and production builds;
- non-root minimal runtimes;
- runtime-injected exact HTTPS application and Catalog origins;
- server-rendered acquisition, Catalog resilience, security headers and health endpoints in the public image;
- mobile-first Vue navigation and durable generated-boundary routes in the private image; and
- no `.openai/hosting.json`, Wrangler state or Cloudflare release dependency.

The same matched application/public/private digests must move through local validation, Hostinger Stage and LKE production configuration without rebuilding. The final operator release path must supply attached SBOM, BuildKit provenance, a trusted signature and retention evidence; the unsigned single-platform RC.36 Stage artifact does not satisfy that final gate.

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
  publish-stage-release.sh
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
  0.3.0-rc.2.env
  0.3.0-rc.3.env
  0.3.0-rc.4.env
  0.3.0-rc.6.env
  0.3.0-rc.7.env
  0.3.0-rc.8.env
  0.3.0-rc.9.env
deploy/kubernetes/overlays/
  linode-common/
  linode-preproduction/
  linode-production/
website/Dockerfile
website/.dockerignore
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
- private versioned MinIO document/creative storage with server-side encryption, four workload-scoped identities and no host-published port;
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

On 2026-08-21 the website and application stage names resolved to the VPS public address and the shared Caddy routes were activated with the repository's shared security-header policy, including HSTS. On 2026-08-22 the MCP wildcard hostname was activated. On 2026-08-27 `/opt/spyglass-stage/current` advanced to the RC.9 checkout and connected browser acceptance confirmed the Your Turn-led Features story, all twelve detail links, canonical metadata and no horizontal overflow. On 2026-09-03 the selector advanced through RC.30 for the adaptive interview, RC.31 for null-collection compatibility and RC.32 for prompt placement. On 2026-09-04 it advanced through RC.33 for chat interaction and Work-link routing, RC.34 for reply and approved-Work reconciliation, RC.35 for chat keyboard behavior, and then to `/opt/spyglass-stage/releases/f145d80c46fbfbf1ab8662b26157e49646dcfdb6` for RC.36 failed-output recovery. The exact RC.36 application/public/private digest triple is deployed, 49 long-running containers are present, all 48 healthchecked workloads are healthy, the internal edge is running, and the retained global/cell A/cell B PostgreSQL services remain at migrations 68/80/80. Authenticated acceptance recovered the historical failed 03:38 response without a duplicate answer and established the fixture's Baseline; Your Turn continuation remains open.

The provider input remains mode 600 outside Git. Active generated secret set `/opt/spyglass-stage/secrets/2026-08-31-01` passes its mode, identity, certificate and restore-checkpoint verifier while carrying retained database/service credentials and the configured Stage providers. Future additive topology upgrades must supply the previous active environment to `prepare-stage-secrets.sh`; full credential rotation needs coordinated datastore changes. Stripe test access, non-production model access, Stalwart implicit TLS and model-gateway health passed the content-free provider certificate. The historical RC.4 rollback and RC.5 restoration completed without database restoration; that does not prove an RC.30 schema downgrade. Owner-managed backup destinations/schedules and external disk/certificate alerting remain environment operations, as agreed; they are not application-construction blockers.

The required non-secret `SPYGLASS_OPENAI_MODEL_PRICING_JSON` provider input now contains reviewed current prices for every exact model enabled in immutable Personas. Stage secret preparation and the LKE model-gateway Secret fail closed when it is absent or malformed; neither actual prices nor provider credentials belong in Git because prices are environment-reviewed operational input.

### Stage topology

The stage override uses the same service graph with production-mode process arguments:

- application and website images pulled from GHCR by digest;
- three PostgreSQL containers with private networks and persistent volumes;
- private versioned MinIO document/creative storage plus a separate `spyglass-account-exports` bucket on the persistent volume, with a generated static stage encryption key and distinct prefix-scoped admission, document-worker, read-only Integration-connector, one-shot prototype-migration, export-source-reader, artifact-build and artifact-expiry credentials; source can read only exact Knowledge/Marketing originals, build reaches only exact derived artifact keys, expiry has no create/list authority, and production replaces MinIO with corresponding scoped S3-compatible credentials;
- one Account-export build worker per cell with private mode-`0700` staging capped at 1 GiB on the 7.7 GiB synthetic Stage host, plus one separately credentialed global expiry worker; local stress retains 34 GiB tmpfs and LKE uses 34 GiB disk-backed `emptyDir` staging;
- an internal Caddy router on the existing `infiniteocean_public` network; the existing Infinite Ocean Caddy remains the only public 80/443 and ACME owner;
- Stripe test mode, TLS SMTP and non-production provider credentials;
- a non-internal provider-egress network attached only to Account API, billing, notification, model-gateway and the two runner-broker workloads;
- workload-specific secrets/environment files stored outside the repository;
- generated Integration-connector global/cell database credentials plus an independent read-only Marketing-object credential; the Stage render fails closed if any are absent;
- resource limits, health checks, log rotation and content-safe telemetry;
- two stateless, least-privilege MCP gateway replicas behind the public/internal Caddy chain;
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

The RC.2 anonymous boundary certificate passes 11/11 checks at SHA-256 `dd87e48f4c57070afc22f916118b900fe6b2268bd1b7aee73f7d4e6b16e4168d`. The two-replica MCP public-edge load/failover certificate is SHA-256 `5ca33277955531c993b5305e89b3530fb1cd54a4fd29a6e38182e5a8d9ad0de5`; it records the clean 16-concurrency profile and also preserves the rejected 32-concurrency calibration with nine client timeouts. Both mode-600 artifacts are under `/opt/spyglass-stage/evidence/0.3.0-rc.2/`.

The RC.2 Agent dispatch/projection recovery certificate is SHA-256 `1c2da6f0d753d8d64419561e5690a52fcfa818b7a3168403a2e6dc512f62c1e9`. It proves populated/empty inspection for both queues, signed exact requeue, wrong-target, wrong-queue and duplicate rejection, no table authority, immutable audit events, worker-restart reclaim and full synthetic Account erasure. Its temporary roles and credential artifacts were removed; both workers are healthy. Restore replay remains paired with the owner-configured backup game day rather than being simulated from an application-created backup.

The RC.4 Account-export worker certificate is SHA-256
`f6367057000f1346ed58c061280788638257198f336b024e1c25a284b773fa3c`.
It records the same-revision image pair and deployment checkout, three healthy
export workers, UID/GID 65532, read-only roots, the two private 1 GiB mode-0700
build staging mounts, migrations 35/66/66, three positive database-role policy
assertions, the complete positive/negative object policy gate, 45 running
containers, 44 healthy healthchecks, zero unhealthy healthchecks, seven retained
persistent volumes and HTTP 200 at all three public origins. The mode-600,
content-free artifact is
`/opt/spyglass-stage/evidence/0.3.0-rc.4/account-export-workers.json`.

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

## Integration provider-secret placement

The constructed mounted credential broker uses the same workload-private filesystem contract in both environments; see [integration-credential-broker.md](integration-credential-broker.md). Hostinger will mount its owner-managed credential directory read-only into only the per-cell connector workers. LKE will project an environment Secret or secret-store CSI volume into only those workers, with no generic Kubernetes Secret-read RBAC. The approved opaque reference's SHA-256 is stored in Spyglass and is rejoined to provider code inside the execute-only claim; material remains outside PostgreSQL, images, Compose environment files and Git.

This is deployment preparation, not an enablement decision. Stage and production connector workers remain closed until the immutable Marketing object lifecycle, exact email/web adapters, egress policy and applied provider certification are complete. Production promotion remains gated on all Phase 3 construction.

Local Phase 3 OAuth checkpoint (2026-08-23): the Docker environment now creates distinct provider-vault encryption keys at first boot in private named volumes and runs a deterministic Google OAuth/Drive fixture behind `oauth.infiniteocean.localhost`. This is a local backend conformance dependency, not a hosting platform or release service. The application continues to use fixed Google origins outside local fixture mode. Nothing from this checkpoint was pushed to GHCR or applied to Hostinger Stage or LKE; later environment work must furnish owner-managed provider material through the mounted/projected secret contract above and pass the complete-product release gates.

Local Phase 3 Drive checkpoint (2026-08-24): the same fixture is now exercised end to end by the real connector and Knowledge workers. It proves the product-owned OAuth credential can authorize Drive capture, ordinary Knowledge processing can publish the immutable source revision, and revocation prevents subsequent sync without deleting capture history. The local worker database-role correction remains protected by forced Account RLS. This is repository and Docker conformance evidence only: no release image was produced, no commit was pushed, and neither Hostinger Stage nor LKE was contacted or changed. Environment deployment remains deferred until all Phase 3 application construction is complete.

Local Phase 3 email-foundation checkpoint (2026-08-24): the repository now has provider-neutral source claims and purpose-separated `imap` credential leases, backed by a 113th additive cell migration and fresh PostgreSQL evidence. This does not enable an environment connector: the IMAP adapter, deterministic TLS mail fixture and full LB3 capture certificate are still under construction. No image, GHCR, Hostinger or LKE state changed.
