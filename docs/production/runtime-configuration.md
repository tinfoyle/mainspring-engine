# Production Runtime Configuration

- Status: executable Phase 2 account, billing, and notification processes
- Binary: `spyglass`
- Process modes: `account-api`, `billing-worker`, `notification-worker`, one-shot `migrate`, and explicit local-only `development`

## Process ownership

| Mode | Owns | Does not own |
|---|---|---|
| `account-api` | Signup, login/recovery, session security/reauthentication, Account selection, invitations, encrypted notification enqueueing, local billing reads, Checkout/Portal creation, signed Stripe webhook acceptance, private browser shell | SMTP delivery, billing event projection, reconciliation polling, Account business workloads |
| `billing-worker` | Leased Stripe inbox processing, current Subscription retrieval, transactional grant/snapshot projection, reconciliation queue | Browser/API traffic, raw webhook acceptance, customer business work |
| `notification-worker` | Leased encrypted identity-notification delivery, bounded retries, terminal dead-letter state | Browser/API traffic, identity mutation, billing credentials, customer business work |
| `development` | Memory-backed local identity and browser journey | Persistent data, outbound email, paid Stripe operations |
| `migrate` | One embedded, immutable migration target against one database | Serving traffic, background work, automatic target selection |

The account API and workers share no in-memory state. Multiple replicas coordinate through PostgreSQL row leases and unique constraints.

## Common production values

| Environment variable | Consumers | Meaning |
|---|---|---|
| `SPYGLASS_DATABASE_URL` | All persistent processes | Global PostgreSQL connection string supplied through the environment secret manager |
| `SPYGLASS_STRIPE_SECRET_KEY` | Account API, billing worker | Environment-specific `sk_test_` or `sk_live_` key |
| `SPYGLASS_STRIPE_MODE` | Account API, billing worker | Exact `test` or `live` mode; must match the key |
| `SPYGLASS_STRIPE_API_VERSION` | Account API, billing worker | Optional deliberate override; defaults to the compiled, tested pin |
| `SPYGLASS_NOTIFICATION_ENCRYPTION_KEY` | Account API, notification worker | Standard Base64 encoding of exactly 32 random bytes |
| `SPYGLASS_NETWORK_ACTOR_KEY` | Account API | Standard Base64 encoding of exactly 32 random bytes used only for keyed request-actor hashing |
| `SPYGLASS_MAX_DATABASE_CONNS` | All persistent processes | Positive per-process pool cap; defaults to 10 for account API and 5 for workers |

Database connection limits are per replica. Environment overlays must ensure the replica maximum multiplied by the pool cap fits the managed PostgreSQL connection budget.

## Account API values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_HTTP_ADDRESS` | Optional listen address; defaults to `:8080` |
| `SPYGLASS_APP_ORIGIN` | Exact HTTPS Spyglass application origin |
| `SPYGLASS_PUBLIC_ORIGIN` | Exact HTTPS Infinite Ocean public origin |
| `SPYGLASS_STRIPE_WEBHOOK_SECRET` | Endpoint-specific `whsec_` secret |
| `SPYGLASS_TRUSTED_PROXY_CIDRS` | Optional comma-separated ingress/load-balancer networks allowed to supply `X-Forwarded-For`; empty trusts no proxy |

The account API has no SMTP configuration. It serializes registration, invitation, and credential-recovery messages, encrypts each envelope with AES-256-GCM, and persists only ciphertext, a nonce, and key version in the durable outbox. Associated data binds the ciphertext to the outbox ID and notification kind. Plaintext tokens are never written to the outbox or application logs.

Credential-recovery initiation always returns the same accepted response regardless of whether the email is registered, malformed, or throttled. Unknown and throttled identifiers enqueue encrypted discard work so the public request does similar cryptographic and database work without sending mail. Challenges store only a SHA-256 token hash, expire after 30 minutes, and are single use. Completion changes the Argon2id credential, advances the User security version, revokes every session, consumes all pending recovery challenges, and appends a `credential_recovered` security event in one PostgreSQL transaction. Identifier and network-actor throttles are both durable.

Anonymous login and recovery budgets are shared across replicas in PostgreSQL. The actor key is HMAC-SHA-256 over the canonical socket/client address, so raw addresses are not stored in limiter state and cannot be recovered through an offline hash dictionary. Login permits 60 attempts per actor per 15 minutes; recovery permits 10 per actor per hour. A denied login still performs the identity lookup and password verification before returning the generic credential failure. A denied recovery request performs token generation and encrypted discard enqueueing before returning the generic accepted response.

Forwarding headers are ignored unless the immediate socket peer belongs to `SPYGLASS_TRUSTED_PROXY_CIDRS`. Behind trusted proxies, Spyglass walks `X-Forwarded-For` from right to left and selects the first untrusted hop, preventing a client-supplied leftmost value from becoming authoritative. Malformed trusted forwarding chains fail closed. Environment overlays must set only the exact ingress or load-balancer networks they operate; broad private-network ranges are not safe defaults.

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

## Local invocation shape

```text
spyglass account-api
spyglass billing-worker
spyglass notification-worker
SPYGLASS_MIGRATION_TARGET=global spyglass migrate
```

## Database migrations

The production binary embeds the reviewed SQL files, so a deployment does not depend on a mutable filesystem mount. `spyglass migrate` requires `SPYGLASS_DATABASE_URL` and an exact `SPYGLASS_MIGRATION_TARGET` of `global`, `cell`, or `development`. Unknown and empty targets fail closed.

Run `global` against the global control-plane database before deploying an account API, billing worker, or notification worker that depends on the new schema. Run `cell` independently against each cell database before routing Accounts to workloads using that schema. `development` is seed data for disposable development databases only and must never run in production.

The runner takes a target-specific PostgreSQL advisory lock, checks the SHA-256 checksum of every previously applied file, and executes each new migration in its own transaction. Applied files are immutable: edit an unapplied prototype migration only while it has never reached a durable environment; otherwise add a new forward migration. The `spyglass_schema_migrations` ledger records target, version, filename, checksum, application time, and execution duration.

Migration credentials are an independent deployment secret. They may own or alter schema; serving credentials must not. In particular, a cell serving role must not own cell tables and must not have `SUPERUSER` or `BYPASSRLS`, or PostgreSQL row-level security would not provide the intended Account boundary.

CI starts a disposable PostgreSQL 17 service and proves all three migration targets are executable and idempotent. The same gate exercises distributed network budgets, encrypted notification delivery, the published catalog, registration provisioning, Checkout reservation concurrency, transaction-local Account context, and attempted cross-Account reads and writes through a non-owner serving role.

The Kubernetes reference uses these exact arguments and expects environment overlays to supply `spyglass-global-runtime` plus workload-specific `spyglass-account-api-secrets`, `spyglass-billing-worker-secrets`, and `spyglass-notification-worker-secrets`. Those objects are intentionally absent from the repository. Workload-specific secrets keep SMTP credentials out of the account API and billing worker, and keep Stripe credentials out of the notification worker. No literal production credential belongs in source control or a rendered manifest.

The `development` process still requires `SPYGLASS_ENV=development`; omitting both a mode and that explicit marker fails closed.
