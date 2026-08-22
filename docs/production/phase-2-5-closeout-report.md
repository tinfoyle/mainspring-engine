# Phase 2.5 closeout report

- Plan date: 2026-08-20
- Completion date: 2026-08-21
- Phase 2 application baseline: `b195fae07b264ea4a609e424d6664f0b776eb6a4`
- Repository: `main`, direct commits permitted
- Purpose: realign the cut-short Phase 2 rewrite with the final container-first form before final application construction
- Exit state: **achieved — a coherent platform foundation, reproducible local Docker environment, connected Hostinger Docker stage, and production-ready LKE deployment skeleton**

## Execution ledger

| Slice | Final status | Revision-controlled evidence |
|---|---|---|
| P2.5.0 plan normalization | Complete | This report, [Phase 3](phase-3-report.md), and [deployment](stage-production-deployment-report.md) use the same phase model, environments, origins, GHCR policy, and production-release gate. |
| P2.5.1 website final runtime | Complete | Standard Next.js standalone Node image, runtime origins, server-side Catalog proxy, health endpoints, security headers, and container-only website gate. |
| P2.5.2 local parity Compose | Complete | Persistent global/cell A/cell B PostgreSQL, migrations, two active cells, serving APIs/router, separate runtime roles, containerized Go/race/PostgreSQL/OpenAPI/website tests, and HTTPS smoke verification. |
| P2.5.3 shared process topology | Complete | All implemented global and per-cell workers run with health/status gates and constrained roles; TLS Mailpit capture, the complete machine-readable process inventory, a 17-target Prometheus profile, a dedicated workload-mTLS tool router, and a separate non-development TLS 1.3 topology with exact-identity route canaries are verified. |
| P2.5.4 Docker runner launcher | Complete | A stage-only, mTLS and bearer-authenticated launcher owns the Docker socket; controller and broker have disjoint authority; deterministic constrained containers survive ambiguous creation and launcher restart; cancellation and bounded orphan cleanup are certified against the ubunturojo Docker daemon. |
| P2.5.5 platform closeout | Complete | Durable Account movement, package/surface lifecycle policy, explicit absent-surface gates, billing mismatch explanations, bounded identity retention and the empty external-store handler inventory are implemented and tested. Provider connectivity is live-stage evidence in P2.5.6; customer Stripe and retention-load journeys are final-product evidence in Phase 3, not missing platform architecture. |
| P2.5.6 Hostinger stage | Complete | RC.5 is active from a clean checkout, with 31 long-running containers, three persistent PostgreSQL services, generated workload identities, isolated cell runner paths and the shared Infinite Ocean Caddy edge. Stripe sandbox, the enabled webhook, non-production OpenAI, implicit-TLS SMTP and model-gateway readiness are certified without recording credentials. RC.5 -> admitted RC.4 -> RC.5 completed successfully against the retained databases; both release legs passed the 11-check anonymous boundary certificate, and a single application replica restart served 30/30 public requests without interruption. |
| P2.5.7 LKE skeleton | Complete | Both CI-verified overlays render two cell workload sets, three CloudNativePG clusters, website/ingress/certificate resources, database/provider/observability/API NetworkPolicies, disruption/scaling controls and suspended migration/Catalog/release jobs using the admitted RC.5 digests. Kubeconfig-dependent CNI/storage/API CIDR/add-on, SOPS recipient and sandbox RuntimeClass values remain explicit Phase 3 apply-time gates. |

The local implementation and tests run only through Docker commands issued inside `ubunturojo`. No Unity, OpenAI Sites, Cloudflare deployment, or alternate hosted preview is part of this execution path.

The first successful paired publication is `spyglass-v0.2.5-rc.2` plus `website-v0.2.5-rc.2`, both built from `5ce697933661e5b6d467804ad3608666f9c2dddd`. Its manifest digests remain recorded as immutable history, but RC.2 predates the enforced release-admission scan and is not an approved rollback target.

RC.3 remains signed historical evidence, but its website image failed the subsequent independent Trivy scan with 5 critical and 48 high findings per platform and cannot be promoted or used for rollback. RC.4 is the first admitted pair and is retained in `deploy/releases/0.2.5-rc.4.env`. The active platform candidate is the matched `spyglass-v0.2.5-rc.5` plus `website-v0.2.5-rc.5` pair built from `4bd276c6f96f6e9c4feef811864403c5fa36a1bb`. Its exact manifests are tracked in `deploy/releases/0.2.5-rc.5.env`, pinned into both Linode overlays, bound to per-platform SPDX/SLSA records, independently Cosign-verified and clean under the zero-high/critical/secret admission gate. The source delta from RC.4 to RC.5 changes only release records, environment digest references and documentation—no application, website, migration or Catalog code—and the connected-stage rollback rehearsal confirmed their compatibility.

## Program phase model

| Phase | Meaning |
|---|---|
| Phase 1 | The runnable Mainspring prototype preserved under `prototype/`; it is the behavioral reference, not production architecture. |
| Phase 2 | The production Spyglass rewrite completed so far: identity, Accounts, Catalog, entitlements, billing boundaries, global/cell routing, Work and Agent foundations, migrations, release image and Kubernetes reference. The run ended before the platform and deployment form were complete. |
| Phase 2.5 | This realignment phase. Finish interrupted platform foundations, remove preview-specific architecture, make Docker the local/stage execution model, and prepare the final LKE form. |
| Phase 3 | The complete remaining application construction, prototype parity, production hardening, LKE deployment and launch. Production does not ship with deferred application phases or unpublished substitute scope. |

“P2.5 entitlement engine” in the historical delivery plan is a work-package identifier inside the old Phase 2 structure. It is not this program-level Phase 2.5.

## Phase 2.5 objectives

1. Make the repository's default development and integration path run in Docker from `ubunturojo`.
2. Replace the Cloudflare/OpenAI preview website runtime with a standard container runtime.
3. Create one shared Compose topology with local and Hostinger stage overrides.
4. Publish immutable application and website images to GHCR.
5. Make workload behavior explicit across Docker stage and Kubernetes production, including runner execution.
6. Finish the Phase 2 platform capabilities that Phase 3 must safely build upon.
7. Produce renderable, policy-tested vanilla LKE overlays before cluster credentials are needed.
8. Bring connected Hostinger staging online and leave Phase 3 with no architectural rework debt.

## Non-negotiable final-form decisions

- Public website and private application are separate origins and separate images.
- The public website is a normal Node container; `.openai/hosting.json`, the Sites plugin and `chatgpt.site` are not release inputs.
- The Go application remains one multi-mode image selected by process arguments.
- PostgreSQL is containerized in every environment: three local/stage services and containerized production clusters on LKE.
- Local and stage use Docker Compose. Production uses vanilla Linode Kubernetes Engine.
- GHCR is the registry; deployment uses immutable digests, never mutable tags.
- Direct commits to `main` are acceptable; release publication still requires an immutable tag/version and recorded digest.
- All application phases must be complete before production release.
- Environment backup execution is owned by the project owner after the application deployments exist; application/deployment code must expose restore checkpoints and documented backup hooks.

## Workstream A — canonical plans and configuration

1. Consolidate the historical Phase 3-8 backlog into the new final Phase 3 plan.
2. Update the older delivery/readiness documents so they no longer imply that incomplete packages may be deferred for production.
3. Create a machine-readable inventory of every process mode, port, database role, environment value, health endpoint and dependency.
4. Create one environment schema with local, stage and production validation; unknown or missing production values fail closed.
5. Remove preview-hosting language and generated line-ending churn from the website in a dedicated, reviewed change.

Exit evidence: documentation, runtime configuration validation and deployment manifests describe the same process/dependency graph.

## Workstream B — containerize the website

Migrate `website/` from the Cloudflare/Vinext preview runtime to standard Next.js on Node 22:

- use a production standalone build and non-root runtime image;
- replace the Cloudflare Worker Catalog proxy with a server-side route handler;
- preserve security headers, exact-origin validation, bounded response handling and cache policy;
- replace Cloudflare image bindings with normal static/image behavior;
- move the private-app handoff and Catalog upstream to validated runtime configuration so one image can be promoted unchanged;
- add `/health/live` and `/health/ready` endpoints;
- exclude source, preview metadata, Wrangler state and credentials from the runtime image;
- run build, rendered-route, accessibility, lint and dependency checks inside the image pipeline.

Exit evidence: one website digest runs unchanged in local, stage and production configuration.

## Workstream C — local Docker parity environment

Add a shared Compose stack under `deploy/docker/spyglass/` with local overrides. It must include:

- Caddy edge proxy;
- public website;
- global, cell A and cell B PostgreSQL 17 services with separate volumes;
- one-shot global/cell migration jobs;
- `account-api`, `app-router`, one `app-api` per cell and private `admission-api`;
- billing, notification, entitlement, Account lifecycle, route-receipt and Work reconciliation workers;
- optional Agent/runner/model profiles until their tests need them;
- local SMTP capture and optional local observability services;
- workload-specific environment files, networks, database users and health checks.

Add containerized verification commands for:

- uncached Go tests and race tests;
- PostgreSQL integration tests with `SPYGLASS_POSTGRES_TEST_URL` set;
- website build/test/lint/audit under Node 22;
- OpenAPI compatibility and generation checks;
- migration, two-cell routing, stale-placement and wrong-Account certification;
- Compose health and exact-origin smoke journeys.

The normal shutdown path preserves volumes. A separately named reset command may delete only the verified Spyglass local Compose project and its volumes.

Exit evidence: a clean checkout in `ubunturojo` reaches a healthy two-cell local system through documented Docker commands without using host Go or Node runtimes.

## Workstream D — Docker-stage runner execution

The current runner controller is Kubernetes-only. Phase 2.5 must add an explicit stage strategy rather than pretending the Kubernetes adapter works under Compose.

Implement a stage-only Docker runner launcher behind a narrow authenticated service boundary:

- only the launcher service receives Docker Engine authority;
- `runner-controller` calls a bounded launch/get/cancel/reconcile API and never mounts the Docker socket;
- launched runners use immutable digests, read-only filesystems, no database/provider credentials, bounded CPU/memory/time and an isolated network path to the broker only;
- operation identity, encrypted exchange, one-use capability and result semantics remain identical to Kubernetes;
- the Docker launcher is impossible to select in production configuration;
- stage failure tests cover uncertain create, duplicate launch, cancellation, launcher restart and orphan cleanup.

LKE remains the authoritative production runner isolation environment; Docker staging proves application semantics and recovery.

## Workstream E — finish interrupted Phase 2 foundations

### Durable Account movement

Implement the full resumable move workflow, not a test-only generation change:

1. durable globally owned move record, lease, phase, attempts and immutable events;
2. destination capacity validation and source drain/freeze policy;
3. initial Account-scoped copy for rows and enabled external stores;
4. durable change capture and high-water mark;
5. reconciliation of rows, objects, search state, workflows, entitlements and usage;
6. compare-and-swap placement-generation switch;
7. destination resume and source rollback window;
8. rollback through another generation switch;
9. verified source retirement after policy permits;
10. inspect/pause/resume/rollback operator commands and crash-boundary tests.

Current evidence: [Account movement operations](account-movement.md) documents the implemented durable state machine. Global placement and cell checkpoints, queue-gated freeze, schema-driven PostgreSQL copy, snapshot high-water mark, row manifest/content digest reconciliation, generation-safe switch/rollback, retained rollback copy, source retirement and signed operator commands are covered by disposable three-database and crash-restart tests. No external Account store is enabled yet; Phase 3 must extend the handler inventory before introducing one.

### Commercial and lifecycle completion

- Maintain the accepted [package-by-surface lifecycle matrix](package-surface-lifecycle.md), including explicit absent-state gates for MCP, schedules, connectors and external stores that Phase 3 has not introduced.
- Enforce it through every implemented HTTP, UI, worker, runner and Agent boundary; Phase 3 must register MCP, schedule and connector surfaces before enabling them.
- Keep scheduled identity-retention thresholds as a Phase 3 connected stage-load certification. The restore-gated least-privilege worker, bounded pruning, content-free metrics/status, passkey rename and atomic compromised-credential/all-session response are implemented.
- Preserve the implemented billing mismatch explanations. Phase 2.5 proves Stripe sandbox/webhook readiness; Phase 3 completes test-mode policy journeys through the final Catalog and product surfaces.
- Treat the explicit zero-enabled-external-store inventory as the Phase 2.5 acceptance state. Phase 3 cannot enable an external store until movement, export, erasure, acknowledgement, attestation and restore handlers are registered.
- Maintain the completed Catalog/package/surface inventory and sanitized API publication policy as Phase 3 adds surfaces.

Exit evidence: all commercial/platform foundations are complete enough that Phase 3 adds product capabilities without revisiting identity, Account, entitlement, billing, placement or lifecycle architecture.

## Workstream F — Hostinger connected staging

Use the `infiniteocean` SSH target and the stage Compose override to deploy:

- `stage.infiniteocean.net` for the public website;
- `app.stage.infiniteocean.net` for the private application;
- GHCR application and website images pinned by digest;
- containerized global/cell PostgreSQL with persistent storage;
- TLS, Stripe test mode, TLS SMTP, non-production provider credentials and content-safe telemetry;
- off-host backup hooks and restore checkpoint records for the owner-managed backup phase.

Phase 2.5 certifies the connected platform boundary: exact origins, provider credentials and TLS, immutable releases, container health/recovery and database-preserving rollback. The final customer email/passkey/Stripe journeys, two-cell product isolation, accessibility/device matrix, product load/fairness, provider degradation and restore fencing require the completed Phase 3 Catalog and product surfaces and are therefore Phase 3 release evidence. Hostinger does not certify Kubernetes-only controls.

Exit evidence: an exact release pair is repeatably deployable and rollbackable on the VPS without copying a developer worktree or plaintext secrets; live provider readiness is proven without creating customer or billing records.

## Connected Hostinger closure evidence

The active `/opt/spyglass-stage/current` link selects clean checkout `d127a4c7159b20329412b55436e0db4a98e0dfeb`; the deployed application remains the immutable RC.5 pair. The generated secret set is `/opt/spyglass-stage/secrets/2026-08-21-02`. Operational evidence is mode 600 outside Git, while its content-free identity is recorded here:

| Evidence | Result | SHA-256 |
|---|---|---|
| RC.4 anonymous boundary | 11/11 checks passed during rollback | `b36353fec16fd5220cd2094a6f4e70374ef3d6914ccc2930ee608e7f8f877b92` |
| RC.5 anonymous boundary after restoration | 11/11 checks passed | `83d680e9a215ed47cb943d32157ac817c91e35fa6ab64dae604f61e041af1d3f` |
| RC.5 application replica restart | 30/30 HTTP 200; restarted replica healthy | `8201f58712c0752aa42c5faeb5a09308aa34eb87f1593835f4130a95f4ec95cf` |
| RC.5 provider readiness | Stripe sandbox/webhook, OpenAI, SMTP TLS and model gateway passed | `73a53bcbd690685565fad59c6da7aa43e216bbf72e927c183d5a057c45ee529d` |

The stage database was deliberately left with zero users, Accounts, active provider-price mappings, billing profiles and subscriptions. Creating the final signed Catalog mappings and synthetic/customer journeys before Phase 3 would certify a temporary product surface and weaken the operator-authorization boundary. Those records and tests are created through the Phase 3 release process instead.

Post-closeout Phase 3 note (2026-08-22): the Phase 2.5 closure evidence above remains the historical RC.5 baseline. Current Stage has advanced to admitted Phase 3 RC.2 from clean checkout `773f3c45cc3fb351aec44d6e52a6d279d3b5d4bb`, secret set `2026-08-22-03` and 42 long-running containers, including two healthy MCP gateway replicas. This does not reopen Phase 2.5 or redefine its scope; the RC.2 external-client consent/tool/revocation certificate and later complete-product journeys belong to Phase 3.

## Workstream G — LKE production skeleton

Before the kubeconfig is furnished, create and CI-render:

- `linode-preproduction` and `linode-production` Kustomize overlays;
- ingress-nginx, cert-manager, metrics-server, observability and SOPS/age secret integration assumptions;
- CloudNativePG-based global, cell A and cell B database resources with configurable replica counts and LKE block storage;
- NetworkPolicies, service accounts, Pod security, disruption budgets, topology spread and connection caps;
- dedicated runner namespace/node policy and a required sandbox RuntimeClass contract;
- application and website digest injection;
- restore checkpoint, migration, Catalog and release-record jobs.

Cluster-specific values remain fail-closed placeholders until a read-only kubeconfig audit confirms actual CNI, storage, Kubernetes API endpoint, ingress and node/runtime capabilities. SOPS/age recipients and encrypted Secret payloads are created only after the owner selects the production recipient. Backups are configured by the project owner after the database/application topology is applied; the overlay leaves explicit backup/restore checkpoints and job boundaries.

Exit evidence: both overlays render and pass repository policy checks without credentials or mutable images.

## Phase 2.5 execution order

1. **P2.5.0 — plan normalization:** align the three phase/deployment documents and mark older backlog semantics for consolidation.
2. **P2.5.1 — website final runtime:** migrate and publish the container-ready website locally.
3. **P2.5.2 — local parity Compose:** databases, migrations, minimum request path, containerized test runner and health gates.
4. **P2.5.3 — full shared process topology:** workers, two cells, TLS/workload configuration and observability.
5. **P2.5.4 — Docker runner launcher:** stage-only execution and recovery parity.
6. **P2.5.5 — platform closeout:** Account movement, downgrade/lifecycle, identity and billing gaps.
7. **P2.5.6 — Hostinger stage:** digest deployment, provider connections, certification and rollback.
8. **P2.5.7 — LKE skeleton:** production overlays, controller contracts and policy verification.

## Completed first slice record

Phase 2.5 began with the following bounded P2.5.1 slice, which is now complete:

1. remove Sites/Cloudflare build coupling from the website;
2. add a standard Node 22 standalone production build;
3. add the website Dockerfile, health endpoints and runtime-origin configuration;
4. run the complete website suite inside Docker from `ubunturojo`;
5. add a minimal Compose path containing edge, website and the development Spyglass process only as a visual/request smoke gate;
6. preserve the current public pages, Catalog fallback and private signup handoff behavior.

P2.5.2 subsequently expanded that path to persistent global/two-cell PostgreSQL and production process modes. P2.5.3 completed the implemented worker topology, constrained runtime roles, TLS SMTP capture, local metrics profile, and secure-local workload-identity certification. P2.5.4 added the explicit Docker-stage runner substrate and proved create ambiguity, duplicate launch, identity verification, launcher restart, cancellation, and cleanup through a live Docker Engine integration gate. P2.5.5 then closed Account movement, lifecycle/surface inventory, billing explanation and identity-retention architecture. P2.5.7 completed the credential-free LKE skeleton. P2.5.6 then activated and certified connected Hostinger staging, completing Phase 2.5.

## Phase 2.5 completion rule

Phase 2.5 is complete only when:

- a clean checkout builds and tests entirely through Docker in `ubunturojo`;
- local and Hostinger stage use the same application topology and immutable GHCR images;
- the website has no preview-host dependency;
- durable Account movement and commercial/lifecycle foundations are accepted;
- connected platform certification and database-preserving stage rollback are repeatable;
- LKE overlays render and enforce the intended final boundaries; and
- Phase 3 can concentrate on completing the application rather than changing platform or deployment architecture.

All seven conditions are satisfied as of 2026-08-21. Full customer/provider/product certification remains a Phase 3 production-release condition, not deferred Phase 2.5 work.
