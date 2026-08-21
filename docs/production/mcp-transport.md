# MCP transport

- Status: protocol and Attention tool adapter implemented; production identity/routing composition and deployment pending
- Phase: 3.1 / 3.6
- Protocol: stateless Streamable HTTP through the official Go SDK, with current `2026-07-28` support and backward negotiation supplied by the SDK

## Implemented boundary

`internal/transport/mcpapi` is a thin transport over the same `application/attention.Service` used by customer HTTP. It publishes 15 typed tools covering queue, detail, create, answer, decision and cancellation operations for Information requests, Work reviews and consequential approvals.

The boundary:

- accepts one bounded Bearer credential on every HTTP request and rejects cookie authentication;
- validates an optional `Origin` against exact configured origins before protocol handling;
- emits an RFC 9728 protected-resource metadata challenge on `401`;
- uses bounded stateless JSON responses and enables request-cancellation propagation;
- authenticates the actor before MCP lifecycle or tool discovery;
- reauthorizes the exact Account, package and read/mutation class for every tool call;
- places the resulting Account authority into the same route context consumed by the application service;
- returns typed structured output plus equivalent JSON text for client compatibility;
- exposes redacted queue outputs and authorization-sensitive detail outputs matching HTTP policy;
- translates domain/access failures into the same stable Attention denial codes without returning backend errors;
- preserves the exact raw consequential payload through the MCP envelope so version-1 JSON number lexemes are not changed before canonicalization.

Tool annotations mark reads, additive mutations and cancellations explicitly. Mutation inputs carry a stable UUID operation/idempotency value and expected aggregate version where applicable. Consequential approval creation deliberately carries separate approval-idempotency and external-operation identifiers.

Protocol tests use the official SDK client against the real Streamable HTTP handler. They prove deterministic typed discovery, Bearer/cookie/origin behavior, Account/package authorization classification, service reuse, approval-queue redaction, safe conflict parity and canonical-payload preservation.

## Why production MCP remains absent

The transport does not mint or persist customer access tokens, resolve global Account placement, or own a cell database credential. Those concerns cannot be guessed into `app-api` without breaking the cell boundary. `production-mcp` therefore remains in `deploy/package-surface-inventory.json` as an absent surface and no deployment manifest exposes this handler yet.

Production enablement requires one global MCP gateway composition that:

1. validates audience-bound User or workload access tokens and serves protected-resource metadata;
2. resolves current Membership, role, package access, cell placement and generation for each tool requirement;
3. forwards the exact Account-bound call to the assigned cell using the existing short-lived signed route proof, without forwarding the Bearer credential;
4. supplies separate Work and Agents package authority per call rather than broad combined authority;
5. records content-free authentication, authorization and tool-call audit outcomes;
6. exposes the endpoint only after token issuance/revocation, routing retry, rate-limit, conformance and multi-replica tests pass;
7. removes `production-mcp` from the absent inventory and adds `mcp` to the Work and Agents boundaries in the same reviewed change.

Until that composition exists, this package is executable and tested adapter code, not a claim that stage or production MCP is available.
