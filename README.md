# Mainspring Engine

Mainspring Engine is a multi-tenant AI back office for small, trade-focused businesses. It provisions tenant-specific boardrooms whose specialized personas can deliberate, retrieve business knowledge, use capability-controlled tools, and run on durable schedules.

The application owns orchestration. Agent providers perform bounded turns and are replaceable behind a provider-neutral harness.

## Current implementation status

This repository is under active MVP development. The architecture is recorded in [docs/architecture/README.md](docs/architecture/README.md). The current vertical slice includes tenant registration and hostname routing, separate tenant authentication, seeded boardroom personas, persistent multi-run conversations with follow-ups, durable Temporal runs, SSE conversation updates, recurring interval/cron schedules that open dated conversations, signed persona capabilities, and an idempotent external-action ledger.

## Development prerequisites

- Go 1.26 or newer
- Docker Engine with Docker Compose
- `templ` generation through `go generate`

On the current development machine, run build and Docker commands in the `UbuntuRojo` WSL distribution.

## Common commands

```bash
cp .env.example .env
make generate
make test
make build
make docker-up
make smoke
```

The local Docker stack uses the browser-reserved `.localhost` domain for tenant subdomains and publishes its edge on port `8088`:

- Control plane: `http://account.localhost:8088`
- Demo tenant: `http://demo.localhost:8088`
- Temporal UI: `http://localhost:8233`

The first visit to the demo tenant redirects to owner setup. The local-only setup token is `mainspring-local-setup`.

## Repository layout

```text
cmd/mainspring        Executable entry point
internal              Private application packages
web                   templ components and embedded assets
internal/migrate/sql  Control and tenant database migrations
deploy/docker         Local and VPS Docker definitions
docs/architecture     System overview and ADRs
```

## Security note

Local credential files are ignored. Never add provider tokens, database passwords, session secrets, or OAuth credentials to Git.
