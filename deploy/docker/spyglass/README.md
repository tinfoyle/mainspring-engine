# Spyglass Docker environments

Run these commands from `deploy/docker/spyglass` inside the `ubunturojo` WSL distribution.

```bash
make verify
```

The default persistent stack includes Caddy, the website, the global database, two cell databases, one-shot migrations, local global seed data, account-api, admission-api, two app-api processes, app-router, all global workers, and per-cell Work reconciliation, route-receipt, Agent dispatch, and Agent projection workers. The verification builds and tests the standalone website image, builds the multi-mode Spyglass application image, waits for every persistent dependency, and exercises the public/private origins through Caddy's local CA on HTTPS port `8444` (`8088` is the HTTP redirect listener).

`make test` uses a disposable PostgreSQL container and pinned Go build image. It runs the uncached Go suite (including PostgreSQL integration tests), race suite, vet, formatting check, OpenAPI generation/registration check, and the website build/render/accessibility/lint/audit gate without host Go or Node installations.

At `app.infiniteocean.localhost`, global/private routes go to account-api while cell-owned Work and Agent API families go through app-router. Browsers never reach a cell API directly.

`env/local.env` contains intentionally public, local-only credentials and deterministic keys. It must never be copied to stage or production. Environment-specific secret files are supplied separately.

The machine-readable [process inventory](../../spyglass-process-inventory.json) is checked against every executable `spyglass` mode before local verification. It records each process lifecycle, scope, port, health surface, database role family, configuration inputs, and dependencies.

The local role jobs are idempotent and give each serving process a non-superuser, non-owner, non-`BYPASSRLS` login. Migration credentials remain separate. The checked-in role passwords are local-only fixtures, not secret templates.

Mailpit captures local notification traffic at `http://127.0.0.1:8025`. Its SMTP listener is exposed only on `127.0.0.1:1026` and requires implicit TLS. The checked-in CA, leaf certificate, and key are deterministic local test fixtures; the notification worker trusts that CA through the optional custom SMTP root setting. The smoke gate performs a certificate-verified, content-free delivery and confirms it appears in Mailpit.

Use `make down` to stop the stack while preserving all three database volumes. The destructive reset is deliberately explicit:

```bash
make reset CONFIRM=spyglass-local
```
