# Mainspring Engine

Mainspring Engine is a multi-tenant AI operating system for small businesses. A tenant selects a business template—currently trades or software and IT services—which supplies a focused onboarding flow and persona catalog while sharing the same durable boardroom, RAG, email, scheduling, and tool-control runtime.

The application owns orchestration. Agent providers perform bounded turns and are replaceable behind a provider-neutral harness.

## Current implementation status

This repository is under active MVP development. The architecture is recorded in [docs/architecture/README.md](docs/architecture/README.md). The current vertical slice includes tenant registration and hostname routing, separate tenant authentication, structured assisted onboarding, business-aware boardroom personas, a hybrid to-do and ticket work queue, a tenant-scoped document library with RAG ingestion, an encrypted IMAP/SMTP mailbox integration, persistent multi-run conversations with follow-ups, durable Temporal runs, SSE conversation updates, recurring interval/cron schedules that open dated conversations, signed persona capabilities, and an idempotent external-action ledger.

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
- Trade demo: `http://demo.localhost:8088`
- Software company demo (SaaS or MSP): `http://saas.localhost:8088`
- Temporal UI: `http://localhost:8233`

The first visit to either demo tenant redirects to owner setup. The local-only setup token is `mainspring-local-setup`. The repeatable software smoke flows create `founder@example.test` with password `correct-horse-battery-staple` when needed.

After owner setup, a new tenant enters the six-step assisted onboarding flow. Trade tenants begin with office management, bookkeeping, and dispatch and can add focused trade specialists. Software tenants begin with operations, revenue, and customer success. SaaS businesses can add product, engineering, reliability, growth, sales, UX research, and website conversion roles; MSPs can add service delivery, technical account management, cloud and systems, and security roles. Existing development tenants can restart onboarding from **Demo tools → Reset onboarding**; reset preserves the owner login, conversations, documents, and connected mailbox.

Once onboarding is complete, **Work** opens the tenant's shared work queue. Owners can add lightweight personal to-dos or structured tickets, assign work to themselves, set priorities and due dates, filter the queue, and advance items through open, in-progress, waiting, and done states. The same record shape retains persona, schedule, boardroom, conversation, and run provenance for automated ticket creation.

Set `MAINSPRING_TENANT_TEMPLATE=trades` or `MAINSPRING_TENANT_TEMPLATE=software` when provisioning a tenant. The former `saas` value remains a compatibility alias. This is tenant configuration, not a separate binary or fork.

The tenant **Documents** section uploads, indexes, lists, and displays text-native business documents. The MVP accepts TXT, Markdown, CSV/TSV, JSON, XML, HTML, YAML, and LOG files up to 2 MB. PDF and Word extraction are planned as a separate ingestion stage.

The tenant **Email** section verifies and stores encrypted IMAP/SMTP settings, displays the inbox and message content, and sends owner-confirmed email through the idempotent outbound ledger. The development stack uses a visible mail simulator; production defaults to the network connector.

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
