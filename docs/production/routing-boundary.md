# Global-to-cell routing boundary

Status: executable signed route-context, global app-router, bounded Account Directory cache, cell app-api Work reads/commands, private usage-admission broker, shared replay receipts, placement-generation rejection, and Kubernetes reference topology implemented. Internal TLS/workload identity remains.

## Why this boundary exists

Spyglass serves many Accounts from shared stateless containers and shared cell databases. A browser session is global, but ordinary Work, Agents, Finance, Marketing, and Knowledge records belong to one cell. The global Account service must not gain cell database access, and a cell service must not trust a browser-supplied Account ID, slug, role, package, or placement.

The `app-router` therefore authenticates the system-wide session against the global database, rechecks Account Membership/state/role/placement/entitlements, selects an allowlisted cell service, and signs a narrow internal request. The cell `app-api` verifies and consumes that authority before opening an Account-scoped transaction.

## Signed envelope

The route token is a compact three-part Base64URL envelope with a strict versioned header, strict JSON claims, and HMAC-SHA-256 signature. It is deliberately application-owned rather than a general identity JWT.

Claims include:

- Issuer, exact cell audience, signing key ID, issued time, and expiry.
- Unique UUID route request ID and, for mutations, a distinct UUID logical operation ID copied from `Idempotency-Key`.
- Immutable Account ID, assigned cell ID, and placement generation.
- Actor kind and system-wide actor ID; User routes also carry the verified Membership role.
- Entitlement version and, when the allowlisted route belongs to a Feature Package, only that package's effective mode, limits, and limit policies.
- Exact uppercase HTTP method, escaped request target including query, SHA-256 body digest, and a canonical digest of semantic `Content-Type`, `Idempotency-Key`, and `If-Match` headers when present.

Tokens live for 20 seconds by default and may never exceed 30 seconds. Cell verification rejects unknown key IDs, unsupported version/type/algorithm, unknown JSON fields, invalid claims, future issue times, expiry, signature changes, audience mismatch, and any method/target/body mismatch.

Request binding means a valid token for `GET .../context` cannot authorize `POST .../work-items`, another Account path, a changed query/body, a different idempotency key, or a changed expected version. The router generates the digests after enforcing its request limit, then sends those exact bytes and semantic headers. It does not forward browser cookies or Authorization headers to the cell.

## Shared replay and placement defense

Cryptographic verification happens before database access. The cell then uses the signed Account ID to open a transaction through `CellPool`, which sets transaction-local `app.account_id`. In that transaction it:

1. Loads only the RLS-visible Account namespace.
2. Requires the signed placement generation to equal the current cell generation.
3. Permits reads while a namespace is `active`, `draining`, or `frozen`; writes require `active`.
4. Inserts the UUID request into `route_context_receipts` under a composite Account key.
5. Rejects a duplicate as `route_replay` across every app-api replica sharing the cell database.

Expired receipts are removed inside Account scope. The serving database role needs bounded delete authority on this RLS-protected technical table; it still must not own tables or have `BYPASSRLS`.

## Executable processes

`spyglass app-router` owns global session authentication, Account authorization, least-authority token issuance, Account Directory routing, header stripping, bounded proxy bodies, response bounds, no-redirect behavior, and exact-origin checks for mutations. Current allowlisted resources are the Account context probe and the reserved Work path family.

The directory source joins the Account assignment to the operational cell registry. Each cached route contains the cell ID, placement generation, directory state, cell state, region, and an operator-managed internal origin. Entries are loaded on demand, coalesced per Account, retained for 30 seconds by default, and evicted least-recently-used at a default 10,000-entry bound. A cache hit is valid only when its cell and generation exactly match the independently authorized Account context. A mismatch bypasses the cached value and refreshes once from the source; a continuing mismatch fails closed.

Only `active`, `draining`, and `frozen` Account assignments on `active` or `draining` cells are routable. Moving or disabled assignments, disabled cells, missing origins, unsafe origins, and expired entries whose refresh fails never produce a cell request. Production origins must be HTTPS origins with no credentials, path, query, or fragment. Plain HTTP is available only when the process is explicitly in development mode. `/health/status` exposes capacity, entry/expiry counts, oldest age, hits, misses, refresh failures, and evictions without revealing Account IDs, cell IDs, or origins.

`spyglass app-api` owns one cell database pool, route verification keyring, shared replay receipts, placement checks, and cell API transport. It has no global database credential. Executable routes are:

```text
GET /api/v1/accounts/{accountID}/context
GET /api/v1/accounts/{accountID}/work-items
GET /api/v1/accounts/{accountID}/work-items/summary
GET /api/v1/accounts/{accountID}/work-items/{itemID}
GET /api/v1/accounts/{accountID}/work-items/{itemID}/children
POST /api/v1/accounts/{accountID}/work-items
POST /api/v1/accounts/{accountID}/work-items/{itemID}/transitions
PATCH /api/v1/accounts/{accountID}/work-items/{itemID}/assignment
```

The context probe proves the base global-session-to-cell-RLS path. Work routes add an exact Work package claim, translate verified claims back into the shared application authorization contract, and return explicit customer-safe DTOs. Mutations require the signed operation ID, and transition/assignment require the signed weak ETag. The global router and Account API do not query Work records.

`spyglass admission-api` is a private global process for narrow usage admission. A cell presents the same short-lived route proof; the broker re-verifies its cell audience, Work mutation path, operation ID, and enabled package claim, then rechecks current global access. It can read only the access projection and mutate usage counters/reservations. It never receives cell SQL authority or Work content. Replays are safe because reservation and compensation keys are idempotent and finalized keys cannot be resurrected. Network policy permits only app-api pods to reach this surface; internal TLS/workload identity remains a promotion gate.

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
| Missing/unroutable directory entry | `routing_unavailable`; no dynamic URL fallback |
| Directory disagrees with authorized cell/generation | One source refresh, then `routing_unavailable`; no cell request |
| Directory source unavailable | An exact unexpired hit may be used; an expired, missing, or mismatched entry fails closed |
| Cell timeout/redirect/oversized response | Bounded gateway failure |
| Token expired/altered/wrong audience | Cell returns `invalid_route_context` |
| Duplicate request UUID | Cell returns `route_replay` |
| Placement generation changed | Cell returns `stale_route`; caller refreshes global placement |
| Account draining/frozen write | Cell returns `account_unavailable`; safe reads may continue |
| Missing/changed Idempotency-Key or If-Match | Router/cell rejects the command before a business write |
| Global admission denied/unavailable | No cell create/reopen; stable capacity/access failure |
| Cell commit outcome unknown after reserve | Reservation remains active; caller retries the exact operation |
| Cell database unavailable | Readiness fails and the request returns a bounded service failure |

The router never guesses another cell, follows redirects, or falls back to querying business data itself.

## Remaining production work

1. Add internal TLS/workload identity between router and cell in addition to application signatures, with certificate rotation and network-policy enforcement.
2. Add workload identity at admission-api as well as router-to-cell, including certificate/key rotation and explicit denial telemetry.
3. Add route receipt retention/partitioning and metrics for replay, stale generation, verification failure, latency, cell saturation, and cache age.
4. Add two-cell PostgreSQL integration tests, key-rotation canaries, router failover, cell/admission failover, and load/fairness evidence. Unit coverage already proves stale-cache refresh, bounded eviction, coalesced misses, and refusal to use expired entries during source failure.

## Evidence and limits

Unit tests cover directory cache hits, mismatch refresh, expiry under source failure, unsafe/unhealthy routes, bounded LRU eviction, concurrent miss coalescing, body and semantic-header binding, signature alteration, key rotation, origin rejection, credential stripping, exact allowlisted paths, successful router-to-cell traversal, replay, altered Account paths, Work package translation, read-only package behavior, strict query/command parsing, safe Work views, assignment spoofing, and cross-Account Work paths. The PostgreSQL 17 contract proves replay uniqueness, stale placement rejection, draining-write rejection, draining-read acceptance, RLS, routed broker-backed Work creation/compensation through split roles, Work query isolation, and migration replay through non-owner roles.

The manifests remain review-only. They have no literal secrets or real endpoints and the default-deny policy still requires environment overlays for ingress, global/cell database egress, TLS identity, monitoring, and image digests. An edge overlay must route the more-specific `/api/v1/accounts/{accountID}/work-items...` family to `app-router` while private HTML and global control routes remain on `account-api`.
