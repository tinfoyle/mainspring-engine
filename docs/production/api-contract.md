# Customer API Contract

Status: route, owner, authentication, and generation drift gate executable; operation-specific payload schema certification remains in progress

[`api/spyglass.openapi.json`](../../api/spyglass.openapi.json) is the OpenAPI 3.1 source of truth for customer-facing JSON routes. It covers the global Account API and cell-owned Work/Agents API as one external surface at `app.infiniteocean.net`; clients never address or select a cell. Private workload APIs, health/metrics endpoints, server-rendered browser forms, and Stripe's provider-side schema are separate contracts.

Every operation declares a unique operation ID, owning runtime, authentication class, and success/problem responses. Path identifiers are typed and required. The current shared success response intentionally promises no body or exact status while request and response payloads are certified operation by operation; this contract must not yet be presented as a fully typed third-party SDK promise.

Run:

```text
go run ./cmd/apicontract -write
go run ./cmd/apicontract -check
```

The write command deterministically produces:

- `internal/generated/apicontract/routes.go` for Go-side route/operation inventory;
- `website/lib/generated/api-contract.ts` for web-side route and operation-ID types.

The check command parses the Go Account/cell transport registrations with the Go AST and compares every customer JSON method/path/service tuple with OpenAPI. It also rejects duplicate operation IDs, missing ownership, missing security declarations, missing responses, stale generated output, and undocumented or unregistered routes. CI runs the check before accepting a change.

Changing a route therefore requires one reviewed OpenAPI change and regenerated artifacts in the same commit. Do not hand-edit generated files. Breaking path, method, authentication, or payload changes require a versioning/deprecation decision; adding an operation requires authorization, isolation, error, and browser contract tests before advertisement.

## Payload-schema closeout

For each operation, replace the shared success response with exact status, request, success, error-code, query, and header schemas derived from the application command/view types. Then generate typed request/response clients, run both Go handlers and browser clients against schema fixtures, add backward-compatibility diff classification, and publish the sanitized document. The production gate requires no generic responses on advertised partner/API operations.
