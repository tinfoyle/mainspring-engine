# Mainspring Engine

Mainspring Engine is a multi-tenant AI operating system for small businesses. A tenant selects a business template—currently trades or software and IT services—which supplies a focused onboarding flow and persona catalog while sharing the same durable boardroom, RAG, email, scheduling, and tool-control runtime.

The application owns orchestration. Agent providers perform bounded turns and are replaceable behind a provider-neutral harness. External user agents can operate the same tenant workspace through the authenticated [MCP surface](docs/mcp.md). Public-web research uses the same capability broker for boardroom agents and MCP clients, with self-hosted Firecrawl and SearXNG isolated behind private Docker networks.

## Current implementation status

This repository is under active MVP development. The architecture is recorded in [docs/architecture/README.md](docs/architecture/README.md). The current vertical slice includes tenant registration and hostname routing, separate tenant authentication, a conversational documented-baseline onboarding, business-aware boardroom personas, detailed owner/admin agent customization, a parent/child ticket work queue, autonomous agent-owned ticket dispatch, a unified **Your turn** queue for owner input, completed-work review, and consequential approvals, a tenant-scoped document library with RAG ingestion, scoped read-only email and Google Drive evidence connectors, persistent multi-run conversations, recurring schedules, and a durable agent execution platform. Every persona turn is a versioned, idempotent Temporal Activity with structured output, bounded context, capability-checked document and web retrieval, verified citations, approval-gated external actions, quota accounting, and a short-lived constrained runner container.

## Development prerequisites

- Go 1.26 or newer
- Docker Engine with Docker Compose
- `templ` generation through `go generate`

On the current development machine, run build and Docker commands in the `UbuntuRojo` WSL distribution.

## Common commands

```bash
cp .env.example .env
make generate
make test-fast
make build
make docker-up
make test-full
```

`make test-full` creates a fresh, isolated Compose project on port `18088`, runs the complete deterministic application suite, and removes its test database afterward. It includes a document-recall contract that attaches one document to a boardroom conversation, retrieves its evidence, excludes a decoy, and recalls the attachment again on a follow-up. See [docs/testing.md](docs/testing.md) for suite coverage and debugging controls.

`make smoke` runs the deterministic black-box checks against an already-started development stack. The Docker development stack uses `MAINSPRING_RUNNER_PROVIDER=codex` by default so inherited agents are real; the full test suite explicitly overrides it with `mock`. To exercise a focused real Codex CLI invocation, point the runner controller at the host Codex auth file and CA bundle, temporarily use a single-turn boardroom, and run `make smoke-codex`. This command incurs a real model call and is intentionally not part of the deterministic suite.

The local Docker stack uses the browser-reserved `.localhost` domain for tenant subdomains and publishes its edge on port `8088`:

- Control plane: `http://account.localhost:8088`
- Unified business demo: `http://demo.localhost:8088`
- Unified demo MCP: `http://demo.localhost:8088/mcp`
- Temporal UI: `http://localhost:8233`

Firecrawl, Playwright, SearXNG, Redis, RabbitMQ, and the Firecrawl queue database publish no host ports. The tenant runtime and Temporal worker reach only the private Firecrawl API; ephemeral agent runners do not join any research network. The development Compose stack enables web research automatically. See [ADR-0023](docs/architecture/adr/0023-self-hosted-web-research.md) for the trust boundary and production requirements.

The former `saas.localhost` address redirects to the unified demo. Its first visit redirects to owner setup; the local-only setup token is `mainspring-local-setup`.

After owner setup, unified onboarding offers trade/field service, SaaS/software, MSP/IT services, and start-from-scratch entry points. The primary path is an interview with Mia that records confirmed business facts, uses the stated industry, services, team size, and concern to select an explainable business-specific evidence scope, accepts immediately available uploads, supplements them with visible public research, and then works through each applicable record conversationally—one topic at a time. Software companies receive product, release, security, privacy, resilience, subscription, and customer-agreement topics rather than trade licensing or technician-closeout prompts; field-service, professional-service, retail, and general businesses receive their corresponding operating scopes. Plain-language answers become verified evidence or a locate, search, create, obtain, or not-applicable follow-up with an accountable owner. The owner reviews the resulting summary before approving one parent baseline ticket and its child work items. Email and Google Drive are deliberately deferred to optional post-onboarding integrations. Existing development tenants can restart onboarding from **Demo tools → Reset demo account**; reset preserves the owner login, conversations, and connected source credentials while clearing the Work queue and ticket proposals, removing uploaded/indexed documents, and archiving the prior baseline for history.

Once onboarding is complete, **Work** opens the tenant's shared work queue. Owners can add lightweight personal to-dos or structured tickets, assign work to themselves, set priorities and due dates, filter the queue, and advance items through open, in-progress, waiting, and done states. The same record shape retains persona, schedule, boardroom, conversation, and run provenance for automated ticket creation.

The tenant **Agents** section lets owners and administrators create, edit, deactivate, and duplicate specialists. It controls identity, purpose, system instructions, turn order, provider and model overrides, reasoning and sampling preferences, context/output/time/tool ceilings, estimated-cost reservations, response and citation policies, external-action policy, and conditioned tool grants. Future runs snapshot the entire configuration into immutable persona versions; active runs do not change underneath an edit.

`MAINSPRING_TENANT_TEMPLATE` remains a backward-compatible provisioning default. New onboarding records persist the owner's selected `trades` or `software` template and `trades`, `saas`, or `msp` variant; this is tenant configuration, not a separate binary or fork.

The tenant **Documents** section uploads, extracts, indexes, lists, and displays business documents. The MVP accepts PDF, DOCX, TXT, Markdown, CSV/TSV, JSON, XML, HTML, YAML, and LOG files up to 15 MB, with extracted text bounded before it enters the tenant RAG service.

Ready documents can be attached when starting or continuing a boardroom conversation. Attachments remain visible as conversation knowledge, persist across follow-ups, and are snapshotted for each run. Agent document access is configured as none, the entire library, or an explicit document allowlist; a run can only narrow that configured scope.

The tenant **Email** section verifies and stores encrypted IMAP/SMTP settings, displays the inbox and message content, and sends owner-confirmed email through the idempotent outbound ledger. The development stack uses a visible mail simulator; production defaults to the network connector.

## Accounts and platform notices

Each tenant has exactly one organization owner and any number of members. The owner can invite and remove members from **Team**; invitations create a pending account, and the recipient chooses a password through the one-time activation link. The owner role cannot be granted through the tenant UI. A platform administrator, authenticated through the control-plane administrator credential, can transfer ownership through `POST /api/tenants/{tenantID}/owner` with `{ "user_id": "…" }`.

Platform administrators can publish `planned_downtime` and `service_notice` announcements through `POST /api/announcements`. Targets are `owners` for one tenant, `tenant_members` for one tenant, or `all_users` across every ready tenant. Announcements are delivered to each tenant's **Inbox**. In development, invitation links are displayed to the owner rather than being sent; production sends through the tenant's configured mailbox.

## Main Manager and Codex runner

The first boardroom persona is the **Main Manager**. It explicitly selects the isolated `codex` runner, uses high reasoning, and has an eight-call tool budget. Other enabled agents inherit the deployment runner (`codex` in the development stack) and receive document plus web-research capabilities. External actions remain proposal-only and require the existing approval path.

To enable the real runner, make a dedicated, runner-writable Codex authentication directory available through `MAINSPRING_RUNNER_CODEX_AUTH_PATH` (the development default points at `/home/rojo/.codex/auth.json`; its containing `.codex` directory is mounted). Build the `runner-image` and run a single boardroom prompt. Codex refreshes its short-lived session token there. Do not place the credential in this repository or a tenant container.

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
