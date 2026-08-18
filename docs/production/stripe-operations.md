# Stripe Commercial Access Operations

- Status: implemented boundary; test-mode environment validation pending
- API version: `2026-07-29.dahlia`
- Projection authority: verified webhook inbox plus current Stripe Subscription retrieval

## Safety invariants

- The browser submits a local published `offer_code`; it never submits a Stripe Price ID.
- `offer_provider_prices` is private operational configuration, separated by Catalog version and Stripe test/live mode.
- Only an active Account owner or billing administrator with a password confirmation no more than 10 minutes old can create Checkout or Customer Portal sessions.
- Every Stripe `POST` carries an idempotency key. Checkout and Portal API requests require a UUID `Idempotency-Key` from the caller; customer creation has a deterministic Account-scoped key.
- A durable Account/mode Checkout reservation serializes attempts. Concurrent requests cannot open parallel subscription Checkouts, and retries resume the already-created hosted session until it expires or projection completes.
- Success, cancel, and return URLs are constructed from the configured exact HTTPS application origin. They are not request parameters.
- A Checkout redirect never grants access. Only a locally projected subscription state changes subscription grants.
- Entitlement checks use the current local immutable snapshot and do not synchronously call Stripe.

## Required environment configuration

The persistent `accountapi` composition requires PostgreSQL, a real notification sender, a Stripe secret key, a distinct webhook endpoint secret, and exact application/public origins. The key's `sk_test_` or `sk_live_` prefix must match `StripeMode`.

Configure the webhook endpoint in Stripe Workbench with API version `2026-07-29.dahlia`. `StripeAPIVersion` can override the compiled request pin only for a deliberate, tested upgrade. Test and live events, keys, Customers, Prices, and mappings remain isolated.

## Publishing a paid Offer mapping

Catalog publication and Stripe object creation are separate reviewed operations. After creating an immutable recurring Stripe Price, insert one mapping for each enabled mode:

```sql
INSERT INTO offer_provider_prices (
  catalog_version, offer_code, provider, mode, provider_price_id, active, created_at
) VALUES (
  2, 'team-monthly-v1', 'stripe', 'test', 'price_REPLACE_IN_ENVIRONMENT', true, statement_timestamp()
);
```

Do not place Price IDs in public Catalog JSON. Price changes require a new Catalog Offer/mapping; existing subscriptions retain the historical mapping needed to explain access.

## Projection policy

| Stripe subscription state | Spyglass subscription grants |
|---|---|
| `active`, `trialing` | Plan package modes and limits |
| `past_due` | Read-only package grants during remediation |
| `incomplete`, `incomplete_expired`, `paused`, `unpaid`, `canceled` | No subscription grants; other grant sources still apply |

Each recognized event triggers retrieval of the current Stripe Subscription. This makes delayed and out-of-order delivery converge on current state. The transaction locks the Account entitlement version, upserts the Subscription while rejecting cross-Account conflicts, replaces only that Subscription's grants, re-evaluates every active grant source, and publishes a snapshot only when effective access changed.

## Worker and operator boundaries

- `BillingProcessor.ProcessOne` leases one verified inbox event and applies bounded retry.
- `BillingReconciler.ProcessOne` leases one requested Subscription refresh and retrieves current provider state.
- `BillingInbox.Replay` can requeue only a stored, signature-verified event that is processed or failed; it cannot inject a payload.
- `BillingProjectionRepository.QueueReconciliation` is the boundary for scheduled drift scans and operator-requested refresh.

Production commands over replay/reconciliation must require an operator identity, reason, audit record, and environment confirmation. Direct database mutation is not an operator interface.

## Environment validation gate

1. Run migrations against disposable PostgreSQL and verify the restore procedure.
2. Create test-mode Product, recurring Prices, portal configuration, and version-pinned webhook endpoint.
3. Exercise Checkout completion, duplicate and delayed delivery, failed payment, remediation, cancellation-at-period-end, cancellation, and reactivation.
4. Confirm the Checkout return page remains `processing` until projection completes.
5. Compare local Subscriptions and snapshots with Stripe through the reconciliation queue.
6. Rotate the test webhook secret and verify overlap/retirement before live rollout.
