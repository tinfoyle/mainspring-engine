# Mainspring Engine

Mainspring Engine is a multi-tenant AI operating system for small businesses. A tenant selects a business template—currently trades or software and IT services—which supplies a focused onboarding flow and persona catalog while sharing the same durable boardroom, RAG, email, scheduling, and tool-control runtime.

The application owns orchestration. Agent providers perform bounded turns and are replaceable behind a provider-neutral harness.

## Current implementation status

This repository is under active MVP development. The architecture is recorded in [docs/architecture/README.md](docs/architecture/README.md). The current vertical slice includes tenant registration and hostname routing, separate tenant authentication, structured assisted onboarding, business-aware boardroom personas, detailed owner/admin agent customization, a hybrid to-do and ticket work queue, a tenant-scoped document library with RAG ingestion, encrypted IMAP/SMTP integration, persistent multi-run conversations, recurring schedules, and a durable agent execution platform. Every persona turn is a versioned, idempotent Temporal Activity with structured output, bounded context, capability-checked document retrieval, verified citations, approval-gated email actions, quota accounting, and a short-lived constrained runner container.

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

`make smoke` uses the deterministic mock provider through the same remote runner boundary. To exercise a real Codex CLI invocation, start the worker with `MAINSPRING_RUNNER_PROVIDER=codex`, point the runner controller at the host Codex auth file and CA bundle, temporarily use a single-turn boardroom, and run `make smoke-codex`. This command incurs a real model call and is intentionally not part of the default suite.

The local Docker stack uses the browser-reserved `.localhost` domain for tenant subdomains and publishes its edge on port `8088`:

- Control plane: `http://account.localhost:8088`
- Unified business demo: `http://demo.localhost:8088`
- Temporal UI: `http://localhost:8233`

The former `saas.localhost` address redirects to the unified demo. Its first visit redirects to owner setup; the local-only setup token is `mainspring-local-setup`.

After owner setup, unified onboarding offers trade/field service, SaaS/software, MSP/IT services, and start-from-scratch entry points. A new-business owner then chooses the closest industry context. Existing trade businesses begin with office management, bookkeeping, and dispatch; existing software businesses begin with operations, revenue, and customer success. The startup path asks future-tense validation and launch questions and proposes a lean Launch Room for planning, finance, and customer/market evidence. Existing development tenants can restart onboarding from **Demo tools → Reset onboarding**; reset preserves the owner login, conversations, documents, and connected mailbox.

Once onboarding is complete, **Work** opens the tenant's shared work queue. Owners can add lightweight personal to-dos or structured tickets, assign work to themselves, set priorities and due dates, filter the queue, and advance items through open, in-progress, waiting, and done states. The same record shape retains persona, schedule, boardroom, conversation, and run provenance for automated ticket creation.

The tenant **Agents** section lets owners and administrators create, edit, deactivate, and duplicate specialists. It controls identity, purpose, system instructions, turn order, provider and model overrides, reasoning and sampling preferences, context/output/time/tool ceilings, estimated-cost reservations, response and citation policies, external-action policy, and conditioned tool grants. Future runs snapshot the entire configuration into immutable persona versions; active runs do not change underneath an edit.

`MAINSPRING_TENANT_TEMPLATE` remains a backward-compatible provisioning default. New onboarding records persist the owner's selected `trades` or `software` template and `trades`, `saas`, or `msp` variant; this is tenant configuration, not a separate binary or fork.

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
