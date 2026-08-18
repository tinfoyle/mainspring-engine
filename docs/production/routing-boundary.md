# Global-to-cell routing boundary

Status: executable signed route-context, global app-router, cell app-api probe, shared replay receipts, placement-generation rejection, and Kubernetes reference topology implemented. Directory caching and production Work routes remain.

## Why this boundary exists

Spyglass serves many Accounts from shared stateless containers and shared cell databases. A browser session is global, but ordinary Work, Agents, Finance, Marketing, and Knowledge records belong to one cell. The global Account service must not gain cell database access, and a cell service must not trust a browser-supplied Account ID, slug, role, package, or placement.

The `app-router` therefore authenticates the system-wide session against the global database, rechecks Account Membership/state/role/placement/entitlements, selects an allowlisted cell service, and signs a narrow internal request. The cell `app-api` verifies and consumes that authority before opening an Account-scoped transaction.

## Signed envelope

The route token is a compact three-part Base64URL envelope with a strict versioned header, strict JSON claims, and HMAC-SHA-256 signature. It is deliberately application-owned rather than a general identity JWT.

Claims include:

- Issuer, exact cell audience, signing key ID, issued time, and expiry.
- Unique UUID request ID.
- Immutable Account ID, assigned cell ID, and placement generation.
- Actor kind and system-wide actor ID; User routes also carry the verified Membership role.
- Entitlement version and, when the allowlisted route belongs to a Feature Package, only that package's effective mode, limits, and limit policies.
- Exact uppercase HTTP method, escaped request target including query, and SHA-256 body digest.

Tokens live for 20 seconds by default and may never exceed 30 seconds. Cell verification rejects unknown key IDs, unsupported version/type/algorithm, unknown JSON fields, invalid claims, future issue times, expiry, signature changes, audience mismatch, and any method/target/body mismatch.

Request binding means a valid token for `GET .../context` cannot authorize `POST .../work-items`, another Account path, a changed query, or a changed body. The router generates the body digest after enforcing its request limit, then sends those exact bytes. It does not forward browser cookies or Authorization headers to the cell.

## Shared replay and placement defense

Cryptographic verification happens before database access. The cell then uses the signed Account ID to open a transaction through `CellPool`, which sets transaction-local `app.account_id`. In that transaction it:

1. Loads only the RLS-visible Account namespace.
2. Requires the signed placement generation to equal the current cell generation.
3. Permits reads while a namespace is `active`, `draining`, or `frozen`; writes require `active`.
4. Inserts the UUID request into `route_context_receipts` under a composite Account key.
5. Rejects a duplicate as `route_replay` across every app-api replica sharing the cell database.

Expired receipts are removed inside Account scope. The serving database role needs bounded delete authority on this RLS-protected technical table; it still must not own tables or have `BYPASSRLS`.

## Executable processes

`spyglass app-router` owns global session authentication, Account authorization, least-authority token issuance, fixed cell routing, header stripping, bounded proxy bodies, response bounds, no-redirect behavior, and exact-origin checks for mutations. Current allowlisted resources are the Account context probe and the reserved Work path family.

`spyglass app-api` owns one cell database pool, route verification keyring, shared replay receipts, placement checks, and cell API transport. The first executable route is:

```text
GET /api/v1/accounts/{accountID}/context
```

This probe proves the complete global-session-to-cell-RLS path without pretending Work transport is finished. It returns safe Account routing facts and no customer business record.

## Key rotation

The router loads exactly one active signing key ID/key. Each cell loads a keyring. Rotation order is:

1. Add the new verification key to every cell while retaining the old key.
2. Confirm all cells can verify a canary signed with the new key.
3. Switch routers to sign with the new key ID.
4. Wait longer than the maximum 30-second token lifetime plus clock-skew allowance.
5. Remove the old verification key from cells.

Keys are standard Base64 encodings of exactly 32 random bytes. They belong in a managed secret system, not ConfigMaps, manifests, logs, URLs, or repository files. The route signing key is not an identity-session, notification-encryption, Stripe, or network-actor key and must never be reused for those purposes.

## Failure behavior

| Condition | Outcome |
| --- | --- |
| Missing/invalid global session | Router returns `authentication_required`; no token or cell request |
| Membership/package/role denial | Stable authorization problem; no cell request |
| Unknown configured cell | `cell_unavailable`; no dynamic URL fallback |
| Cell timeout/redirect/oversized response | Bounded gateway failure |
| Token expired/altered/wrong audience | Cell returns `invalid_route_context` |
| Duplicate request UUID | Cell returns `route_replay` |
| Placement generation changed | Cell returns `stale_route`; caller refreshes global placement |
| Account draining/frozen write | Cell returns `account_unavailable`; safe reads may continue |
| Cell database unavailable | Readiness fails and the request returns a bounded service failure |

The router never guesses another cell, follows redirects, or falls back to querying business data itself.

## Remaining production work

1. Replace static `SPYGLASS_CELL_ROUTES` lookup with a bounded, observable directory cache whose entries include cell endpoint, health, and generation policy; global DB outage may use only unexpired cache entries.
2. Add internal TLS/workload identity between router and cell in addition to application signatures, with certificate rotation and network-policy enforcement.
3. Define the Work HTTP command/query schema, convert signed package authority into the Work application authorizer, and coordinate global usage admission without adding a global database credential to app-api.
4. Add route receipt retention/partitioning and metrics for replay, stale generation, verification failure, latency, cell saturation, and cache age.
5. Add two-cell integration tests, stale-cache refresh, key-rotation canaries, router failover, cell failover, bounded global outage, and load/fairness evidence.
6. Put the Account context and Work routes behind the private Spyglass shell; keep the prototype's queue interaction language while using the production contracts.

## Evidence and limits

Unit tests cover request binding, expiry, signature alteration, unknown keys, key rotation, malformed authority, origin rejection, credential stripping, successful router-to-cell traversal, replay, and altered Account paths. The PostgreSQL 17 contract proves replay uniqueness, stale placement rejection, draining-write rejection, draining-read acceptance, RLS, and migration replay through a non-owner role.

The manifests remain review-only. They have no literal secrets or real endpoints and the default-deny policy still requires environment overlays for ingress, global/cell database egress, TLS identity, monitoring, and image digests. The Account probe is not a substitute for Work API/UI completion.
