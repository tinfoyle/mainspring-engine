# MCP transport

- Status: protocol, complete typed tool surface, signed global-to-cell routing, OAuth 2.1 core, distributed endpoint budgets, User grant control, and local/Stage/LKE deployment topology implemented; Stage activation and public-edge failover certified, external-client authorization/tool/revocation pending
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

`internal/transport/mcpgateway` and `internal/bootstrap/mcpgateway` provide the global resource server and routing half without a cell credential. The public endpoint is Account-scoped at `/mcp/v1/accounts/{accountID}`. It authenticates one audience/scope-bound token, parses only a bounded JSON-RPC envelope and the Account identifier required for routing, classifies every published tool through one reviewed registry, then resolves current Membership, role, package mode, placement and generation from the global store. The unchanged body is forwarded over the workload-authenticated cell transport with a fresh request-bound route proof; the customer Bearer value is removed and never reaches the cell.

The gateway serves RFC 9728 protected-resource metadata, challenges with the canonical metadata URL and `spyglass:mcp` scope, and validates the current `2026-07-28` `MCP-Protocol-Version`, `Mcp-Method` and `Mcp-Name` header mirrors against the body before using them. Older-version negotiation remains delegated to the official SDK. Tests prove exact tool classification, unsupported-method, cross-Account and unknown-tool rejection before routing, modern header/body mismatch rejection, canonical audience/scope input, no token passthrough, exact body/path proof binding and one-use replay denial. A transport failure receives one retry with a fresh request ID/proof, cancellation propagates without retry, and a race-enabled 128-request certificate exercises stateless concurrent routing. This follows the current [MCP Streamable HTTP transport](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http) and [authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization) contracts.

## OAuth and deployment checkpoint

`internal/application/mcpauth`, `internal/adapters/postgres/mcp_auth.go` and `internal/transport/mcpoauth` now implement the authorization-server core on the existing `app.*` origin. Authorization uses the existing signed-in browser session and explicit consent, exact registered redirect matching from an HTTPS Client ID Metadata Document, mandatory S256 PKCE, RFC 8707 exact `resource`, exact `spyglass:mcp` scope and the RFC 9207 `iss` response parameter. Client metadata retrieval is HTTPS-only, redirect-free, size/time bounded, DNS-pinned for the request and rejects non-public addresses.

Authorization codes are one-use and expire after five minutes. Opaque access tokens expire after 15 minutes; refresh tokens expire after 30 days and rotate on every use. PostgreSQL stores only SHA-256 digests. Grants bind User, client, audience, scope and `users.security_version`; credential recovery or another security-version advance invalidates them immediately. Reusing a consumed refresh credential atomically revokes its full refresh/access family. Revocation is immediate across replicas, and content-free security events record approvals, denials, exchanges, rotations, replay detection and revocation.

Authorization, token and revocation endpoints consume separate PostgreSQL-backed network-actor budgets, so limits remain consistent across account-api replicas and failures close the endpoint. Identity Security lists only grants with currently usable access or refresh credentials, identifies each client from its verified metadata document, and lets the signed-in User revoke a grant. That transaction revokes the grant plus every access/refresh family and records a content-free event.

Transport conformance tests reject duplicated security parameters, query parameters on form endpoints, JSON token bodies, client secrets and HTTP Basic authentication for CIMD public clients. A two-service PostgreSQL integration contract exchanges, authenticates and rotates across separate service instances, then proves refresh replay, token revocation and User grant revocation are immediately visible to the other replica.

The executable `mcp-gateway` mode, dedicated Docker database role, local/Stage Compose service, Hostinger ingress, client-only Stage workload certificate, LKE Deployment/Service/PDB/HPA, public ingress and default-deny network-policy additions are revision controlled. Local Docker proves both discovery documents and the unauthenticated RFC 9728 challenge through `mcp.infiniteocean.localhost`.

RC.2 applies migrations 33-34 and the least-privilege role on Hostinger, publishes both discovery documents and the RFC 9728 challenge through public DNS/TLS, and keeps exactly two healthy Stage gateway replicas. Its public-edge certificate records 128/128 expected responses with both replicas, with each replica stopped in turn, and after both were restored. The certificate also retains a rejected 32-concurrency calibration with nine client timeouts; the accepted 16-concurrency profile is not misrepresented as a higher-capacity result. The only remaining MCP activation evidence is the signed-in public-client consent/code exchange, one routed tool call, refresh rotation and revocation sequence.

## Why `production-mcp` remains absent

The implementation is deployable, but `production-mcp` remains in `deploy/package-surface-inventory.json` until the release gates below are complete. This avoids treating a local topology check as an applied customer capability.

Production enablement requires one global MCP gateway composition that:

1. capture an external-client authorization/tool-call/refresh/revocation certificate through the applied Hostinger ingress;
2. retain the completed applied multi-replica public-edge load/failover certificate with the RC.2 release evidence;
3. remove `production-mcp` from the absent inventory and add `mcp` to each actually published package boundary in the same reviewed change.

Until that composition exists, this package is executable and tested adapter code, not a claim that stage or production MCP is available.
