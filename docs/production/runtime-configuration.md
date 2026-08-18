# Production Runtime Configuration

- Status: executable Phase 2 account, global router, cell API, private admission API, route rotation canary, route-receipt retention, billing, notification, entitlement-rollout, Account lifecycle, Work reconciliation, migration, Catalog/Work release/passkey rotation operators, and reviewed Account erasure processes
- Binary: `spyglass`
- Process modes: `account-api`, `app-router`, `app-api`, `admission-api`, `route-receipt-worker`, `billing-worker`, `notification-worker`, `entitlement-worker`, `account-lifecycle-worker`, `work-reconciler`, `runner-controller`, `runner-broker`, `model-gateway`, `agent-dispatch-worker`, `agent-projection-worker`, one-shot `runner-invocation`/`route-canary`/`agent-queue-admin`/`work-release-admin`/`account-erasure-admin`/`passkey-admin`/`catalog-admin`/`migrate`, and explicit local-only `development`

## Process ownership

| Mode | Owns | Does not own |
|---|---|---|
| `account-api` | Signup, password/passkey login and recovery, session security/reauthentication, Account selection, invitations, encrypted notification enqueueing, local billing reads, Checkout/Portal creation, signed Stripe webhook acceptance, private browser shell | SMTP delivery, billing event projection, reconciliation polling, Account business workloads |
| `app-router` | Authenticate the global session, recheck Account authority, resolve an eligible directory cell, issue request-bound route context, and proxy bounded workload-authenticated Account API traffic | Cell database access, business-record queries, arbitrary destinations |
| `app-api` | Verify and consume route context, reject replay/stale placement, and execute Account-owned use cases through one shared cell pool | Global database, session cookies, Account/Billing mutation, arbitrary cell routing |
| `admission-api` | Re-verify routed Work operation proofs, reauthorize current global access, and reserve/compensate governed capacity | Cell database, Work content, browser sessions, terminal Work release, Stripe or SMTP operations |
| `route-receipt-worker` | Lease identifier-only per-Account cleanup schedules and perform bounded replay-receipt deletion inside Account RLS | Serving traffic, global data, customer Work, cross-Account receipt reads, Stripe or SMTP operations |
| `route-canary` | One candidate-key and candidate-certificate probe through a dedicated internal Account's protected cell path or admission verifier | Database credentials, customer Accounts, serving traffic, secret logging, usage mutation, or automatic cutover |
| `billing-worker` | Leased Stripe inbox processing, current Subscription retrieval, transactional grant/snapshot projection, reconciliation queue | Browser/API traffic, raw webhook acceptance, customer business work |
| `notification-worker` | Leased encrypted identity and Account-ownership notification delivery, bounded per-recipient retries, terminal dead-letter state | Browser/API traffic, identity/Account mutation, billing credentials, customer business work |
| `entitlement-worker` | Bounded existing-Account Catalog rollout seeding, leased free-plan recomputation, immutable changed-access snapshots, and drift repair | Catalog publication decisions, paid-grant mutation, Stripe or SMTP operations, customer business work |
| `work-reconciler` | Lease identifier-only terminal Work release jobs, idempotently release global capacity, and checkpoint the matching Account-scoped Work row | Serving traffic, Work content reads, capacity reservation, package mutation, Stripe or SMTP operations |
| `model-gateway` | Translate one bounded provider-neutral model step, enforce strict sequential-tool/output controls, and normalize provider result/usage | Account database, browser/session identity, Kubernetes API, runner identity, business tools |
| `agent-dispatch-worker` | Lease identifier-only Agent invocations, read their immutable Account-RLS plans, and provision exact encrypted runner requests | Browser/session authority, global database, provider credentials, Agent configuration mutation, plaintext queue storage |
| `agent-projection-worker` | Lease terminal Agent results, decrypt Pod-bound envelopes, validate results, and atomically project Account-visible outcomes | Browser/session authority, provider credentials, unencrypted result persistence, arbitrary Agent mutation |
| `agent-queue-admin` | One audited bounded inspection or exact-target requeue of one cell's Agent dispatch/projection dead letters | Serving traffic, direct queue/content/exchange access, provider credentials, cross-cell discovery |
| `work-release-admin` | One audited, bounded inspection or exact-target requeue of Work release dead letters | Serving traffic, customer Work content, direct queue table access, global capacity mutation |
| `account-erasure-admin` | One audited prepare, inspect, independent approval, pre-execution cancellation, leased cross-store execution, or signed restore replay of a retained closed Account | Serving traffic, automatic approval, arbitrary SQL, cross-cell fallback, external-store deletion |
| `passkey-admin` | One audited key-version inspection or bounded credential/ceremony envelope re-encryption batch | Serving traffic, User/contact reads, password/session authority, automatic key retirement |
| `catalog-admin` | One audited draft, mapping, review, approval, publish, retire, or rollback action | Serving traffic, automatic publication decisions, customer data mutation |
| `development` | Memory-backed local identity and browser journey | Persistent data, outbound email, paid Stripe operations |
| `migrate` | One embedded, immutable migration target against one database | Serving traffic, background work, automatic target selection |

The account API and workers share no in-memory state. Multiple replicas coordinate through PostgreSQL row leases and unique constraints.

## Common production values

| Environment variable | Consumers | Meaning |
|---|---|---|
| `SPYGLASS_DATABASE_URL` | Persistent processes except `work-reconciler` | Workload-specific PostgreSQL connection string: global for control-plane modes and one cell database for `app-api` or `route-receipt-worker` |
| `SPYGLASS_STRIPE_SECRET_KEY` | Account API, billing worker | Environment-specific `sk_test_` or `sk_live_` key |
| `SPYGLASS_STRIPE_MODE` | Account API, billing worker | Exact `test` or `live` mode; must match the key |
| `SPYGLASS_STRIPE_API_VERSION` | Account API, billing worker | Optional deliberate override; defaults to the compiled, tested pin |
| `SPYGLASS_NOTIFICATION_ENCRYPTION_KEY` | Account API, notification worker | Standard Base64 encoding of exactly 32 random bytes |
| `SPYGLASS_NETWORK_ACTOR_KEY` | Account API | Standard Base64 encoding of exactly 32 random bytes used only for keyed request-actor hashing |
| `SPYGLASS_PASSKEY_ENCRYPTION_KEYS` | Account API, passkey admin | Comma-separated `positive-version=standard-base64-key` keyring; every key is exactly 32 bytes |
| `SPYGLASS_PASSKEY_ENCRYPTION_ACTIVE_VERSION` | Account API, passkey admin | Positive version present in the keyring; new and updated passkey envelopes use only this version |
| `SPYGLASS_OPERATOR_AUTH_ISSUER` | All administrator processes | Exact external operator identity-plane issuer |
| `SPYGLASS_OPERATOR_AUTH_VERIFY_KEYS` | All administrator processes | Comma-separated `key-id=standard-base64` Ed25519 public-key rotation set |
| `SPYGLASS_OPERATOR_AUTHORIZATION` | All administrator processes | Short-lived signed, action/environment/reason/scope-bound authorization envelope |
| `SPYGLASS_MAX_DATABASE_CONNS` | Persistent processes except `work-reconciler` | Positive per-process pool cap with a workload-specific default |
| `SPYGLASS_OTEL_TRACES_ENDPOINT` | Traced modes except `runner-invocation` | Optional exact HTTPS OTLP/HTTP `/v1/traces` endpoint; when set, every trace variable and the workload mTLS files become mandatory |
| `SPYGLASS_OTEL_TRACE_SAMPLE_RATIO` | All traced modes | Explicit finite root sampling ratio greater than `0` and at most `1` |
| `SPYGLASS_TRACE_ACCOUNT_HASH_KEY` | All traced modes | Standard Base64 encoding of exactly 32 random bytes used only for HMAC-pseudonymous Account trace correlation |
| `SPYGLASS_ENVIRONMENT` | All traced modes and administrator processes | Reviewed bounded environment identity |

Database connection limits are per replica. Environment overlays must ensure the replica maximum multiplied by the pool cap fits the managed PostgreSQL connection budget.

## Account API values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_HTTP_ADDRESS` | Optional listen address; defaults to `:8080` |
| `SPYGLASS_APP_ORIGIN` | Exact HTTPS Spyglass application origin |
| `SPYGLASS_PUBLIC_ORIGIN` | Exact HTTPS Infinite Ocean public origin |
| `SPYGLASS_PASSKEY_RP_ID` | Exact WebAuthn relying-party domain for the application origin; no scheme, port, or path |
| `SPYGLASS_STRIPE_WEBHOOK_SECRET` | Endpoint-specific `whsec_` secret |
| `SPYGLASS_TRUSTED_PROXY_CIDRS` | Optional comma-separated ingress/load-balancer networks allowed to supply `X-Forwarded-For`; empty trusts no proxy |
| `SPYGLASS_CATALOG_REFRESH_INTERVAL` | Optional positive Go duration for effective publication polling; defaults to `5s` |

The account API has no SMTP configuration. It serializes registration, invitation, and credential-recovery messages, encrypts each envelope with AES-256-GCM, and persists only ciphertext, a nonce, and key version in the durable outbox. Associated data binds the ciphertext to the outbox ID and notification kind. Plaintext tokens are never written to the outbox or application logs.

Passkey credentials and server-side ceremony data use a separate AES-256-GCM keyring and record-bound associated data; the unencrypted credential ID exists only for WebAuthn lookup and counter fencing. New writes use the configured active version while retained versions are decrypt-only. Live rotation uses `passkey-admin inspect|reencrypt` and the procedure in [passkey-key-rotation.md](passkey-key-rotation.md); an old key is never removed merely because a rollout completed.

Passkey recovery codes need no encryption setting because plaintext is returned once and never persisted. The account API stores only domain-separated hashes, atomically consumes one code after recent password authentication, and binds the resulting ten-minute replacement grant to the current live User session. Rotating the set invalidates all previous codes and grants. These grants are identity-only and cannot satisfy Account or operator authorization.

Credential-recovery initiation always returns the same accepted response regardless of whether the email is registered, malformed, or throttled. Unknown and throttled identifiers enqueue encrypted discard work so the public request does similar cryptographic and database work without sending mail. Challenges store only a SHA-256 token hash, expire after 30 minutes, and are single use. Completion changes the Argon2id credential, advances the User security version, revokes every session, consumes all pending recovery challenges, and appends a `credential_recovered` security event in one PostgreSQL transaction. Identifier and network-actor throttles are both durable.

Anonymous login and recovery budgets are shared across replicas in PostgreSQL. The actor key is HMAC-SHA-256 over the canonical socket/client address, so raw addresses are not stored in limiter state and cannot be recovered through an offline hash dictionary. Login permits 60 attempts per actor per 15 minutes; recovery permits 10 per actor per hour. A denied login still performs the identity lookup and password verification before returning the generic credential failure. A denied recovery request performs token generation and encrypted discard enqueueing before returning the generic accepted response.

Forwarding headers are ignored unless the immediate socket peer belongs to `SPYGLASS_TRUSTED_PROXY_CIDRS`. Behind trusted proxies, Spyglass walks `X-Forwarded-For` from right to left and selects the first untrusted hop, preventing a client-supplied leftmost value from becoming authoritative. Malformed trusted forwarding chains fail closed. Environment overlays must set only the exact ingress or load-balancer networks they operate; broad private-network ranges are not safe defaults.

Each account-api replica holds one immutable Catalog snapshot. It polls for the newest effective `published_at` and atomically replaces the snapshot, including deliberate rollback to a lower version. A request or registration completion reads one snapshot, so a concurrent refresh cannot mix versions inside that operation.

## App router and cell API values

| Environment variable | Consumers | Requirement |
|---|---|---|
| `SPYGLASS_ROUTE_ISSUER` | App router, app API, route canary | Exact shared issuer name, normally `spyglass-app-router` |
| `SPYGLASS_ROUTE_SIGNING_KEY_ID` | App router, route canary | Active or candidate non-secret key identifier |
| `SPYGLASS_ROUTE_SIGNING_KEY` | App router, route canary | Standard Base64 encoding of exactly 32 random secret bytes |
| `SPYGLASS_ROUTE_VERIFY_KEYS` | App API, admission API | Comma-separated `key-id=base64-key` keyring containing active and retained rotation keys |
| `SPYGLASS_ROUTE_CONTEXT_TTL` | App router | Optional positive duration; defaults to `20s` and has a hard `30s` maximum |
| `SPYGLASS_TOOL_CONTEXT_ISSUER` | App router, runner broker | Exact broker tool-dispatch issuer, normally `spyglass-runner-broker`; distinct from the route issuer |
| `SPYGLASS_TOOL_CONTEXT_VERIFY_KEYS` | App router | Comma-separated `key-id=base64-key` keyring for broker tool proofs only |
| `SPYGLASS_DIRECTORY_CACHE_TTL` | App router | Optional positive duration, at most `5m`; defaults to `30s` |
| `SPYGLASS_DIRECTORY_CACHE_CAPACITY` | App router | Optional positive Account-route bound, at most 1,000,000; defaults to 10,000 |
| `SPYGLASS_CELL_ID` | App API, route canary | Exact cell identity used as token audience and deployment identity |
| `SPYGLASS_SESSION_COOKIE_NAME` | App router | Optional; defaults to `__Host-spyglass_session` |
| `SPYGLASS_MAX_REQUEST_BODY_BYTES` | App API | Optional positive limit up to 16 MiB; defaults to 1 MiB |
| `SPYGLASS_WORK_ADMISSION_ORIGIN` | App API | Exact private admission-api origin; HTTPS is the fail-closed default |
| `SPYGLASS_WORKLOAD_CERT_FILE` | App router, app API, admission API, route canary | PEM workload certificate path; app-api certificates need server and client usage |
| `SPYGLASS_WORKLOAD_KEY_FILE` | App router, app API, admission API, route canary | PEM private-key path readable only by the workload |
| `SPYGLASS_WORKLOAD_CA_FILE` | App router, app API, admission API, route canary | PEM trust-bundle path for the environment workload CA rotation set |
| `SPYGLASS_WORKLOAD_CLIENT_IDENTITIES` | App API, admission API | Comma-separated exact SPIFFE URI identities permitted on private endpoints |

Cell route origins are operational data in the global `cells` registry, not process configuration. Before assigning Accounts, an operator must set each cell's exact internal HTTPS origin whose DNS name appears in that cell server certificate; origins may not contain credentials, paths, queries, fragments, or control characters. The router joins this registry to `account_directory`, caches only eligible assignments, and requires an exact cell/generation match with authorization. `SPYGLASS_ENV=development` is the only plain-HTTP and non-workload-TLS escape hatch.

The signing and verification keys follow the add-verifier, switch-signer, wait-for-expiry, remove-old-key sequence in [routing-boundary.md](routing-boundary.md). Tool-context keys follow the same overlap sequence but are a separate credential family and have a maximum 15-second lifetime. The app-router database credential is global and needs read-only access to Account routing/current-entitlement state plus insert-only access to `tool_context_receipts`; it cannot read cell schemas. Tool receipts contain identifiers and digests only, are consumed before routing, and cascade away during Account erasure. The app-api credential is cell-local and cannot read global Users, Memberships, Entitlements, Billing, or sessions. Every routed mutation requires a UUID `Idempotency-Key`; transition and assignment require `If-Match`. Those semantic headers are included in the signed request binding.

## Admission API values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_DATABASE_URL` | Required narrow global admission credential |
| `SPYGLASS_ROUTE_ISSUER` | Must exactly match app-router and app-api |
| `SPYGLASS_ROUTE_VERIFY_KEYS` | Same rotating verification keyring used by cells |
| `SPYGLASS_ADMISSION_CELL_IDS` | Comma-separated exact cell IDs whose route audiences may request admission |
| `SPYGLASS_ADMISSION_MAX_REQUEST_BODY_BYTES` | Optional positive bound up to 1 MiB; defaults to 64 KiB |
| `SPYGLASS_MAX_DATABASE_CONNS` | Positive per-replica global pool cap; defaults to 10 |

Admission-api is private and accepts neither browser cookies nor bearer identity. Its database role needs `SELECT` on Accounts, Memberships, and entitlement snapshots; `SELECT/INSERT/UPDATE` on usage counters and reservations; and `EXECUTE` on `spyglass_lock_account_entitlement_version(uuid)`. It must not receive Account `UPDATE`, User/session/Billing access, or any cell credential. The lock function has no PUBLIC execute grant and provides only the entitlement-version row lock needed for fencing.

App-api presents the original short-lived route proof. Admission-api re-verifies its signature, cell audience, Work mutation path, operation ID, enabled package claim, and exact request binding before current global authorization. The reference NetworkPolicy permits only app-api pods to connect. Production app-api accepts only an HTTPS broker origin and presents its workload certificate; admission-api additionally requires that certificate's exact configured SPIFFE URI. Plain HTTP is available only to an explicitly selected `SPYGLASS_ENV=development` process.

Internal servers require TLS 1.3. They reload the certificate, key, and CA bundle on each new handshake; clients reload those files for each new pooled connection. `/health/*` remains HTTPS but does not require a client certificate so Kubernetes probes work. Every business path requires a verified allowed workload identity. Trust rotation adds the new CA to the bundle before issuing new leaves, waits for new connections and rollout evidence, then removes the old CA; emergency revocation also terminates existing pods/connections.

## Route rotation canary values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_ROUTE_CANARY_TARGET` | Required exact `cell` or `admission` |
| `SPYGLASS_ROUTE_CANARY_ORIGIN` | Required exact private HTTPS origin for the selected target |
| `SPYGLASS_ROUTE_CANARY_ACCOUNT_ID` | Required dedicated internal canary Account UUID; never a customer Account |
| `SPYGLASS_ROUTE_CANARY_PLACEMENT_GENERATION` | Required current positive cell placement generation |
| `SPYGLASS_ROUTE_CANARY_ENTITLEMENT_VERSION` | Required current positive entitlement version |
| `SPYGLASS_CELL_ID` | Required exact audience cell ID |
| `SPYGLASS_ROUTE_ISSUER` | Required issuer matching target verifier configuration |
| `SPYGLASS_ROUTE_SIGNING_KEY_ID` | Required candidate key ID |
| `SPYGLASS_ROUTE_SIGNING_KEY` | Required candidate standard-Base64 32-byte signing key |
| `SPYGLASS_WORKLOAD_CERT_FILE` | Candidate client certificate with the exact allowed app-router or app-api SPIFFE identity |
| `SPYGLASS_WORKLOAD_KEY_FILE` | Candidate certificate private key |
| `SPYGLASS_WORKLOAD_CA_FILE` | Trust bundle containing the destination server issuer during the overlap window |
| `SPYGLASS_ROUTE_CANARY_TIMEOUT` | Optional duration from `1s` through `30s`; defaults to `10s` |

The process is one-shot and has no development/plain-HTTP mode or database credential. A cell target traverses the ordinary Account context verifier, RLS namespace, and replay receipt. An admission target verifies its per-cell keyring without touching usage. The only success log fields are target, cell ID, key ID, and placement generation. See [route-rotation-operations.md](route-rotation-operations.md) for provisioning, rollout, rollback, and retirement steps.

## Shared worker health and metrics

Every long-running worker serves process liveness at `GET /health/live`, bounded dependency readiness at `GET /health/ready`, and a no-store operational snapshot at `GET /health/status` when that worker has queue or aggregate counters. `GET /metrics` renders the same numeric, content-free snapshot in Prometheus text format and fails unavailable when the restore gate, database, or status query is not ready. The renderer accepts only bounded static worker/field labels; it has no Account, User, request, invocation, email, provider object, or customer-content label path.

The Agent dispatch, Agent projection, and runner controller endpoints additionally publish the exact gauges `spyglass_agent_dispatch_ready`, `spyglass_agent_projection_ready`, and `spyglass_runner_ready`. Environment monitoring scrapes the health port and the custom/external metrics adapter projects those series into the HPA API. These are current global/cell backlog snapshots repeated by each replica, so adapters must preserve the reference HPA `AverageValue` semantics and alert when a series disappears. Billing, notification, entitlement, Account lifecycle, Work reconciliation, and route-receipt workers expose aggregate processing/failure or queue-state fields through `spyglass_worker_status{worker,field}` without customer-derived labels.

Every HTTP service also exposes `GET /metrics` on its service port. The endpoint reports `spyglass_http_in_flight`, `spyglass_http_requests_total`, and `spyglass_http_request_duration_seconds` with only static service names, a bounded method set, registered `ServeMux` route patterns, and response status classes. Unmatched routes collapse to `unmatched`, non-standard methods collapse to `OTHER`, and raw paths, Account IDs, request bodies, query strings, and headers are never labels. Monitoring must scrape this endpoint over the same trusted network boundary as the service; it is operational telemetry, not a public product endpoint.

When `SPYGLASS_OTEL_TRACES_ENDPOINT` is configured, the process additionally exports bounded OTLP/HTTP traces through the rotating workload mTLS certificate. Server spans contain only static release/environment/cell identity, bounded method and route template, response status, and an HMAC-pseudonymous Account reference. Principal clients propagate sanitized W3C `traceparent` across Infinite Ocean-owned boundaries; inbound `tracestate` is discarded, while external-provider calls remain child spans but strip `traceparent`, `tracestate`, and baggage. Raw destinations, bodies, headers, identifiers, and error strings are never span attributes. Exporter queues, batches, timeouts, retries, graceful flush, required dashboards, and staging acceptance are defined in [OpenTelemetry tracing and observability operations](observability-operations.md).

## Billing worker values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_BILLING_POLL_INTERVAL` | Optional positive Go duration; defaults to `1s` |
| `SPYGLASS_HEALTH_ADDRESS` | Optional health listen address; defaults to `:8081` |

`GET /health/live` reports process liveness. `GET /health/ready` performs a bounded PostgreSQL ping. The worker stops claiming new work on termination and the process allows the current database/provider request to end within the pod termination grace period.

## Notification worker values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_APP_ORIGIN` | Exact HTTPS Spyglass application origin used to construct identity links |
| `SPYGLASS_SMTP_ADDRESS` | Implicit-TLS SMTP host and port, normally port 465 |
| `SPYGLASS_SMTP_SERVER_NAME` | TLS certificate server name |
| `SPYGLASS_SMTP_FROM_ADDRESS` | Bare sender email address |
| `SPYGLASS_SMTP_FROM_NAME` | Optional display name; defaults to `Infinite Ocean` |
| `SPYGLASS_SMTP_USERNAME`, `SPYGLASS_SMTP_PASSWORD` | Optional as a pair for authenticated relays |
| `SPYGLASS_NOTIFICATION_POLL_INTERVAL` | Optional positive Go duration; defaults to `1s` |
| `SPYGLASS_HEALTH_ADDRESS` | Optional health listen address; defaults to `:8081` |

Delivery requires TLS 1.2 or newer. Workers claim one row with `FOR UPDATE SKIP LOCKED`, use a two-minute lease for crash recovery, refuse to send expired identity links, and retry delivery failures with bounded exponential backoff. The twelfth failed attempt enters terminal `dead_letter` state. A successfully accepted SMTP message is acknowledged as delivered; because delivery and PostgreSQL acknowledgement cannot share a transaction, a crash in between can produce a duplicate message. Identity links remain single-use, which makes that at-least-once boundary safe for credentials.

Key rotation must retain the currently configured key until every row encrypted with its version has reached a terminal state or been re-encrypted. This slice records key versions but loads one active version; introducing a multi-version keyring is required before rotating a live environment key.

## Entitlement worker values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_ENTITLEMENT_POLL_INTERVAL` | Optional positive Go duration; defaults to `1s` |
| `SPYGLASS_ENTITLEMENT_SEED_BATCH` | Optional number of Accounts added per seeding transaction; defaults to `100` and must be between 1 and 1000 |
| `SPYGLASS_HEALTH_ADDRESS` | Optional health listen address; defaults to `:8081` |

Publishing a Catalog creates a durable rollout in the same transaction as the publication and operator audit event. Entitlement workers cursor through affected Accounts in bounded batches, claim Account work with `FOR UPDATE SKIP LOCKED` and a two-minute recovery lease, and replace only grants whose source is `free_plan`. Subscription, trial, promotion, grandfathered, and support-override grants remain intact.

Each Account records the Catalog version last reconciled. A recomputation advances the Account entitlement version and appends an immutable snapshot only when effective package access or limits changed; otherwise only the reconciliation marker advances. Periodic drift detection creates a repair rollout for late Accounts and work missed after a crash. Invalid Catalog content fails terminally, while transient failures retry with bounded exponential backoff and dead-letter on the twelfth attempt. A failed rollout suppresses automatic repair for that Catalog version until an operator publishes a corrected version, preventing an unrecoverable row from creating an infinite retry cycle.

## Account lifecycle worker values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_DATABASE_URL` | Required constrained global credential for Accounts, active owner Membership checks, projected subscriptions/checkouts, closure requests, and lifecycle events |
| `SPYGLASS_MAX_DATABASE_CONNS` | Optional positive pool cap; defaults to `5` |
| `SPYGLASS_ACCOUNT_CLOSURE_POLL_INTERVAL` | Optional positive duration; defaults to `1s` |
| `SPYGLASS_ACCOUNT_CLOSURE_LEASE` | Optional duration at most `30m`; defaults to `2m` |
| `SPYGLASS_ACCOUNT_CLOSURE_RETENTION` | Optional duration from `168h` through `8760h`; defaults to `720h` |
| `SPYGLASS_ACCOUNT_CLOSURE_BLOCKED_RETRY` | Optional duration from `1h` through `168h`; defaults to `24h` |
| `SPYGLASS_HEALTH_ADDRESS` | Optional health listen address; defaults to `:8081` |

Owners request and cancel closure through the account API using recent passkey assurance and an expected Account version. Requesting atomically changes the Account from `active` to `closing`; ordinary authorization and Account selection then fail immediately. The global lifecycle list deliberately remains available so an active owner can restore a closing Account without first selecting it.

Worker replicas claim due requests with `FOR UPDATE SKIP LOCKED` and expiring leases. Before logical close, each attempt rechecks the locally projected subscription and active Checkout state. A blocker leaves the Account frozen, records an immutable workload event, and reschedules the request. A clear preflight changes the Account to terminal `closed`, records `closed_at`, and schedules `delete_after`. The worker has no Stripe credential, performs no synchronous export or deletion, and must not be granted cell database access. Post-retention physical erasure is a separate reviewed operator workflow.

## Route receipt worker values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_DATABASE_URL` | Required credential for exactly one cell, restricted to the identifier-only cleanup queue and forced-RLS route receipts |
| `SPYGLASS_MAX_DATABASE_CONNS` | Optional positive pool cap; defaults to `5` |
| `SPYGLASS_ROUTE_RECEIPT_POLL_INTERVAL` | Optional duration from `100ms` through `1m`; defaults to `1s` |
| `SPYGLASS_ROUTE_RECEIPT_LEASE` | Optional duration from `1s` through `30m`; defaults to `30s` |
| `SPYGLASS_ROUTE_RECEIPT_RETENTION` | Optional post-expiry replay-evidence retention from `1m` through `24h`; defaults to `5m` |
| `SPYGLASS_ROUTE_RECEIPT_PRUNE_BATCH` | Optional bounded delete batch from 1 through 1000; defaults to `500` |
| `SPYGLASS_HEALTH_ADDRESS` | Optional health listen address; defaults to `:8081` |

Receipt insertion schedules an Account identifier in a non-RLS technical queue; it never copies request bindings, actors, paths, or business content into the coordination surface. Replicas claim with `FOR UPDATE SKIP LOCKED` and expiring leases. Deletion then runs through `CellPool` with transaction-local Account context and forced RLS. Because the bounded selection locks receipt rows, the credential needs `SELECT/UPDATE/DELETE` on `route_context_receipts`, plus `SELECT/UPDATE/DELETE` on the cleanup queue and schema usage. It must not own either table, receive `BYPASSRLS`, or read Account business tables.

The schedule version advances on every receipt insert. Cleanup acknowledgement is fenced by both lease ID and schedule version, so an insert racing with an empty cleanup cannot remove or postpone its schedule. `/health/status` exposes scheduled, ready, leased, retrying, oldest-due age, and aggregate process/prune/failure counters without Account or request identifiers.

## Work reconciler values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_CELL_DATABASE_URL` | Required cell database credential scoped to the technical Work release outbox plus Account-RLS Work checkpoints |
| `SPYGLASS_GLOBAL_DATABASE_URL` | Required global credential scoped to Account existence plus usage reservation/counter release |
| `SPYGLASS_CELL_MAX_DATABASE_CONNS` | Optional positive cell-pool cap; defaults to `4` |
| `SPYGLASS_GLOBAL_MAX_DATABASE_CONNS` | Optional positive global-pool cap; defaults to `4` |
| `SPYGLASS_WORK_RECONCILE_POLL_INTERVAL` | Optional positive duration; defaults to `1s` |
| `SPYGLASS_WORK_RECONCILE_LEASE` | Optional duration from `1s` through `30m`; defaults to `2m` |
| `SPYGLASS_WORK_RECONCILE_MAX_ATTEMPTS` | Optional integer from 1 through 100; defaults to `12` |
| `SPYGLASS_WORK_RELEASE_CLEANUP_INTERVAL` | Optional completed-job cleanup cadence from `1m` through `24h`; defaults to `1h` |
| `SPYGLASS_WORK_RELEASE_COMPLETED_RETENTION` | Optional completed technical-job retention from `24h` through `8760h`; defaults to `720h` (30 days) |
| `SPYGLASS_WORK_RELEASE_PRUNE_BATCH` | Optional SKIP-LOCKED delete batch from 1 through 1000; defaults to `500` |
| `SPYGLASS_HEALTH_ADDRESS` | Optional health listen address; defaults to `:8081` |

The cell migration creates an identifier-only technical outbox without Account RLS so a worker can lease across the cell without `SUPERUSER` or `BYPASSRLS`. Database grants—not a shared application credential—must restrict the cell role to `SELECT/UPDATE/DELETE` on that outbox, `SELECT` on Account namespaces, and `SELECT/UPDATE` on Work rows that remain protected by forced RLS. `DELETE` is used only by the bounded completed-job retention query. The global role needs `SELECT` on Accounts and `SELECT/UPDATE` on usage counters/reservations. It must not read Users, sessions, Billing, Entitlements, or cell business tables.

Terminal Work updates enqueue in the same cell transaction. A unique lease token prevents a stale replica from acknowledging reclaimed work. Release is idempotent under the original reservation UUID, so a crash between global release and cell checkpoint is safe. The twelfth transient failure or an immediately corrupt/missing reservation enters `dead_letter`; `GET /health/status` returns content-free counts and oldest pending age. Audited recovery uses the separate one-shot command documented in [work-release-operations.md](work-release-operations.md).

Every reconciler replica periodically deletes at most the configured batch of completed rows older than the retention cutoff using `FOR UPDATE SKIP LOCKED`. It never deletes pending, processing, failed, or dead-letter rows. The partial `completed_at` index keeps cleanup independent of live queue scans, and immutable `work_capacity_release_operator_events` remain after queue cleanup because they intentionally have no queue foreign key.

## Runner controller values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_CELL_DATABASE_URL` | Required controller credential with `SELECT/UPDATE` only on the two identifier-only runner-control tables |
| `SPYGLASS_CELL_MAX_DATABASE_CONNS` | Optional positive pool cap; defaults to `4` |
| `SPYGLASS_RUNNER_CONTROL_POLL_INTERVAL` | Optional duration from `100ms` through `1m`; defaults to `1s` |
| `SPYGLASS_RUNNER_CONTROL_LEASE` | Optional launch lease from `1s` through `30m`; defaults to `2m` |
| `SPYGLASS_RUNNER_CONTROL_MAX_ATTEMPTS` | Optional integer from 1 through 100; defaults to `8` |
| `SPYGLASS_RUNNER_INSPECTION_BATCH` | Optional terminal-inspection claim batch from 1 through 1000; defaults to `100` |
| `SPYGLASS_RUNNER_PAYLOAD_CLEANUP_INTERVAL` | Optional encrypted terminal-payload cleanup interval from `1m` through `24h`; defaults to `1h` |
| `SPYGLASS_RUNNER_PAYLOAD_RETENTION` | Optional encrypted request/result recovery window from `1h` through `720h`; defaults to `24h` |
| `SPYGLASS_RUNNER_PAYLOAD_PRUNE_BATCH` | Optional SKIP-LOCKED cleanup batch from 1 through 1000; defaults to `500` |
| `SPYGLASS_RUNNER_NAMESPACE` | Required exact Kubernetes namespace DNS label |
| `SPYGLASS_RUNNER_IMAGE` | Required immutable image reference ending in `@sha256:` plus exactly 64 lowercase hexadecimal characters |
| `SPYGLASS_RUNNER_SERVICE_ACCOUNT` | Required runner-pod service account; it has no RBAC, automatic API token mounting is disabled, and only a Pod-bound broker-audience token is projected explicitly |
| `SPYGLASS_RUNNER_RUNTIME_CLASS` | Required sandbox RuntimeClass DNS label; the environment must test its isolation and PID behavior |
| `SPYGLASS_RUNNER_BROKER_URL` | Required HTTPS broker origin/path with no embedded credentials, query, or fragment |
| `SPYGLASS_RUNNER_BROKER_CA_CONFIG_MAP` | Required namespace-local immutable ConfigMap name containing only the broker trust bundle at key `ca.crt`; its name is included in the Job contract |
| `SPYGLASS_RUNNER_ACTIVE_DEADLINE` | Optional whole-second Job deadline from `30s` through `24h`; defaults to `15m` |
| `SPYGLASS_RUNNER_JOB_RETENTION` | Optional whole-second completed-Job TTL from `1m` through `168h`; defaults to `1h` |
| `SPYGLASS_ERASURE_CHECKPOINT_SEQUENCE` / `SPYGLASS_ERASURE_CHECKPOINT_ROOT` | Required pinned cell restore checkpoint |
| `SPYGLASS_HEALTH_ADDRESS` | Optional health listen address; defaults to `:8081` |

The controller uses its in-cluster projected service-account token and CA only to create, get, and exactly delete Jobs. Its database role cannot insert work or request cancellation; the separate producer role has no table grants and executes only bounded configure, atomic encrypted-provision, and cancel functions. The controller additionally receives execute-only terminal-payload cleanup authority. Cleanup locks a bounded identifier-only terminal batch, enters each Account's RLS scope internally, and overwrites encrypted request/result envelopes with fixed sentinels after the recovery window while preserving hashes, action state, and capability audit. Ambiguous create results retain their Account slot under `launch_uncertain` until the exact Job or its exact absence is observed. Runner Jobs receive no database credential and set `automountServiceAccountToken: false`; they explicitly project only a short-lived token for the exact configured broker URL. The three compiled resource profiles (`agent-small`, `agent-medium`, and `agent-large`) are deployment policy rather than invocation input.

## Runner broker values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_CELL_DATABASE_URL` | Required broker credential with execute-only claim/result functions and no direct table grants |
| `SPYGLASS_CELL_MAX_DATABASE_CONNS` | Optional positive pool cap; defaults to `10` for the broker process |
| `SPYGLASS_RUNNER_BROKER_URL` | Exact HTTPS URL used both as projected-token audience and runner client origin |
| `SPYGLASS_RUNNER_NAMESPACE` | Exact namespace whose runner Pods and Jobs the online verifier accepts |
| `SPYGLASS_RUNNER_SERVICE_ACCOUNT` | Exact no-RBAC ServiceAccount used by runner Pods |
| `SPYGLASS_RUNNER_ENCRYPTION_KEYS` | Required comma-separated `positive-version=base64-32-byte-key` keyring; runtime secret, never database configuration |
| `SPYGLASS_RUNNER_ENCRYPTION_ACTIVE_VERSION` | Required positive version present in the keyring; all new envelopes use it |
| `SPYGLASS_RUNNER_BROKER_MAX_REQUEST_BODY_BYTES` | Optional positive result-request limit through 2 MiB; defaults to 2 MiB while the application envelope remains capped at 1 MiB |
| `SPYGLASS_TOOL_ROUTER_ORIGIN` | Required private app-router origin; HTTPS outside development |
| `SPYGLASS_MODEL_GATEWAY_ORIGIN` | Required private model-gateway origin; HTTPS outside development |
| `SPYGLASS_TOOL_CONTEXT_ISSUER` | Required exact issuer matching the app-router verifier |
| `SPYGLASS_TOOL_CONTEXT_SIGNING_KEY_ID` | Required active tool-proof key identifier |
| `SPYGLASS_TOOL_CONTEXT_SIGNING_KEY` | Standard Base64 encoding of at least 32 random secret bytes; never reuse route or envelope keys |
| `SPYGLASS_TOOL_CONTEXT_TTL` | Optional positive duration through `15s`; defaults to `10s` |
| `SPYGLASS_WORKLOAD_CERT_FILE` / `SPYGLASS_WORKLOAD_KEY_FILE` / `SPYGLASS_WORKLOAD_CA_FILE` | Required rotating TLS 1.3 server material |
| `SPYGLASS_ERASURE_CHECKPOINT_SEQUENCE` / `SPYGLASS_ERASURE_CHECKPOINT_ROOT` | Required pinned cell restore checkpoint |
| `SPYGLASS_HTTP_ADDRESS` | Optional broker HTTPS address; defaults to `:8443` |

The broker ServiceAccount uses its ordinary in-cluster credential only for online TokenReview and exact Pod/Job GETs. Its database role has execute-only exchange, capability-audit, and action begin/complete authority with no direct table grants. A separate future Attention projection role receives only authorization-record/cancel execute authority; it cannot begin or settle an action. The broker reaches model-gateway with its rotating workload certificate; it has no provider key. Health endpoints disclose only liveness/readiness and exchange responses set `no-store`.

Do not deploy the runner fleet until the Agents serving/dispatch workloads and any provider-specific consequential adapters are protected by a tested environment NetworkPolicy. Routed Agent serving, encrypted dispatch, the bounded `agent.turn.execute` and `work.summary.snapshot` executors, read-only `work.summary.read` and `agents.model.turn` handlers, lease-fenced result projection worker, and durable execute-versus-reconcile action authorizer are executable, but no consequential handler exists. The reference topology places ephemeral Jobs and their permissionless ServiceAccount in a dedicated runner namespace, includes namespace-scoped Job lifecycle and TokenReview/Pod/Job observer RBAC, and permits only the internal runner→broker→gateway/tool paths. An environment must still supply and validate its cluster API egress CIDR, sandbox RuntimeClass, digest-pinned runner artifact, database/provider egress, certificate/secret controllers, and alert/custom-metric integration. Durable cancellation, database exchange revocation, capability reauthorization, Pod-bound content-free audit, and projection retention fencing are executable but still require applied-cluster and node-partition proof. See [runner-control.md](runner-control.md) and [runner-broker.md](runner-broker.md).

## Agent dispatch worker values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_CELL_DATABASE_URL` | Required constrained cell dispatch credential |
| `SPYGLASS_CELL_MAX_DATABASE_CONNS` | Optional positive pool cap; defaults to `5` |
| `SPYGLASS_RUNNER_ENCRYPTION_KEYS` | Same versioned runtime keyring mounted into the broker; never stored in PostgreSQL |
| `SPYGLASS_RUNNER_ENCRYPTION_ACTIVE_VERSION` | Positive active key version present in the keyring |
| `SPYGLASS_AGENT_DISPATCH_POLL_INTERVAL` | Optional duration from `100ms` through `1m`; defaults to `1s` |
| `SPYGLASS_AGENT_DISPATCH_LEASE` | Optional whole-second lease from `1s` through `30m`; defaults to `30s` |
| `SPYGLASS_AGENT_DISPATCH_MAX_ATTEMPTS` | Optional integer from 1 through 100; defaults to `12` |
| `SPYGLASS_ERASURE_CHECKPOINT_SEQUENCE` / `SPYGLASS_ERASURE_CHECKPOINT_ROOT` | Required pinned cell restore checkpoint |
| `SPYGLASS_HEALTH_ADDRESS` | Optional health listen address; defaults to `:8081` |

The dispatcher role receives `USAGE` on `public,spyglass`; `SELECT` on `agent_invocation_execution_plans`, `agent_invocations`, `agent_persona_versions`, `agent_user_messages`, and `agent_messages` (all forced through the transaction-local Account RLS context); and `EXECUTE` only on the four dispatch functions plus `spyglass_provision_runner_invocation`. It receives no direct dispatch-queue, runner-queue, or runner-exchange table privileges and no Agent mutation authority.

```sql
GRANT USAGE ON SCHEMA public,spyglass TO spyglass_agent_dispatcher;
GRANT SELECT ON spyglass.agent_invocation_execution_plans,spyglass.agent_invocations,
  spyglass.agent_persona_versions,spyglass.agent_user_messages,spyglass.agent_messages
  TO spyglass_agent_dispatcher;
GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_dispatch(uuid,timestamptz,integer) TO spyglass_agent_dispatcher;
GRANT EXECUTE ON FUNCTION public.spyglass_complete_agent_dispatch(uuid,uuid,uuid,bytea,timestamptz) TO spyglass_agent_dispatcher;
GRANT EXECUTE ON FUNCTION public.spyglass_fail_agent_dispatch(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer) TO spyglass_agent_dispatcher;
GRANT EXECUTE ON FUNCTION public.spyglass_agent_dispatch_stats(timestamptz) TO spyglass_agent_dispatcher;
GRANT EXECUTE ON FUNCTION public.spyglass_provision_runner_invocation(uuid,uuid,text,timestamptz,bytea,bytea,integer,bytea,timestamptz) TO spyglass_agent_dispatcher;
```

## Agent result projection worker values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_CELL_DATABASE_URL` | Required execute-only projection credential; no direct Agent, runner-exchange, or projection-queue table grants |
| `SPYGLASS_CELL_MAX_DATABASE_CONNS` | Optional positive pool cap; defaults to `5` |
| `SPYGLASS_RUNNER_ENCRYPTION_KEYS` | Same versioned runtime keyring mounted into the broker; never stored in PostgreSQL |
| `SPYGLASS_RUNNER_ENCRYPTION_ACTIVE_VERSION` | Positive version present in the keyring; retained versions decrypt results created before rotation |
| `SPYGLASS_AGENT_PROJECTION_POLL_INTERVAL` | Optional duration from `100ms` through `1m`; defaults to `1s` |
| `SPYGLASS_AGENT_PROJECTION_LEASE` | Optional whole-second lease from `1s` through `30m`; defaults to `30s` |
| `SPYGLASS_AGENT_PROJECTION_MAX_ATTEMPTS` | Optional integer from 1 through 100; defaults to `12` |
| `SPYGLASS_ERASURE_CHECKPOINT_SEQUENCE` / `SPYGLASS_ERASURE_CHECKPOINT_ROOT` | Required pinned cell restore checkpoint |
| `SPYGLASS_HEALTH_ADDRESS` | Optional health listen address; defaults to `:8081` |

Grant this workload only `USAGE` on `public` and `EXECUTE` on `spyglass_claim_agent_result_projection`, both lease-bound projection functions, `spyglass_fail_agent_result_projection`, and `spyglass_agent_result_projection_stats`. Claim responses contain encrypted envelopes plus bounded identifiers/model routing metadata. The process never logs plaintext, keys, prompts, model results, or ciphertext. Invalid authenticated content is dead-lettered immediately; transient projection failures use bounded exponential retry. Terminal retention cannot purge a linked Agent envelope until queue state is `projected`, and dead-letter ciphertext therefore requires a controlled recovery or erasure decision.

```sql
GRANT USAGE ON SCHEMA public TO spyglass_agent_projector;
GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection(uuid,timestamptz,integer) TO spyglass_agent_projector;
GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_success(uuid,uuid,uuid,uuid,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,timestamptz,timestamptz) TO spyglass_agent_projector;
GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_failure(uuid,uuid,uuid,bytea,text,timestamptz,timestamptz) TO spyglass_agent_projector;
GRANT EXECUTE ON FUNCTION public.spyglass_fail_agent_result_projection(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer) TO spyglass_agent_projector;
GRANT EXECUTE ON FUNCTION public.spyglass_agent_result_projection_stats(timestamptz) TO spyglass_agent_projector;
```

## Agent queue operator values

`agent-queue-admin inspect|requeue` is a one-shot cell recovery command documented in [Agent Queue Operations](agent-queue-operations.md). It requires an exact `SPYGLASS_AGENT_QUEUE` of `dispatch` or `projection`, the target cell's execute-only `SPYGLASS_CELL_DATABASE_URL`, environment confirmation, and signed operator authorization. Inspection accepts only the bounded `SPYGLASS_AGENT_QUEUE_INSPECT_LIMIT`; requeue additionally requires exact `SPYGLASS_AGENT_ACCOUNT_ID` and `SPYGLASS_AGENT_INVOCATION_ID` values. The database functions expose identifiers, attempt counts, timestamps, and bounded error codes only. They never return prompts, message bodies, model results, ciphertext, keys, or provider credentials.

## Model gateway values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_OPENAI_API_KEY` | Required provider credential, mounted only into model-gateway |
| `SPYGLASS_OPENAI_ORIGIN` | Optional exact provider API origin; defaults to `https://api.openai.com` |
| `SPYGLASS_MODEL_GATEWAY_MAX_REQUEST_BODY_BYTES` | Optional positive bound through 256 KiB; defaults to 256 KiB |
| `SPYGLASS_WORKLOAD_CERT_FILE` / `SPYGLASS_WORKLOAD_KEY_FILE` / `SPYGLASS_WORKLOAD_CA_FILE` | Required rotating TLS 1.3 server material outside development |
| `SPYGLASS_WORKLOAD_CLIENT_IDENTITIES` | Comma-separated exact broker SPIFFE URI identities accepted on the private invocation path |
| `SPYGLASS_HTTP_ADDRESS` | Optional listen address; defaults to `:8443` outside development and `:8080` in development |

Model-gateway is stateless, accepts only workload-authenticated internal requests outside explicit development, and has no PostgreSQL configuration. Its OpenAI adapter disables provider-side response storage and parallel tool calls, sends strict JSON schemas for both tool arguments and final output, refuses redirects, ignores ambient proxy configuration, bounds responses, and never returns provider error bodies or its credential. No live provider call is part of repository verification; contract tests use local HTTP fixtures.

## Work release operator values

`work-release-admin inspect|requeue` is a short-lived controlled job, never a standing Deployment. Both actions require `SPYGLASS_CELL_DATABASE_URL`, `SPYGLASS_OPERATOR_ID`, `SPYGLASS_OPERATOR_REASON`, `SPYGLASS_ENVIRONMENT`, and an exact matching `SPYGLASS_CONFIRM_ENVIRONMENT`. Inspection accepts optional `SPYGLASS_WORK_RELEASE_INSPECT_LIMIT` from 1 through 100. Requeue requires `SPYGLASS_WORK_ACCOUNT_ID`, `SPYGLASS_WORK_ITEM_ID`, and `SPYGLASS_WORK_RESERVATION_ID` from an inspected record.

The operator credential receives only `USAGE` on the `public` and `spyglass` schemas plus `EXECUTE` on the two audited security-definer functions. It receives no direct queue, audit-table, Account namespace, or Work-table grants. See [work-release-operations.md](work-release-operations.md) for grants, diagnosis rules, invocation examples, and verification.

## Account erasure preparation values

`account-erasure-admin prepare|inspect|approve|cancel|execute|restore-replay` is a short-lived controlled job implementing the reviewed state machine in [account-erasure.md](account-erasure.md). Every action requires `SPYGLASS_GLOBAL_DATABASE_URL`, `SPYGLASS_OPERATOR_ID`, `SPYGLASS_OPERATOR_REASON`, `SPYGLASS_ENVIRONMENT`, and an exact `SPYGLASS_CONFIRM_ENVIRONMENT`. Every action except preparation requires `SPYGLASS_ACCOUNT_ERASURE_REQUEST_ID`; approval, cancellation, and live execution also require `SPYGLASS_ACCOUNT_ERASURE_VERSION`.

`execute` additionally requires `SPYGLASS_CELL_DATABASE_URL`, the exact snapshotted `SPYGLASS_CELL_ID`, `SPYGLASS_ACCOUNT_ID` plus matching `SPYGLASS_CONFIRM_ACCOUNT_ID`, and `SPYGLASS_ACCOUNT_ERASURE_EVIDENCE_KEY` as standard Base64 for exactly 32 random bytes. `SPYGLASS_ACCOUNT_ERASURE_LEASE` defaults to five minutes and must be between 30 seconds and one hour. The global and cell credentials are distinct execute-only roles. A retry before lease expiry fails closed; after expiry it may reclaim only the same durable stage and evidence. A retry after global commit returns the existing content-free tombstone without consuming a new event ID or decrementing capacity again.

`restore-replay` requires `SPYGLASS_CELL_DATABASE_URL`, exact `SPYGLASS_CELL_ID`, `SPYGLASS_ACCOUNT_ID` plus exact `SPYGLASS_CONFIRM_ACCOUNT_ID`, `SPYGLASS_ACCOUNT_ERASURE_RESTORE_DIRECTIVE_FILE`, and `SPYGLASS_ACCOUNT_ERASURE_RESTORE_SIGNING_KEY` as standard Base64 for exactly 32 bytes. The file must be a regular strict-JSON file no larger than 64 KiB and its signed request, Account, cell, and environment must match the explicit command configuration. Restore credentials are separate replay-only global/cell roles and the signing key is distinct from `SPYGLASS_ACCOUNT_ERASURE_EVIDENCE_KEY`. Completion logs contain the request ID and reconstructed checkpoint, never the raw Account or directive.

## Erasure restore checkpoint values

Every production serving process requires `SPYGLASS_ERASURE_CHECKPOINT_SEQUENCE` and `SPYGLASS_ERASURE_CHECKPOINT_ROOT` for its database target. The root is exactly 64 hexadecimal characters; sequence zero requires 64 zeroes. Global workloads pin a global checkpoint, while cell workloads pin that cell's checkpoint. `work-reconciler` instead requires both `SPYGLASS_GLOBAL_ERASURE_CHECKPOINT_SEQUENCE`/`ROOT` and `SPYGLASS_CELL_ERASURE_CHECKPOINT_SEQUENCE`/`ROOT` because it spans both stores. Explicit development mode may omit these values and uses only the canonical sequence-zero checkpoint.

The configured checkpoint may be historical: the immutable database ledger retains every sequence/root, so a live database that has completed newer erasures remains ready. A backup from before the pinned checkpoint cannot satisfy the lookup and is quarantined even though its schema and PostgreSQL connection are otherwise healthy. Ingress returns `503 {"status":"restore_replay_required"}` for every non-liveness request, and workers stop on their periodic gate check. The serving credential needs only `SELECT` on its target restore-ledger table.

Preparation additionally requires `SPYGLASS_CELL_DATABASE_URL`, `SPYGLASS_CELL_ID`, `SPYGLASS_ACCOUNT_ID`, exact `SPYGLASS_CONFIRM_ACCOUNT_ID`, `SPYGLASS_ACCOUNT_ERASURE_POLICY_VERSION`, RFC3339 `SPYGLASS_ACCOUNT_ERASURE_BACKUP_EXPIRES_AT`, and `SPYGLASS_ACCOUNT_ERASURE_EXPORT_DISPOSITION`. An `artifact` export requires its opaque reference, 64-character hexadecimal SHA-256, and RFC3339 expiry; `not_applicable` requires a policy-approved export reason. Approval reconnects to the snapshotted cell through the configured cell ID and repeats readiness attestation before changing state.

Normal operator credentials receive only `USAGE` on their schemas and `EXECUTE` on the relevant security-definer functions. They receive no direct Account, closure, billing, usage, Work, request, or audit-table privileges. Restore replay uses separate function-owner and operator roles; only the replay functions own the narrowly scoped table mutation and immutable-event bypass authority. No serving workload or ordinary erasure operator receives those grants.

## Local invocation shape

```text
spyglass account-api
spyglass app-router
spyglass app-api
spyglass admission-api
spyglass route-canary
spyglass route-receipt-worker
spyglass runner-controller
spyglass runner-broker
spyglass model-gateway
spyglass agent-dispatch-worker
spyglass agent-projection-worker
spyglass agent-queue-admin <action>
spyglass runner-invocation --broker-url=https://runner-broker.example --invocation-id=<uuid> --identity-token-file=/var/run/secrets/spyglass.io/runner-identity/token --broker-ca-file=/var/run/secrets/spyglass.io/broker-ca/ca.crt
spyglass billing-worker
spyglass notification-worker
spyglass entitlement-worker
spyglass account-lifecycle-worker
spyglass work-reconciler
spyglass work-release-admin <action>
spyglass account-erasure-admin <action>
spyglass catalog-admin <action>
SPYGLASS_MIGRATION_TARGET=global spyglass migrate
```

## Catalog operator values

`catalog-admin` is a one-shot process documented in [catalog-operations.md](catalog-operations.md). Every action requires `SPYGLASS_DATABASE_URL`, `SPYGLASS_OPERATOR_ID`, and `SPYGLASS_OPERATOR_REASON`. Draft creation additionally requires `SPYGLASS_CATALOG_FILE`; other actions require `SPYGLASS_CATALOG_VERSION`. `map-price` also requires `SPYGLASS_CATALOG_OFFER_CODE`, `SPYGLASS_STRIPE_MODE`, and `SPYGLASS_STRIPE_PRICE_ID`. `publish` accepts optional RFC3339 `SPYGLASS_CATALOG_EFFECTIVE_AT`.

Run this mode with a dedicated operator database credential in a short-lived controlled job. All administrator modes require the signed external authorization described in [Platform Operator Authorization](operator-authorization.md); an operator-name string is not authority. It does not require or accept account-api, webhook, notification, SMTP, or Stripe secret keys.

## Database migrations

The production binary embeds the reviewed SQL files, so a deployment does not depend on a mutable filesystem mount. `spyglass migrate` requires `SPYGLASS_DATABASE_URL` and an exact `SPYGLASS_MIGRATION_TARGET` of `global`, `cell`, or `development`. Unknown and empty targets fail closed.

Run `global` against the global control-plane database before deploying an account API, billing worker, notification worker, or entitlement worker that depends on the new schema. Run `cell` independently against each cell database before routing Accounts to workloads using that schema. `development` is seed data for disposable development databases only and must never run in production.

The runner takes a target-specific PostgreSQL advisory lock, checks the SHA-256 checksum of every previously applied file, and executes each new migration in its own transaction. Applied files are immutable: edit an unapplied prototype migration only while it has never reached a durable environment; otherwise add a new forward migration. The schema-qualified `public.spyglass_schema_migrations` ledger records target, version, filename, checksum, application time, and execution duration so pooled connections cannot resolve a different ledger after the `spyglass` cell schema exists.

Migration credentials are an independent deployment secret. They may own or alter schema; serving credentials must not. In particular, a cell serving role must not own cell tables and must not have `SUPERUSER` or `BYPASSRLS`, or PostgreSQL row-level security would not provide the intended Account boundary.

CI starts a disposable PostgreSQL 17 service and proves all three migration targets are executable and idempotent. The same gate exercises distributed network budgets, encrypted notification delivery, governed Catalog publication and rollback, existing-Account entitlement rollout and drift repair, concurrency-safe package capacity admission and Work release recovery, broker-backed Work creation and definitive-failure compensation through split roles, bounded forced-RLS route-receipt cleanup with concurrent-insert schedule fencing, execute-only audited Work and Agent dead-letter operations, retained-Account erasure eligibility, fresh cell readiness, four-eyes approval, non-destructive cancellation, exact cell deletion under forced RLS, leased cross-store handoff, repeated cell attestation, atomic global deletion, content-free global/cell tombstones, one-time capacity decrement, post-erasure billing-retry suppression, idempotent completion, and other-Account/User preservation, registration provisioning, Checkout reservation concurrency, transaction-local Account context, split worker credentials, fair runner admission, queued/launching/launched runner cancellation, stale lease rejection, and attempted cross-Account reads and writes through non-owner roles.

The Kubernetes reference uses these exact arguments and expects environment overlays to supply `spyglass-global-runtime`, `spyglass-cell-reference-runtime`, plus workload-specific `spyglass-account-api-secrets`, `spyglass-app-router-secrets`, `spyglass-app-api-secrets`, `spyglass-admission-api-secrets`, `spyglass-route-receipt-worker-cell-reference-secrets`, `spyglass-billing-worker-secrets`, `spyglass-notification-worker-secrets`, `spyglass-entitlement-worker-secrets`, and `spyglass-work-reconciler-secrets`. Those objects are intentionally absent from the repository. The router receives a constrained global credential and signing key; app-api receives only a cell credential and verification keyring. Admission-api receives only its narrow global usage credential and verification keyring. The route-receipt worker receives only its constrained cell cleanup credential. The entitlement worker secret needs only its constrained global-database credential. The Work reconciler secret contains distinct cell/global release credentials and no serving, Stripe, or SMTP secret. No literal production credential belongs in source control or a rendered manifest.

The `development` process still requires `SPYGLASS_ENV=development`; omitting both a mode and that explicit marker fails closed.
