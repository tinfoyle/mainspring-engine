# Phase 3 local backend completion report

- Certificate date: 2026-08-24
- Scope: local backend construction through the backend-to-customer-UI boundary
- Runtime: UbuntuRojo WSL 2, native Docker Engine and the `spyglass-local` Compose project
- Result: **complete for the local backend boundary**
- Deliberately excluded: customer-facing Vue SPA completion, GHCR, Hostinger Stage mutation, LKE and production release work

## Outcome

The local backend now implements every retained launch package and every provider capability required before customer-interface construction. Work, Agents, Knowledge, Finance, Marketing and Integrations are executable in `deploy/package-surface-inventory.json`. Scheduling and Baseline maintenance have canonical workers. Drive, inbound email, outbound delivery, exact HTTPS publication and public-web research have closed adapters, deterministic tests, health behavior and bounded recovery.

The customer API contains 188 generated operations. The composed cell MCP server publishes 89 typed package tools, while the global MCP gateway adds five Account-export tools. HTTP, MCP, schedules, runner tools and workers call shared application services; they do not own domain authorization.

This certificate establishes the boundary requested before customer-facing UI work. It is not a production-readiness claim. Environment-specific provider acceptance, immutable release artifacts, Stage and LKE certification remain later work after the UI is complete.

## Executable package inventory

| Package | Customer/application boundaries | Durable execution boundaries | Local acceptance |
|---|---|---|---|
| Work | browser, HTTP, package-routed MCP Attention tools, admission broker, Agent tools | Work reconciler | ordinary/race suites, migration/RLS tests, complete Docker gate |
| Agents | browser, HTTP, package-routed MCP approval/action tools, runner | dispatch, result projection and schedule workers | runner, broker, schedule, queue-recovery and complete Docker gates |
| Knowledge | browser, HTTP, MCP | document processing and source admission | real MinIO policy, scan/extract/index, retrieval/citation and source-capture journeys |
| Finance | browser, HTTP, MCP, Agent draft tools | approved-action worker | ledger/reconciliation, Agent provenance and unknown-effect recovery suites |
| Marketing | browser, HTTP, MCP, Agent draft tools | approved-action and Integration connector workers | immutable asset/release, Attention activation and outbound connector suites |
| Integrations | browser, HTTP, MCP, Agent research tools | connector execution, health and source-sync workers | OAuth/Drive, IMAP, web research, outbound delivery, broker and policy suites |

## Use-case closure matrix

The operation names below are the canonical generated HTTP families. Exact methods, paths, schemas and status responses are in `api/spyglass.openapi.json`. Exact MCP names are in the interaction guide and the MCP registries.

| Product use case | Canonical HTTP operations | MCP/application surface | Worker or adapter | Acceptance evidence |
|---|---|---|---|---|
| Registration, login and session security | `beginRegistration`, `completeRegistration`, passkey login/registration/reauthentication, session list/revoke/logout | Account-level HTTP; MCP authorization reuses the signed-in session | identity-maintenance worker | passkey, recovery, session, rate-budget and PostgreSQL suites |
| Account selection, membership and ownership | `accountContext`, membership/invitation/ownership operations | signed Account routing for every package tool | app router and route-receipt worker | cross-Account concealment, Membership/role and one-use route-proof tests |
| Billing and package entitlement | `billingStatus`, checkout and portal sessions, Stripe webhook | package requirements are classified before MCP routing | billing and entitlement workers | Stripe projection, webhook replay and enabled/read-only/suspended policy tests |
| Account closure and portability | closure operations and five export operations plus artifact download | five global `spyglass_account_export_*` tools | lifecycle, export build and expiry workers | coordinated snapshots, exact object versions, download capability, erase/restore suites |
| Work lifecycle | nine `work*` operations | Work-owned Attention tools and bounded runner capabilities | admission broker and Work reconciler | capacity, transition, assignment, provenance, downgrade and replay suites |
| Human attention and consequential action recovery | nineteen `attention*` operations | nineteen typed Attention tools | approved-action workers owned by affected packages | exact reviewer/approver, payload binding, dual control and unknown-result tests |
| Agent workspace and execution | eleven Agent boardroom/conversation/Run operations | bounded package tools through the runner broker/tool router | dispatch, runner controller, runner broker and projection worker | invocation/Run binding, cancellation, lease loss, result projection and Docker runner gates |
| Customer schedules | eight `schedule*` operations | schedule context resolves the same application commands | schedule-execution worker | timezone, missed-run, trigger replay, package drift and worker restart tests |
| Knowledge evidence, claims and facts | seven evidence/claim/fact operations | eight typed Knowledge tools include governed retrieval/citations | shared application service | citation/source attribution, human decision and cross-Account tests |
| Knowledge documents and retrieval | six document/retrieval operations | retrieval and citation tools; ingestion stays HTTP/internal source admission | document worker; MinIO, ClamAV and Tika adapters | immutable revisions, malware/extraction/indexing, exact-version deletion and object-policy gates |
| Baseline assessment and maintenance | seventeen Baseline and source-grant operations | five typed Baseline tools, separated by Knowledge and Integrations authority | baseline-maintenance worker | deterministic plan/Work materialization, renewal/reassessment and source-scope tests |
| Finance | twenty-one `finance*` operations | twenty-one typed Finance tools plus narrow Agent draft capabilities | Finance approved-action worker | double-entry, close/reconcile, draft provenance, posting recovery and RLS tests |
| Marketing | sixteen `marketing*` operations | sixteen typed Marketing tools plus six bounded Agent capabilities | Marketing approved-action path and connector worker | immutable asset manifest, approval-bound activation, object admission and connector tests |
| Integration connection lifecycle | connection, credential-binding, health and authorization operations | connection, health and safe OAuth begin/status/revoke tools | connector worker and encrypted/mounted credential brokers | rotation/revocation, recent-passkey, secret-boundary and health-staleness tests |
| Google Drive capture | authorization plus Baseline source-grant operations | safe OAuth lifecycle tools; callback remains HTTP-only | Google OAuth/Drive adapter, connector and Knowledge document workers | `make verify-google-oauth`: consent through immutable Knowledge publication and revocation |
| Inbound email capture | connection and Baseline source-grant operations | governed connection/source tools; message content never enters status output | implicit-TLS IMAP adapter, connector and Knowledge workers | `make verify-imap`: UIDVALIDITY/UID cursor, MIME parts, deletion/reset, outage and revocation |
| Public-web research | `integrationWebResearchSearch`, `integrationWebResearchRead` | `spyglass_integrations_web_search` and `spyglass_integrations_web_read` | hardened retriever, Firecrawl adapter and Knowledge admission | `make verify-web-research`: SSRF fence, bounds, replay and immutable changed-content revisions |
| Outbound email and HTTPS publication | Integration execution prepare/read/recovery operations | Integrations execution tools | SMTP and web-HTTPS adapters through connector worker | `make verify-integration-connector` plus adapter protocol, ambiguity and reconciliation suites |

## Provider and secret boundaries

| Capability | Credential purpose | Network/content boundary | Failure and recovery rule |
|---|---|---|---|
| `google_drive.read` | exact OAuth refresh generation | fixed Google token/Drive origins; folder grant cannot widen connection scope | page cursor advances only after every capture settles; revocation stops new claims |
| `email.read` | IMAP only | implicit TLS, exact folders/date bounds, bounded UID pages and MIME subset | UIDVALIDITY reset restarts safely; partial page failure retains the prior cursor |
| `email.send` | SMTP only | implicit TLS, exact sender/audience and immutable release payload | uncertain DATA acceptance becomes unknown and is never automatically resent |
| `web.publish` | `web_https` only | exact HTTPS origin/path, public-address pinning, create-only PUT | only digest-bound HEAD reconciliation may prove success or no effect |
| `web.research` | Firecrawl API token only | reviewed origin/path policy, DNS pinning, redirect/type/size/time/decompression bounds | provider degradation affects only research; exact request replay reuses the capture |

Provider material is absent from PostgreSQL customer records, HTTP/MCP output, logs, metrics, events, exports and Git. The one-operation broker checks Account, operation, connection, credential identity/generation, provider, purpose, capability, reference digest and lease expiry before returning bounded material to one adapter call.

## Cross-package lifecycle closure

- Account movement includes durable OAuth, source-grant, source-capture, email and web-research state and regenerates derived queues under the destination namespace.
- Account export includes customer-owned non-secret scopes, immutable capture provenance and content; it omits credentials, security digests, queue leases, sealed cursors, broker references and provider tokens.
- Account erasure counts and removes the new durable record families and private queues, including `integration_web_research_captures`.
- Restore replay remains migration-ledger and namespace-generation fenced. Derived health and claim queues are reconstructed rather than treated as portable customer data.
- Operational health/status/metrics remain content-free and expose only bounded states, counts, ages and recovery classes.
- No enabled production module imports or executes the prototype runtime. Prototype transformation/import remains a separately governed migration capability.

## Local certificate

The following gates passed from UbuntuRojo on 2026-08-24:

```text
go test ./...
make -C deploy/docker/spyglass test
make -C deploy/docker/spyglass verify-google-oauth
make -C deploy/docker/spyglass verify-imap
make -C deploy/docker/spyglass verify-web-research
make -C deploy/docker/spyglass verify-integration-connector
go run ./cmd/apicontract -check
git diff --check
```

The complete Docker gate includes uncached and race-enabled Go suites, PostgreSQL migration/RLS/movement/export/erasure tests, object policies, workload boundaries, process/package inventories, generated-contract drift, website build/lint/tests and dependency audit.

## Backend-to-UI boundary

The future customer UI may choose navigation, layout, responsive behavior and progressive disclosure. It must not invent domain transitions, package authorization, cursor interpretation, retry safety, OAuth state, provider recovery or unknown-effect resolution. Those rules are now backend contracts.

Customer-facing UI work starts only under a later explicit direction. Release construction and Stage/LKE/production work remain after UI feature completion.
