# Spyglass Docker environments

Run these commands from `deploy/docker/spyglass` inside the `ubunturojo` WSL distribution.

The connected Hostinger layout is defined by `compose.stage.yml`; its verified coexistence model and secret/TLS prerequisites are documented in [Hostinger stage](../../../docs/production/environments/hostinger-stage.md). It consumes GHCR digests and deliberately does not bind ports 80/443 because the VPS already has an Infinite Ocean Caddy edge.

Stage deployment takes two inputs: a tracked, non-secret `deploy/releases/<version>.env` binding the application/website digests to their source revision, followed by the mode-600 environment secret file. The verifier rejects image overrides in the secret file.

```bash
make verify
```

The default persistent stack includes Caddy, the website, the global database, two cell databases, a private versioned MinIO object store, one-shot migrations and object-store initialization, local global seed data, account-api, admission-api, two app-api processes, app-router, all global workers, and per-cell Work reconciliation, route-receipt, Agent dispatch, and Agent projection workers. The verification builds and tests the standalone website image, builds the multi-mode Spyglass application image, waits for every persistent dependency, and exercises the public/private origins through Caddy's local CA on HTTPS port `8444` (`8088` is the HTTP redirect listener).

The default topology deliberately sets `SPYGLASS_ENV=development` on its application processes, which permits plain HTTP only on the private Docker network. The separate secure-local gate runs the TLS-sensitive processes outside development in an isolated `spyglass-local-tls` Compose project:

```bash
make verify-workload-tls
```

That gate generates a short-lived local workload CA and exact SPIFFE identities inside the Go test container, serves both cell APIs and admission-api with TLS 1.3, seeds only a dedicated canary Account namespace, and runs the release binary's route canary across app-router identity → cell A, app-router identity → cell B, and cell-A app-api identity → admission-api. Generated keys are ignored and are recreated on every verification. The secure project uses alternate loopback edge and Mailpit ports so it can run alongside the ordinary stack; `make down-workload-tls` stops it while preserving its database volumes.

The separate Docker-runner gate exercises the Hostinger execution strategy against the ubunturojo Docker daemon:

```bash
make verify-docker-runner
```

Only `docker-runner-launcher` receives `/var/run/docker.sock`. The controller and broker use distinct mTLS identities and bearer credentials for lifecycle and identity-verification authority. The gate launches one deterministic non-root, read-only, capability-free fixture on an isolated internal network; verifies its one-invocation token; restarts the launcher; reconciles the same invocation without creating a duplicate; cancels it; and verifies container and identity cleanup. The mutable fixture image and Windows-bind-mount permission accommodation are accepted only by `SPYGLASS_ENVIRONMENT=local-secure`; a real stage launcher requires a digest-pinned image and Unix permission enforcement. Use `make down-docker-runner` to stop this isolated verification project.

`make test` uses a disposable PostgreSQL container, the private versioned MinIO service, and a pinned Go build image. It runs the uncached Go suite (including PostgreSQL and S3-compatible object-store integration tests), race suite, vet, formatting check, OpenAPI generation/registration check, and the website build/render/accessibility/lint/audit gate without host Go or Node installations.

At `app.infiniteocean.localhost`, global/private routes go to account-api while cell-owned Work and Agent API families go through app-router. Browsers never reach a cell API directly.

`env/local.env` contains intentionally public, local-only credentials and deterministic keys. It must never be copied to stage or production. Environment-specific secret files are supplied separately. `prepare-stage-secrets.sh` generates the stage MinIO credentials and static server-side-encryption key; MinIO is never published at a host port, its bucket is initialized with versioning enabled, and its persistent volume survives ordinary stack shutdown. Production uses managed Linode Object Storage rather than this Compose service.

The machine-readable [process inventory](../../spyglass-process-inventory.json) is checked against every executable `spyglass` mode before local verification. It records each process lifecycle, scope, port, health surface, database role family, configuration inputs, and dependencies.

The local role jobs are idempotent and give each serving process a non-superuser, non-owner, non-`BYPASSRLS` login. Migration credentials remain separate. The checked-in role passwords are local-only fixtures, not secret templates.

Mailpit captures local notification traffic at `http://127.0.0.1:8025`. Its SMTP listener is exposed only on `127.0.0.1:1026` and requires implicit TLS. The checked-in CA, leaf certificate, and key are deterministic local test fixtures; the notification worker trusts that CA through the optional custom SMTP root setting. The smoke gate performs a certificate-verified, content-free delivery and confirms it appears in Mailpit.

An optional, digest-pinned Prometheus profile verifies the content-safe metrics surface for every implemented long-running process:

```bash
make verify-observability
```

The gate requires exactly 17 healthy scrape targets—five HTTP services and twelve workers—and exposes the local Prometheus UI only at `http://127.0.0.1:9090`. Its one-hour, tmpfs-backed TSDB is disposable and contains no customer-derived labels.

Use `make down` to stop the stack while preserving all database and object-store volumes. The destructive reset is deliberately explicit:

```bash
make reset CONFIRM=spyglass-local
```
