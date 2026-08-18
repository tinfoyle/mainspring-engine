# Customer API Contract

Status: all 56 customer-facing operations have typed success contracts; route, owner, authentication, reference, generation, and no-generic-response drift gates are executable

[`api/spyglass.openapi.json`](../../api/spyglass.openapi.json) is the OpenAPI 3.1 source of truth for customer-facing JSON routes. It covers the global Account API and cell-owned Work/Agents API as one external surface at `app.infiniteocean.net`; clients never address or select a cell. Private workload APIs, health/metrics endpoints, server-rendered browser forms, and Stripe's provider-side schema are separate contracts.

Every operation declares a unique operation ID, owning runtime, authentication class, exact success status and payload, and stable Problem envelope. Path identifiers are typed and required. The contract covers public Catalog and Account acquisition; password, passkey, recovery, and session security; Account selection, Memberships, invitations, ownership, closure, and Billing; Stripe webhook acceptance; and cell-owned Account context, Work, and Agents. This includes the free-Account provisioning result, owner-enrollment gate, bounded WebAuthn wire envelopes, explicit Origin requirements for cookie-authenticated mutations, idempotency and optimistic-concurrency headers, the intentionally unauthenticated idempotent logout boundary, and safe external DTOs instead of internal aggregates. Persona publication exposes only caller-owned model/tool/citation/action policy; Spyglass injects and owns the immutable result schema.

Run:

```text
go run ./cmd/apicontract -write
go run ./cmd/apicontract -check
```

The write command deterministically produces:

- `internal/generated/apicontract/routes.go` for Go-side route/operation inventory;
- `website/lib/generated/api-contract.ts` for web-side route, operation-ID, and route-versus-typed maturity;
- `website/lib/generated/api-types.ts` for deterministic TypeScript types generated from certified component schemas.

The check command parses the Go Account/cell transport registrations with the Go AST and compares every customer JSON method/path/service tuple with OpenAPI. It also rejects duplicate operation IDs, missing ownership, missing security declarations, missing responses, unresolved or external schema references, stale generated output, and undocumented or unregistered routes. Regression tests keep the acquisition, identity-security, Account-context, Work, and Agents slices typed, and a whole-surface gate rejects any reintroduction of a generic success response. CI runs the check before accepting a change.

Changing a route therefore requires one reviewed OpenAPI change and regenerated artifacts in the same commit. Do not hand-edit generated files. Breaking path, method, authentication, or payload changes require a versioning/deprecation decision; adding an operation requires authorization, isolation, error, and browser contract tests before advertisement.

## Publication closeout

The remaining contract work is operational rather than structural: bind generated operation-specific client calls to these component types, run Go handlers and browser clients against schema fixtures, classify backward-compatible versus breaking diffs in CI, decide which operations are public partner surface versus first-party-only, and publish the resulting sanitized document. Provider payloads such as Stripe events and standards payloads such as WebAuthn remain bounded extension envelopes rather than frozen copies of third-party schemas.
