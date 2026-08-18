# Production Runtime Configuration

- Status: executable Phase 2 account and billing processes
- Binary: `spyglass`
- Process modes: `account-api`, `billing-worker`, and explicit local-only `development`

## Process ownership

| Mode | Owns | Does not own |
|---|---|---|
| `account-api` | Signup, login, Account selection, invitations, local billing reads, Checkout/Portal creation, signed Stripe webhook acceptance, private browser shell | Billing event projection, reconciliation polling, Account business workloads |
| `billing-worker` | Leased Stripe inbox processing, current Subscription retrieval, transactional grant/snapshot projection, reconciliation queue | Browser/API traffic, raw webhook acceptance, customer business work |
| `development` | Memory-backed local identity and browser journey | Persistent data, outbound email, paid Stripe operations |

The account API and worker share no in-memory state. Multiple replicas coordinate through PostgreSQL row leases and unique constraints.

## Shared required values

| Environment variable | Meaning |
|---|---|
| `SPYGLASS_DATABASE_URL` | Global PostgreSQL connection string supplied through the environment secret manager |
| `SPYGLASS_STRIPE_SECRET_KEY` | Environment-specific `sk_test_` or `sk_live_` key |
| `SPYGLASS_STRIPE_MODE` | Exact `test` or `live` mode; must match the key |
| `SPYGLASS_STRIPE_API_VERSION` | Optional deliberate override; defaults to the compiled, tested pin |
| `SPYGLASS_MAX_DATABASE_CONNS` | Positive per-process pool cap; defaults to 10 for account API and 5 for worker |

Database connection limits are per replica. Environment overlays must ensure the replica maximum multiplied by the pool cap fits the managed PostgreSQL connection budget.

## Account API values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_HTTP_ADDRESS` | Optional listen address; defaults to `:8080` |
| `SPYGLASS_APP_ORIGIN` | Exact HTTPS Spyglass application origin |
| `SPYGLASS_PUBLIC_ORIGIN` | Exact HTTPS Infinite Ocean public origin |
| `SPYGLASS_STRIPE_WEBHOOK_SECRET` | Endpoint-specific `whsec_` secret |
| `SPYGLASS_SMTP_ADDRESS` | Implicit-TLS SMTP host and port, normally port 465 |
| `SPYGLASS_SMTP_SERVER_NAME` | TLS certificate server name |
| `SPYGLASS_SMTP_FROM_ADDRESS` | Bare sender email address |
| `SPYGLASS_SMTP_FROM_NAME` | Optional display name; defaults to `Infinite Ocean` |
| `SPYGLASS_SMTP_USERNAME`, `SPYGLASS_SMTP_PASSWORD` | Optional as a pair for authenticated relays |

Notification delivery requires TLS 1.2 or newer. Registration and invitation tokens are placed in message bodies only; this adapter does not log them. A send failure removes the unconsumed challenge/invitation rather than leaving a credential the user never received.

## Billing worker values

| Environment variable | Requirement |
|---|---|
| `SPYGLASS_BILLING_POLL_INTERVAL` | Optional positive Go duration; defaults to `1s` |
| `SPYGLASS_HEALTH_ADDRESS` | Optional health listen address; defaults to `:8081` |

`GET /health/live` reports process liveness. `GET /health/ready` performs a bounded PostgreSQL ping. The worker stops claiming new work on termination and the process allows the current database/provider request to end within the pod termination grace period.

## Local invocation shape

```text
spyglass account-api
spyglass billing-worker
```

The Kubernetes reference uses these exact arguments and expects environment overlays to supply `spyglass-global-runtime` plus workload-specific `spyglass-account-api-secrets` and `spyglass-billing-worker-secrets`. Those objects are intentionally absent from the repository. The worker does not receive webhook or SMTP credentials it cannot use, and no literal production credential belongs in source control or a rendered manifest.

The `development` process still requires `SPYGLASS_ENV=development`; omitting both a mode and that explicit marker fails closed.
