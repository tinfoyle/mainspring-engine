# Production Runtime Configuration

- Status: executable Phase 2 account, global router, cell API, private admission API, billing, notification, entitlement-rollout, Work reconciliation, migration, Catalog operator, and Work release operator processes
- Binary: `spyglass`
- Process modes: `account-api`, `app-router`, `app-api`, `admission-api`, `billing-worker`, `notification-worker`, `entitlement-worker`, `work-reconciler`, one-shot `work-release-admin`/`catalog-admin`/`migrate`, and explicit local-only `development`

## Process ownership

| Mode | Owns | Does not own |
|---|---|---|
| `account-api` | Signup, login/recovery, session security/reauthentication, Account selection, invitations, encrypted notification enqueueing, local billing reads, Checkout/Portal creation, signed Stripe webhook acceptance, private browser shell | SMTP delivery, billing event projection, reconciliation polling, Account business workloads |
| `app-router` | Authenticate the global session, recheck Account authority, select an allowlisted cell, issue request-bound route context, and proxy bounded Account API traffic | Cell database access, business-record queries, dynamic arbitrary destinations |
| `app-api` | Verify and consume route context, reject replay/stale placement, and execute Account-owned use cases through one shared cell pool | Global database, session cookies, Account/Billing mutation, arbitrary cell routing |
| `admission-api` | Re-verify routed Work operation proofs, reauthorize current global access, and reserve/compensate governed capacity | Cell database, Work content, browser sessions, terminal Work release, Stripe or SMTP operations |
| `billing-worker` | Leased Stripe inbox processing, current Subscription retrieval, transactional grant/snapshot projection, reconciliation queue | Browser/API traffic, raw webhook acceptance, customer business work |
| `notification-worker` | Leased encrypted identity-notification delivery, bounded retries, terminal dead-letter state | Browser/API traffic, identity mutation, billing credentials, customer business work |
| `entitlement-worker` | Bounded existing-Account Catalog rollout seeding, leased free-plan recomputation, immutable changed-access snapshots, and drift repair | Catalog publication decisions, paid-grant mutation, Stripe or SMTP operations, customer business work |
| `work-reconciler` | Lease identifier-only terminal Work release jobs, idempotently release global capacity, and checkpoint the matching Account-scoped Work row | Serving traffic, Work content reads, capacity reservation, package mutation, Stripe or SMTP operations |
| `work-release-admin` | One audited, bounded inspection or exact-target requeue of Work release dead letters | Serving traffic, customer Work content, direct queue table access, global capacity mutation |
| `catalog-admin` | One audited draft, mapping, review, approval, publish, retire, or rollback action | Serving traffic, automatic publication decisions, customer data mutation |
| `development` | Memory-backed local identity and browser journey | Persistent data, outbound email, paid Stripe operations |
| `migrate` | One embedded, immutable migration target against one database | Serving traffic, background work, automatic target selection |

The account API and workers share no in-memory state. Multiple replicas coordinate through PostgreSQL row leases and unique constraints.

## Common production values

| Environment variable | Consumers | Meaning |
|---|---|---|
| `SPYGLASS_DATABASE_URL` | Persistent processes except `work-reconciler` | Workload-specific PostgreSQL connection string: global for control-plane modes and one cell database for `app-api` |
| `SPYGLASS_STRIPE_SECRET_KEY` | Account API, billing worker | Environment-specific `sk_test_` or `sk_live_` key |
| `SPYGLASS_STRIPE_MODE` | Account API, billing worker | Exact `test` or `live` mode; must match the key |
| `SPYGLASS_STRIPE_API_VERSION` | Account API, billing worker | Optional deliberate override; defaults to the compiled, tested pin |
| `SPYGLASS_NOTIFICATION_ENCRYPTION_KEY` | Account API, notification worker | Standard Base64 encoding of exactly 32 random bytes |
| `SPYGLASS_NETWORK_ACTOR_KEY` | Account API | Standard Base64 encoding of exactly 32 random bytes used only for keyed request-actor hashing |
| `SPYGLASS_MAX_DATABASE_CONNS` | Persistent processes except `work-reconciler` | Positive per-process pool cap with a workload-specific default |

Database connection limits are per replica. Environment overlays must ensure the replica maximum multiplied by the pool cap fits the managed PostgreSQL connection budget.

## Account API values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_HTTP_ADDRESS` | Optional listen address; defaults to `:8080` |
| `SPYGLASS_APP_ORIGIN` | Exact HTTPS Spyglass application origin |
| `SPYGLASS_PUBLIC_ORIGIN` | Exact HTTPS Infinite Ocean public origin |
| `SPYGLASS_STRIPE_WEBHOOK_SECRET` | Endpoint-specific `whsec_` secret |
| `SPYGLASS_TRUSTED_PROXY_CIDRS` | Optional comma-separated ingress/load-balancer networks allowed to supply `X-Forwarded-For`; empty trusts no proxy |
| `SPYGLASS_CATALOG_REFRESH_INTERVAL` | Optional positive Go duration for effective publication polling; defaults to `5s` |

The account API has no SMTP configuration. It serializes registration, invitation, and credential-recovery messages, encrypts each envelope with AES-256-GCM, and persists only ciphertext, a nonce, and key version in the durable outbox. Associated data binds the ciphertext to the outbox ID and notification kind. Plaintext tokens are never written to the outbox or application logs.

Credential-recovery initiation always returns the same accepted response regardless of whether the email is registered, malformed, or throttled. Unknown and throttled identifiers enqueue encrypted discard work so the public request does similar cryptographic and database work without sending mail. Challenges store only a SHA-256 token hash, expire after 30 minutes, and are single use. Completion changes the Argon2id credential, advances the User security version, revokes every session, consumes all pending recovery challenges, and appends a `credential_recovered` security event in one PostgreSQL transaction. Identifier and network-actor throttles are both durable.

Anonymous login and recovery budgets are shared across replicas in PostgreSQL. The actor key is HMAC-SHA-256 over the canonical socket/client address, so raw addresses are not stored in limiter state and cannot be recovered through an offline hash dictionary. Login permits 60 attempts per actor per 15 minutes; recovery permits 10 per actor per hour. A denied login still performs the identity lookup and password verification before returning the generic credential failure. A denied recovery request performs token generation and encrypted discard enqueueing before returning the generic accepted response.

Forwarding headers are ignored unless the immediate socket peer belongs to `SPYGLASS_TRUSTED_PROXY_CIDRS`. Behind trusted proxies, Spyglass walks `X-Forwarded-For` from right to left and selects the first untrusted hop, preventing a client-supplied leftmost value from becoming authoritative. Malformed trusted forwarding chains fail closed. Environment overlays must set only the exact ingress or load-balancer networks they operate; broad private-network ranges are not safe defaults.

Each account-api replica holds one immutable Catalog snapshot. It polls for the newest effective `published_at` and atomically replaces the snapshot, including deliberate rollback to a lower version. A request or registration completion reads one snapshot, so a concurrent refresh cannot mix versions inside that operation.

## App router and cell API values

| Environment variable | Consumers | Requirement |
|---|---|---|
| `SPYGLASS_ROUTE_ISSUER` | App router, app API | Exact shared issuer name, normally `spyglass-app-router` |
| `SPYGLASS_ROUTE_SIGNING_KEY_ID` | App router | Active non-secret key identifier |
| `SPYGLASS_ROUTE_SIGNING_KEY` | App router | Standard Base64 encoding of exactly 32 random secret bytes |
| `SPYGLASS_ROUTE_VERIFY_KEYS` | App API, admission API | Comma-separated `key-id=base64-key` keyring containing active and retained rotation keys |
| `SPYGLASS_ROUTE_CONTEXT_TTL` | App router | Optional positive duration; defaults to `20s` and has a hard `30s` maximum |
| `SPYGLASS_CELL_ROUTES` | App router | Comma-separated `cell-id=https://service-origin` allowlist; production entries allow no paths, credentials, queries, fragments, or HTTP |
| `SPYGLASS_CELL_ID` | App API | Exact cell identity used as token audience and deployment identity |
| `SPYGLASS_SESSION_COOKIE_NAME` | App router | Optional; defaults to `__Host-spyglass_session` |
| `SPYGLASS_MAX_REQUEST_BODY_BYTES` | App API | Optional positive limit up to 16 MiB; defaults to 1 MiB |
| `SPYGLASS_WORK_ADMISSION_ORIGIN` | App API | Exact private admission-api origin; HTTPS is the fail-closed default |
| `SPYGLASS_ALLOW_HTTP_ADMISSION` | App API | Exact `true` opt-in for local/review topology only; forbidden in production |

The signing and verification keys follow the add-verifier, switch-signer, wait-for-expiry, remove-old-key sequence in [routing-boundary.md](routing-boundary.md). The app-router database credential is global and cannot read cell schemas. The app-api credential is cell-local and cannot read global Users, Memberships, Entitlements, Billing, or sessions. Every routed mutation requires a UUID `Idempotency-Key`; transition and assignment require `If-Match`. Those semantic headers are included in the signed request binding.

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

App-api presents the original short-lived route proof. Admission-api re-verifies its signature, cell audience, Work mutation path, operation ID, enabled package claim, and exact request binding before current global authorization. The reference NetworkPolicy permits only app-api pods to connect. App-api rejects a plain-HTTP broker origin unless `SPYGLASS_ALLOW_HTTP_ADMISSION=true`; that explicit escape hatch is present only in the review topology. Production overlays must remove it and supply HTTPS, workload identity, and environment-specific egress policy.

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
| `SPYGLASS_HEALTH_ADDRESS` | Optional health listen address; defaults to `:8081` |

The cell migration creates an identifier-only technical outbox without Account RLS so a worker can lease across the cell without `SUPERUSER` or `BYPASSRLS`. Database grants—not a shared application credential—must restrict the cell role to `SELECT/UPDATE` on that outbox, `SELECT` on Account namespaces, and `SELECT/UPDATE` on Work rows that remain protected by forced RLS. The global role needs `SELECT` on Accounts and `SELECT/UPDATE` on usage counters/reservations. It must not read Users, sessions, Billing, Entitlements, or cell business tables.

Terminal Work updates enqueue in the same cell transaction. A unique lease token prevents a stale replica from acknowledging reclaimed work. Release is idempotent under the original reservation UUID, so a crash between global release and cell checkpoint is safe. The twelfth transient failure or an immediately corrupt/missing reservation enters `dead_letter`; `GET /health/status` returns content-free counts and oldest pending age. Audited recovery uses the separate one-shot command documented in [work-release-operations.md](work-release-operations.md).

## Work release operator values

`work-release-admin inspect|requeue` is a short-lived controlled job, never a standing Deployment. Both actions require `SPYGLASS_CELL_DATABASE_URL`, `SPYGLASS_OPERATOR_ID`, `SPYGLASS_OPERATOR_REASON`, `SPYGLASS_ENVIRONMENT`, and an exact matching `SPYGLASS_CONFIRM_ENVIRONMENT`. Inspection accepts optional `SPYGLASS_WORK_RELEASE_INSPECT_LIMIT` from 1 through 100. Requeue requires `SPYGLASS_WORK_ACCOUNT_ID`, `SPYGLASS_WORK_ITEM_ID`, and `SPYGLASS_WORK_RESERVATION_ID` from an inspected record.

The operator credential receives only `USAGE` on the `public` and `spyglass` schemas plus `EXECUTE` on the two audited security-definer functions. It receives no direct queue, audit-table, Account namespace, or Work-table grants. See [work-release-operations.md](work-release-operations.md) for grants, diagnosis rules, invocation examples, and verification.

## Local invocation shape

```text
spyglass account-api
spyglass app-router
spyglass app-api
spyglass admission-api
spyglass billing-worker
spyglass notification-worker
spyglass entitlement-worker
spyglass work-reconciler
spyglass work-release-admin <action>
spyglass catalog-admin <action>
SPYGLASS_MIGRATION_TARGET=global spyglass migrate
```

## Catalog operator values

`catalog-admin` is a one-shot process documented in [catalog-operations.md](catalog-operations.md). Every action requires `SPYGLASS_DATABASE_URL`, `SPYGLASS_OPERATOR_ID`, and `SPYGLASS_OPERATOR_REASON`. Draft creation additionally requires `SPYGLASS_CATALOG_FILE`; other actions require `SPYGLASS_CATALOG_VERSION`. `map-price` also requires `SPYGLASS_CATALOG_OFFER_CODE`, `SPYGLASS_STRIPE_MODE`, and `SPYGLASS_STRIPE_PRICE_ID`. `publish` accepts optional RFC3339 `SPYGLASS_CATALOG_EFFECTIVE_AT`.

Run this mode with a dedicated operator database credential in a short-lived controlled job. It does not require or accept account-api, webhook, notification, SMTP, or Stripe secret keys.

## Database migrations

The production binary embeds the reviewed SQL files, so a deployment does not depend on a mutable filesystem mount. `spyglass migrate` requires `SPYGLASS_DATABASE_URL` and an exact `SPYGLASS_MIGRATION_TARGET` of `global`, `cell`, or `development`. Unknown and empty targets fail closed.

Run `global` against the global control-plane database before deploying an account API, billing worker, notification worker, or entitlement worker that depends on the new schema. Run `cell` independently against each cell database before routing Accounts to workloads using that schema. `development` is seed data for disposable development databases only and must never run in production.

The runner takes a target-specific PostgreSQL advisory lock, checks the SHA-256 checksum of every previously applied file, and executes each new migration in its own transaction. Applied files are immutable: edit an unapplied prototype migration only while it has never reached a durable environment; otherwise add a new forward migration. The `spyglass_schema_migrations` ledger records target, version, filename, checksum, application time, and execution duration.

Migration credentials are an independent deployment secret. They may own or alter schema; serving credentials must not. In particular, a cell serving role must not own cell tables and must not have `SUPERUSER` or `BYPASSRLS`, or PostgreSQL row-level security would not provide the intended Account boundary.

CI starts a disposable PostgreSQL 17 service and proves all three migration targets are executable and idempotent. The same gate exercises distributed network budgets, encrypted notification delivery, governed Catalog publication and rollback, existing-Account entitlement rollout and drift repair, concurrency-safe package capacity admission and Work release recovery, broker-backed Work creation and definitive-failure compensation through split roles, execute-only audited dead-letter operations, registration provisioning, Checkout reservation concurrency, transaction-local Account context, split reconciler credentials, stale lease rejection, and attempted cross-Account reads and writes through non-owner roles.

The Kubernetes reference uses these exact arguments and expects environment overlays to supply `spyglass-global-runtime`, `spyglass-cell-reference-runtime`, plus workload-specific `spyglass-account-api-secrets`, `spyglass-app-router-secrets`, `spyglass-app-api-secrets`, `spyglass-admission-api-secrets`, `spyglass-billing-worker-secrets`, `spyglass-notification-worker-secrets`, `spyglass-entitlement-worker-secrets`, and `spyglass-work-reconciler-secrets`. Those objects are intentionally absent from the repository. The router receives a constrained global credential and signing key; app-api receives only a cell credential and verification keyring. Admission-api receives only its narrow global usage credential and verification keyring. The entitlement worker secret needs only its constrained global-database credential. The Work reconciler secret contains distinct cell/global release credentials and no serving, Stripe, or SMTP secret. No literal production credential belongs in source control or a rendered manifest.

The `development` process still requires `SPYGLASS_ENV=development`; omitting both a mode and that explicit marker fails closed.
