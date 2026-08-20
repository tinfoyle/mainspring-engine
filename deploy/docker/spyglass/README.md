# Spyglass Docker environments

Run these commands from `deploy/docker/spyglass` inside the `ubunturojo` WSL distribution.

```bash
make verify
```

The default persistent stack includes Caddy, the website, the global database, two cell databases, one-shot migrations, local global seed data, account-api, admission-api, two app-api processes, and app-router. The verification builds and tests the standalone website image, builds the multi-mode Spyglass application image, waits for every persistent dependency, and exercises the public/private origins through Caddy's local CA on HTTPS port `8444` (`8088` is the HTTP redirect listener).

`make test` uses a disposable PostgreSQL container and pinned Go build image. It runs the uncached Go suite (including PostgreSQL integration tests), race suite, vet, formatting check, OpenAPI generation/registration check, and the website build/render/accessibility/lint/audit gate without host Go or Node installations.

At `app.infiniteocean.localhost`, global/private routes go to account-api while cell-owned Work and Agent API families go through app-router. Browsers never reach a cell API directly.

`env/local.env` contains intentionally public, local-only credentials and deterministic keys. It must never be copied to stage or production. Environment-specific secret files are supplied separately.

Use `make down` to stop the stack while preserving all three database volumes. The destructive reset is deliberately explicit:

```bash
make reset CONFIRM=spyglass-local
```
