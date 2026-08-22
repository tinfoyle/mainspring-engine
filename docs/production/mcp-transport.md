# MCP transport

- Status: protocol, complete typed tool surface, and signed global-to-cell routing composition implemented; OAuth authorization server, token lifecycle, and deployment pending
- Phase: 3.1 / 3.6
- Protocol: stateless Streamable HTTP through the official Go SDK, with current `2026-07-28` support and backward negotiation supplied by the SDK

## Implemented boundary

`internal/transport/mcpapi` is a thin transport over the same application services used by customer HTTP. Its complete composition publishes 53 typed Attention, action-recovery, Knowledge, Baseline and Finance tools. The app-api runtime mounts that composition only at private `/internal/v1/mcp` behind the same one-use signed route-proof acceptor as customer HTTP.

The boundary:

- accepts one bounded Bearer credential on every HTTP request and rejects cookie authentication;
- validates an optional `Origin` against exact configured origins before protocol handling;
- emits an RFC 9728 protected-resource metadata challenge on `401`;
- uses bounded stateless JSON responses and enables request-cancellation propagation;
- authenticates the actor before MCP lifecycle or tool discovery;
- reauthorizes the exact Account, package and read/mutation class for every tool call;
- places the resulting Account authority into the same route context consumed by the application service;
- returns typed structured output plus equivalent JSON text for client compatibility;
- exposes redacted queue outputs and authorization-sensitive detail outputs matching HTTP policy, including digest-only manual-resolution evidence;
- translates domain/access failures into the same stable Attention denial codes without returning backend errors;
- preserves the exact raw consequential payload through the MCP envelope so version-1 JSON number lexemes are not changed before canonicalization.

Tool annotations mark reads, additive mutations and cancellations explicitly. Mutation inputs carry a stable UUID operation/idempotency value and expected aggregate version where applicable. Consequential approval creation deliberately carries separate approval-idempotency and external-operation identifiers.

Protocol tests use the official SDK client against the real Streamable HTTP handler. They prove deterministic typed discovery, Bearer/cookie/origin behavior, Account/package/role authorization classification, service reuse, approval-queue and action-recovery redaction, dual-control command wiring, safe conflict parity and canonical-payload preservation.

`internal/transport/mcpgateway` and `internal/bootstrap/mcpgateway` now provide the global resource-server and routing half without a cell credential. The public endpoint is Account-scoped at `/mcp/v1/accounts/{accountID}`. It authenticates one audience/scope-bound token through an injected authority, parses only a bounded JSON-RPC envelope and the Account identifier required for routing, classifies every published tool through one reviewed registry, then resolves current Membership, role, package mode, placement and generation from the global store. The unchanged body is forwarded over the workload-authenticated cell transport with a fresh request-bound route proof; the customer Bearer value is removed and never reaches the cell.

The gateway serves RFC 9728 protected-resource metadata, challenges with the canonical metadata URL and `spyglass:mcp` scope, and validates the current `2026-07-28` `MCP-Protocol-Version`, `Mcp-Method` and `Mcp-Name` header mirrors against the body before using them. Older-version negotiation remains delegated to the official SDK. Tests prove exact tool classification, cross-Account and unknown-tool rejection before routing, modern header/body mismatch rejection, canonical audience/scope input, no token passthrough, exact body/path proof binding and one-use replay denial. This follows the current [MCP Streamable HTTP transport](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http) and [authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization) contracts.

## Why production MCP remains absent

The remaining missing boundary is the OAuth 2.1 authorization server and its durable authorization-code, access-token, refresh/revocation and client-metadata lifecycle. The routing constructor deliberately receives that audience/scope validator as an interface rather than inventing a static token or accepting browser sessions. `production-mcp` therefore remains in `deploy/package-surface-inventory.json` as an absent surface and no public deployment manifest exposes the gateway yet.

Production enablement requires one global MCP gateway composition that:

1. implement OAuth authorization-server metadata, authorization-code plus PKCE, issuer binding, Client ID Metadata Documents, exact redirect validation and RFC 8707 `resource` handling;
2. persist only hashed access/refresh credentials, bind them to User security version, audience and `spyglass:mcp` scope, rotate refresh credentials and make revocation immediate across replicas;
3. add consent/session binding, token and authorization rate limits, content-free durable audit, and User-visible grant revocation;
4. complete malformed-protocol, OAuth conformance, routing retry, cancellation, load and multi-replica tests;
5. add the public `mcp-gateway` process/deployment and allow only its workload identity to reach the private cell MCP route;
6. remove `production-mcp` from the absent inventory and add `mcp` to each actually published package boundary in the same reviewed change.

Until that composition exists, this package is executable and tested adapter code, not a claim that stage or production MCP is available.
