# Mainspring MCP surface

Each tenant runtime can expose a tenant-scoped Streamable HTTP MCP endpoint at `/mcp`. It uses the same stores, durable boardroom dispatcher, document index, approval ledger, and work-item validation as the browser application.

## Enable it

Set both values on the tenant runtime and restart it:

```dotenv
MAINSPRING_MCP_TOKEN=replace-with-a-random-token-at-least-32-bytes
MAINSPRING_MCP_USER_EMAIL=owner@example.test
```

The token authenticates every MCP request. The email binds mutations and audit records to an active tenant user. Approval decisions additionally require that identity to be the tenant owner. If the token is absent, `/mcp` returns 404.

The local Compose stack enables the demo endpoint at `http://demo.localhost:8088/mcp` with token `local-development-mcp-token-change-before-production`. This development credential must never be reused outside the local stack.

## Connect an agent

Configure an MCP client with a Streamable HTTP server and a bearer header. The exact client configuration key varies, but the connection values are:

```json
{
  "url": "http://demo.localhost:8088/mcp",
  "headers": {
    "Authorization": "Bearer local-development-mcp-token-change-before-production"
  }
}
```

The endpoint uses the official Go MCP SDK, stateless Streamable HTTP, JSON responses, request cancellation, bounded request bodies, origin checks, and protocol negotiation including the current 2026-07-28 protocol.

## Tools

| Tool | Purpose |
| --- | --- |
| `mainspring_list_agents` | Discover enabled agents and their IDs, roles, boardrooms, and capabilities. |
| `mainspring_list_documents` | List indexed tenant documents. |
| `mainspring_search_documents` | Retrieve bounded document excerpts for evidence and recall. |
| `mainspring_upload_document` | Upload and index UTF-8 text content up to 2 MB. |
| `mainspring_search_web` | Search public sources and return bounded metadata with citation IDs. Advertised only when web research is configured. |
| `mainspring_read_web_page` | Retrieve bounded main content from one public page. Advertised only when web research is configured. |
| `mainspring_list_work_items` | Filter tickets and todos in the work queue. |
| `mainspring_get_work_item` | Read a ticket, subtasks, conversation, attachments, run, and approvals. |
| `mainspring_create_work_item` | Create a ticket, todo, or parent-linked subtask. |
| `mainspring_update_work_item_status` | Change a work item's lifecycle status. |
| `mainspring_message_ticket` | Start or continue a durable conversation with exactly the selected agents and documents. |
| `mainspring_get_run` | Poll run status, messages, attachments, and pending approvals. |
| `mainspring_list_approvals` | Read pending approvals or decision history. |
| `mainspring_decide_approval` | Owner-only approval or rejection with normal idempotent execution. |

For an agent-driven ticket flow, call `mainspring_list_agents`, optionally upload or find documents, then call `mainspring_message_ticket`. Poll `mainspring_get_run`. If it reports `awaiting_approval`, inspect the payload and use `mainspring_decide_approval`; agent-created subtasks remain bound by the application to the parent ticket.

## Security model

- Tenant isolation comes from the tenant runtime and its dedicated database/RAG credentials.
- The configured active user is the actor on created records and approval decisions.
- MCP tools cannot bypass work-item validation, selected-agent limits, document attachment limits, conversation concurrency, approval payload hashes, or idempotent executors.
- MCP web research passes through the same signed capability broker, schemas, URL restrictions, quotas, citation shape, and audit path used by boardroom agents. Firecrawl is never exposed as a direct MCP server.
- Tool annotations identify read-only, additive, destructive, and idempotent behavior for compatible clients; server-side authorization remains authoritative.
- Use a unique high-entropy token per tenant and rotate it through the deployment secret store.
