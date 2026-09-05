# Spyglass API and MCP interaction guide

- Contract date: 2026-08-24
- HTTP contract: `api/spyglass.openapi.json` (219 operations)
- Generated Go inventory: `internal/generated/apicontract/routes.go`
- Generated TypeScript inventory/types: `website/lib/generated/api-contract.ts` and `website/lib/generated/api-types.ts`
- MCP inventory: 89 routed cell tools plus five global Account-export tools

## Local origins

| Surface | Local origin |
|---|---|
| Customer application and HTTP API | `https://app.infiniteocean.localhost:8444` |
| MCP resource server | `https://mcp.infiniteocean.localhost:8444` |
| MCP authorization server | `https://app.infiniteocean.localhost:8444` |
| MCP protected-resource metadata | `https://mcp.infiniteocean.localhost:8444/.well-known/oauth-protected-resource` |
| OAuth authorization-server metadata | `https://app.infiniteocean.localhost:8444/.well-known/oauth-authorization-server` |
| Deterministic Google OAuth fixture | `https://oauth.infiniteocean.localhost:8444` |

The local Caddy authority is test-only. Browser sessions trust it through the local setup; command-line diagnostics may use the checked local trust setup or `curl --insecure` only against these `.localhost` origins.

## HTTP authentication and Account routing

Customer HTTP uses the secure Spyglass session cookie established by passkey login. Do not put a session, download capability, OAuth code or provider token in a URL. State-changing browser requests must have the exact application `Origin` and the generated content type.

Account-scoped operations use `/api/v1/accounts/{accountID}/...`. The path ID selects an Account but is never authority. The app router resolves the current User membership, role, package mode, Account placement and generation, then signs one request-bound route proof for the cell. The cell repeats Account/package/role authorization. Clients cannot supply or reuse that proof.

Common request rules:

- Use `Content-Type: application/json` for JSON bodies and the generated multipart type for document/asset upload.
- Use a new UUID in `Idempotency-Key` for every new mutation. Reuse it only for an exact retry of the same route and payload.
- Send the current weak ETag from a detail response in `If-Match` for optimistic updates. Never manufacture a version.
- Treat cursors as opaque. A cursor belongs to one collection/filter and cannot be decoded, edited or replayed across collections.
- Follow only generated response schemas. Unknown fields in requests are rejected; backend error detail is intentionally redacted.
- Account-export artifact download uses its short-lived custom authorization capability, not the session cookie.

Example read:

```bash
curl --insecure --fail-with-body \
  --cookie '__Host-spyglass_session=<local-session>' \
  'https://app.infiniteocean.localhost:8444/api/v1/accounts/10000000-0000-4000-8000-000000000001/work-items?limit=25'
```

Example exact mutation/retry:

```bash
curl --insecure --fail-with-body \
  --request POST \
  --header 'Origin: https://app.infiniteocean.localhost:8444' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: 10000000-0000-4000-8000-000000000002' \
  --cookie '__Host-spyglass_session=<local-session>' \
  --data '{"kind":"todo","title":"Review renewal controls","description":"Confirm the current evidence set.","priority":"normal","assignment":{"responsibility":"shared"}}' \
  'https://app.infiniteocean.localhost:8444/api/v1/accounts/10000000-0000-4000-8000-000000000001/work-items'
```

## Generated HTTP operation inventory

The OpenAPI document is the line-item inventory. The groups below are the stable product-use-case index; counts sum to 210.

| Group | Count | Use cases |
|---|---:|---|
| Identity and sessions | 29 | registration, current contact, passkey login/registration/reauthentication, recovery, sessions, connected MCP clients and security posture/events |
| Account and Membership | 17 | selected context, current Membership, invitations, Membership roles/status, ownership, contact change and closure |
| Billing and Catalog | 8 | public Catalog, billing status, AI Token balance, subscription and one-time checkout, promotion redemption, portal and Stripe webhook |
| Account portability | 6 | request/list/get/cancel, short-lived download capability and artifact stream |
| Privacy, analytics and Affiliate | 16 | host-only consent/history/erasure, consent-bound events, passkey-verified rights requests, Affiliate enrollment/code replacement, identity-owned data export, aggregate statements and structured support reviews |
| Work | 9 | list/summary/detail/children, create, transition, assignment and provenance/conversation links |
| Attention | 19 | information, reviews, approvals and dual-controlled action recovery |
| Agents | 11 | boardrooms, manager/personas, conversations/messages and Run start/read/resolve |
| Schedules | 8 | list/get/create/revise/delete/pause/resume/trigger |
| Knowledge | 13 | evidence, claims/facts, documents, retrieval, publication and citation |
| Baseline | 18 | current assessment discovery, assessment lifecycle, plan/Work maintenance and source grants |
| Finance | 21 | Ledgers, posting accounts, entries, period close, reversal and reconciliation |
| Marketing | 17 | campaigns, immutable asset revisions and downloads, release governance and activation lifecycle |
| Integrations | 21 | connections, credentials, health, OAuth, web research, delivery execution and recovery |

Run `go run ./cmd/apicontract -check` after editing the OpenAPI document. Use `-write` only when intentionally regenerating the Go and TypeScript artifacts.

## Google OAuth lifecycle

Google Drive authorization is application-owned:

1. A current Integrations Owner/Administrator with a recent passkey begins authorization for one exact Drive connection revision.
2. The backend creates one bounded state/PKCE session and returns the provider consent URL.
3. Google redirects to the exact HTTP callback. The callback is the only boundary that accepts the authorization code.
4. The backend exchanges once, seals refresh material outside PostgreSQL and activates or rotates the exact credential generation.
5. HTTP and MCP status expose only safe lifecycle fields.
6. Revocation confirms the provider outcome, fences the vault generation, revokes the durable binding and then purges unusable material.

MCP exposes begin, status and revoke. It intentionally has no callback/code/token tool. Begin and revoke require recent passkey evidence carried by the MCP grant; refresh rotation cannot make old passkey evidence newer.

## MCP authorization and transport

The public endpoint for an Account is:

```text
POST https://mcp.infiniteocean.localhost:8444/mcp/v1/accounts/{accountID}
```

MCP uses OAuth 2.1-style public-client authorization with:

- HTTPS Client ID Metadata Document identity;
- exact registered redirect URI;
- authorization code plus mandatory S256 PKCE;
- exact resource `https://mcp.infiniteocean.localhost:8444`;
- exact scope `spyglass:mcp`;
- five-minute one-use authorization codes;
- 15-minute access tokens; and
- rotating refresh tokens with family revocation on replay.

The authorization server endpoints are `/oauth/authorize`, `/oauth/token` and `/oauth/revoke` on the application origin. Token and revocation bodies are `application/x-www-form-urlencoded`; client secrets and HTTP Basic authentication are rejected. Access tokens are Bearer credentials and never reach the cell: the gateway authenticates and removes them, then forwards the unchanged JSON-RPC body with a fresh one-use signed route proof.

Use an MCP SDK for production clients. A content-free discovery diagnostic is:

```bash
curl --insecure --fail-with-body \
  --request POST \
  --header 'Authorization: Bearer <local-access-token>' \
  --header 'Content-Type: application/json' \
  --header 'Accept: application/json, text/event-stream' \
  --header 'MCP-Protocol-Version: 2026-07-28' \
  --header 'Mcp-Method: tools/list' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"content-free-diagnostic","version":"1"}}}}' \
  'https://mcp.infiniteocean.localhost:8444/mcp/v1/accounts/10000000-0000-4000-8000-000000000001'
```

Example read tool call:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "_meta": {"io.modelcontextprotocol/protocolVersion": "2026-07-28"},
    "name": "spyglass_integrations_web_search",
    "arguments": {
      "account_id": "10000000-0000-4000-8000-000000000001",
      "operation_id": "10000000-0000-4000-8000-000000000002",
      "connection_id": "10000000-0000-4000-8000-000000000003",
      "query": "reviewed customer retention guidance"
    }
  }
}
```

For this current protocol form, also send `Mcp-Method: tools/call` and `Mcp-Name: spyglass_integrations_web_search`. The gateway rejects partial or mismatched modern mirror headers before routing.

Tool results contain typed `structuredContent` and equivalent JSON text. Consequential mutations carry a stable operation UUID and current expected version where their HTTP equivalent uses idempotency and `If-Match`.

## MCP tool inventory

### Account lifecycle (global gateway, 5)

`spyglass_account_export_list`, `spyglass_account_export_get`, `spyglass_account_export_request`, `spyglass_account_export_cancel`, `spyglass_account_export_download_capability_create`.

### Attention (19)

`spyglass_attention_information_list`, `spyglass_attention_information_get`, `spyglass_attention_information_create`, `spyglass_attention_information_answer`, `spyglass_attention_information_cancel`; `spyglass_attention_review_list`, `spyglass_attention_review_get`, `spyglass_attention_review_create`, `spyglass_attention_review_decide`, `spyglass_attention_review_cancel`; `spyglass_attention_approval_list`, `spyglass_attention_approval_get`, `spyglass_attention_approval_create`, `spyglass_attention_approval_decide`, `spyglass_attention_approval_cancel`; `spyglass_attention_action_list`, `spyglass_attention_action_get`, `spyglass_attention_action_request_resolution`, `spyglass_attention_action_confirm_resolution`.

### Knowledge and Baseline (13)

Knowledge: `spyglass_knowledge_evidence_register`, `spyglass_knowledge_claim_propose`, `spyglass_knowledge_claim_get`, `spyglass_knowledge_claim_list`, `spyglass_knowledge_claim_decide`, `spyglass_knowledge_fact_list`, `spyglass_knowledge_document_retrieve`, `spyglass_knowledge_document_citation_get`.

Baseline: `spyglass_baseline_start`, `spyglass_baseline_get`, `spyglass_baseline_mutate`, `spyglass_baseline_source_list`, `spyglass_baseline_source_mutate`.

### Finance (21)

Ledgers: `spyglass_finance_ledger_list`, `spyglass_finance_ledger_get`, `spyglass_finance_ledger_create`, `spyglass_finance_ledger_revise`, `spyglass_finance_ledger_close_period`, `spyglass_finance_ledger_archive`.

Posting accounts: `spyglass_finance_posting_account_list`, `spyglass_finance_posting_account_get`, `spyglass_finance_posting_account_create`, `spyglass_finance_posting_account_revise`, `spyglass_finance_posting_account_archive`.

Entries: `spyglass_finance_entry_list`, `spyglass_finance_entry_get`, `spyglass_finance_entry_create_draft`, `spyglass_finance_entry_revise_draft`, `spyglass_finance_entry_post`, `spyglass_finance_entry_reverse`.

Reconciliation: `spyglass_finance_reconciliation_list`, `spyglass_finance_reconciliation_get`, `spyglass_finance_reconciliation_create`, `spyglass_finance_reconciliation_confirm`.

### Marketing (16)

Campaigns: `spyglass_marketing_campaign_list`, `spyglass_marketing_campaign_get`, `spyglass_marketing_campaign_create_draft`, `spyglass_marketing_campaign_revise`, `spyglass_marketing_campaign_archive`, `spyglass_marketing_campaign_activate`, `spyglass_marketing_campaign_pause`, `spyglass_marketing_campaign_complete`.

Assets/releases: `spyglass_marketing_asset_revision_list`, `spyglass_marketing_asset_revision_create_draft`, `spyglass_marketing_release_list`, `spyglass_marketing_release_get`, `spyglass_marketing_release_create_draft`, `spyglass_marketing_release_submit`, `spyglass_marketing_release_approve`, `spyglass_marketing_release_cancel`.

### Integrations (20)

Connections and credentials: `spyglass_integrations_connection_list`, `spyglass_integrations_connection_get`, `spyglass_integrations_connection_create`, `spyglass_integrations_connection_revise`, `spyglass_integrations_credential_activate`, `spyglass_integrations_credential_rotate`, `spyglass_integrations_connection_disable`, `spyglass_integrations_connection_enable`, `spyglass_integrations_connection_revoke`.

OAuth and health: `spyglass_integrations_authorization_begin`, `spyglass_integrations_authorization_status`, `spyglass_integrations_credential_revoke`, `spyglass_integrations_health_list`.

Delivery: `spyglass_integrations_execution_list`, `spyglass_integrations_execution_get`, `spyglass_integrations_execution_prepare`, `spyglass_integrations_execution_request_resolution`, `spyglass_integrations_execution_confirm_resolution`.

Research: `spyglass_integrations_web_search`, `spyglass_integrations_web_read`.

`web_search` is an Integrations read. `web_read` creates immutable Knowledge evidence, so the gateway requires both Integrations mutation and Knowledge mutation authority. No MCP tool can widen a connection scope or bypass Knowledge admission.

## HTTP/MCP relationship

Attention, Knowledge, Baseline, Finance, Marketing and Integrations MCP tools call the same application services as their HTTP equivalents. Tool names are use-case oriented rather than mechanically copied from paths; `spyglass_baseline_mutate` and `spyglass_baseline_source_mutate` select a closed action in their typed input. Account export tools are global because portability must remain available even when every commercial package is absent.

Direct Work/Agent workspace operations remain HTTP plus governed runner/schedule boundaries. Their package MCP boundary is the typed Attention and approval/action surface needed for human/automation interaction; MCP does not expose a generic arbitrary Work or Agent command channel.

## Authorization outcomes

| Condition | Reads | New mutation | Already admitted work |
|---|---|---|---|
| Package `enabled` | allowed by role/object policy | allowed by role/object/policy/limit checks | runs normally |
| Package `read_only` | allowed | `package_read_only` | may settle, cancel or expire without gaining authority |
| Package absent/suspended | `package_not_entitled` | `package_not_entitled` | bounded settlement/cancellation only; no successor work |
| Wrong Account or concealed object | safe not-found/denied outcome | same | no information leak |
| Stale ETag/version | conflict | conflict | reload current state before a new decision |
| Recent passkey required | read may remain available | strong-authentication outcome | existing bounded work is unchanged |

Members may perform ordinary reads and permitted drafts. Owner/Administrator is required for package governance, connection authority, release approval, external execution preparation and recovery. Some irreversible Account/security actions are Owner-only. Agent workloads can use only their invocation-bound capabilities and can draft where explicitly allowed; they cannot approve, post, deliver, rotate credentials or perform arbitrary research/network calls.

## Safe errors and retry rules

- `401` means the session/token is absent or invalid. MCP returns RFC 9728 challenge metadata.
- `403`-class safe outcomes include package, role, recent-passkey, read-only and object-policy denial without backend detail.
- `404` may deliberately conceal a cross-Account or unauthorized object.
- `409` means idempotency mismatch, stale version, invalid state transition or another exact conflict. Do not blind-retry with changed input.
- `429` and bounded unavailable outcomes may be retried with backoff only when the operation contract permits it.
- A network timeout after a mutation is an unknown client result. Retry the exact route, idempotency UUID and payload; never create a second operation UUID merely because the response was lost.
- External-effect state `unknown` is not failure. Show reconciliation/manual-resolution state and never offer a blind resend.
- Queue/health states are content-free. Present healthy/degraded/unavailable/stale and retry/manual-resolution guidance; never display operator leases, secret references or sealed cursors.

## Deterministic local fixtures

| Fixture/gate | Purpose |
|---|---|
| `make verify-google-oauth` | OAuth consent, code exchange, Drive capture, Knowledge settlement and revocation |
| `make verify-imap` | implicit-TLS mailbox scope, UID cursor, MIME capture, deletion/reset/outage/revocation |
| `make verify-web-research` | public-web search/read, SSRF/resource bounds, replay and immutable Knowledge revisions |
| `make verify-integration-connector` | exact Marketing delivery preparation, health, execute/reconcile evidence and least authority |
| `make test` | complete application, migration, race, object-policy, process/package and generated-contract certificate |

Run these only from UbuntuRojo:

```bash
cd /mnt/c/Users/Tinfo/Documents/Mainspring/deploy/docker/spyglass
make verify-google-oauth
make verify-imap
make verify-web-research
make verify-integration-connector
```

## Operator-only boundary

Do not expose database leases, queue table controls, broker references, provider-secret paths, sealed cursors, object-store versions/keys, internal route proofs, workload identities, movement generations, restore checkpoints or manual SQL as customer actions. Customer recovery is limited to the canonical retry, cancellation, credential lifecycle and dual-control resolution operations documented by OpenAPI/MCP.
