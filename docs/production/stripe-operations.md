# Stripe Commercial Access Operations

- Status: implemented boundary and audited operator controls; live Stripe test-mode exercise pending
- API version: `2026-07-29.dahlia`
- Projection authority: verified webhook inbox plus current Stripe Subscription retrieval

## Safety invariants

- The browser submits a local published `offer_code`; it never submits a Stripe Price ID.
- `offer_provider_prices` is private operational configuration, separated by Catalog version and Stripe test/live mode.
- Only an active Account owner or billing administrator with user-verified passkey proof no more than 10 minutes old can create Checkout or Customer Portal sessions. The commercial application service enforces this independently of HTTP/browser adapters, and password confirmation does not satisfy it.
- Every Stripe `POST` carries an idempotency key. Checkout and Portal API requests require a UUID `Idempotency-Key` from the caller; customer creation has a deterministic Account-scoped key.
- A durable Account/mode Checkout reservation serializes attempts. Concurrent requests cannot open parallel subscription Checkouts, and retries resume the already-created hosted session until it expires or projection completes.
- Success, cancel, and return URLs are constructed from the configured exact HTTPS application origin. They are not request parameters.
- A Checkout redirect never grants access. Only a locally projected subscription state changes subscription grants.
- Payment-mode Checkout metadata never supplies fulfillment values. It selects one durable local attempt whose item version, Catalog version, amount and AI Token quantity were frozen before Stripe; only verified paid projection can complete it.
- The optional commissioning Price may be a one-time line in initial subscription Checkout or a later payment-mode Checkout. Durable Account purchase evidence grants no entitlement or Affiliate earning and prevents a second self-service purchase.
- Entitlement checks use the current local immutable snapshot and do not synchronously call Stripe.

## Required environment configuration

The persistent `accountapi` composition requires PostgreSQL, a real notification sender, a Stripe secret key, a distinct webhook endpoint secret, and exact application/public origins. The key's `sk_test_` or `sk_live_` prefix must match `StripeMode`.

Configure the webhook endpoint in Stripe Workbench with API version `2026-07-29.dahlia`. `StripeAPIVersion` can override the compiled request pin only for a deliberate, tested upgrade. Test and live events, keys, Customers, Prices, and mappings remain isolated.

The launch event selection includes Checkout/Subscription/Invoice events required for subscription invalidation, `checkout.session.completed` and `checkout.session.async_payment_succeeded` for payment-mode fulfillment, `charge.refunded` for final full-charge top-up reversal, plus `invoice.paid`, `refund.created`, `refund.updated`, and `charge.dispute.closed` for the closed Affiliate ledger boundary. These events do not enable Affiliate enrollment or attribution; they make already-locked commercial history correct once the separately reviewed feature flags open. See [Affiliate operations](affiliate-operations.md).

## Publishing paid item mappings

Catalog publication and Stripe object creation are separate reviewed operations. After creating immutable recurring and one-time Stripe Prices, use the signed Catalog operator command for each enabled mode. Every governed publication containing AI Token commerce requires mappings for its subscription Offer, each purchasable bundle and commissioning item before review:

```powershell
$env:SPYGLASS_CATALOG_VERSION = '2'
$env:SPYGLASS_CATALOG_OFFER_CODE = 'team-monthly-v1'
$env:SPYGLASS_STRIPE_MODE = 'test'
$env:SPYGLASS_STRIPE_PRICE_ID = 'price_REPLACE_IN_ENVIRONMENT'
spyglass catalog-admin map-price
```

Do not place Price IDs in public Catalog JSON. Price or quantity changes require a new immutable Catalog item/mapping; existing subscriptions and completed purchase attempts retain the historical mapping and local snapshot needed to explain access or fulfillment.

## Projection policy

| Stripe subscription state | Spyglass subscription grants |
|---|---|
| `active`, `trialing` | Plan package modes and limits; a configured `pause_collection` reduces them to read-only |
| `past_due` | Read-only package grants during remediation |
| `incomplete`, `incomplete_expired`, `paused`, `unpaid`, `canceled` | No subscription grants; other grant sources still apply |

Cancellation scheduled at period end keeps the current state-derived grant until Stripe reports the effective state change. Recovery to `active` or `trialing` restores it. Upgrades, downgrades and prorations resolve the current Price to an immutable Offer mapping and replace that Subscription's grants; Spyglass performs no monetary or proration calculation. Invoice events invalidate subscription access projection. Verified `invoice.paid`, succeeded Refund and final lost-dispute evidence additionally drive the isolated Affiliate earning/reversal ledger when a locked attribution exists; they never grant customer access. Tax/invoice presentation never grants access independently.

Each recognized event triggers retrieval of the current Stripe Subscription. This makes delayed and out-of-order delivery converge on current state. The transaction locks the Account entitlement version, upserts the Subscription while rejecting cross-Account conflicts, replaces only that Subscription's grants, and re-evaluates every active grant source. Historical Offer/Catalog mappings still determine the purchased grant values, while the current effective Catalog supplies dependency and limit-policy semantics for the new Account snapshot; a delayed event therefore cannot roll newer free-plan policy backward. A snapshot is published only when effective access changed.

Projection and reconciliation persist safe classified failures. `subscription_mapping_mismatch` means provider metadata conflicts with the immutable local Account/Offer/Catalog association; `subscription_unmapped` means no published Offer/Price mapping resolves the current subscription. Generic provider or transactional failures retain `projection_failed` or `refresh_failed`. `billing-admin inspect` expands these codes into bounded operator guidance without exposing webhook payloads, Price IDs or secrets.

Payment-mode projection accepts only a paid Checkout Session whose signed Account, request, kind, item/version and Catalog version match the durable attempt, whose Session ID matches the hosted reservation and whose USD subtotal equals its frozen local amount. The PaymentIntent becomes the exact-once purchased-grant source. A top-up Refund is actionable only when the Charge reports a final full refund; partial or non-final Refund evidence fails closed for operator reconciliation rather than assigning an arbitrary fractional token value. Commissioning on initial subscription Checkout is recognized only from a positive paid invoice carrying the frozen subscription metadata and the exact privately mapped commissioning Price.

## Worker and operator boundaries

- `BillingProcessor.ProcessOne` leases one verified inbox event and applies bounded retry.
- `BillingReconciler.ProcessOne` leases one requested Subscription refresh and retrieves current provider state.
- `BillingInbox.Replay` can requeue only a stored, signature-verified event that is processed or failed; it cannot inject a payload.
- `BillingProjectionRepository.QueueReconciliation` is the boundary for scheduled drift scans and operator-requested refresh.

`spyglass billing-admin inspect|replay-event|refresh-subscription` is the production operator interface. Every invocation requires exact environment confirmation and a signed phishing-resistant authorization bound to action, Stripe mode, bounded inspection limit, and exact target ID. The authorization ID/mode are appended to the durable reason before PostgreSQL is opened.

The short-lived database role receives only connection/schema usage and execute on the three security-definer functions:

```sql
GRANT EXECUTE ON FUNCTION public.spyglass_inspect_billing_failures(uuid,text,text,text,text,integer) TO spyglass_billing_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_replay_billing_event(uuid,text,text,text,text,text) TO spyglass_billing_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_queue_billing_subscription_refresh(uuid,text,text,text,text,text) TO spyglass_billing_operator;
```

It receives no direct `SELECT`, `INSERT`, `UPDATE`, or `DELETE` on inbox, Subscription, entitlement, Account, or audit tables. Revoke the credential after the operation.

Common configuration:

```powershell
$env:SPYGLASS_ENVIRONMENT = 'staging'
$env:SPYGLASS_CONFIRM_ENVIRONMENT = 'staging'
$env:SPYGLASS_STRIPE_MODE = 'test'
$env:SPYGLASS_OPERATOR_ID = 'operator@example.com'
$env:SPYGLASS_OPERATOR_REASON = 'Ticket IO-123: inspect failed Stripe projection work'
spyglass billing-admin inspect
```

Replay requires `SPYGLASS_STRIPE_EVENT_ID=evt_...`; the database accepts only a stored, signature-verified `processed` or `failed` event and never accepts replacement payload bytes. Refresh requires `SPYGLASS_STRIPE_SUBSCRIPTION_ID=sub_...`; it accepts only a known local Stripe Subscription in the exact configured mode and queues retrieval of current provider state. Both changes and inspection results are recorded in immutable `billing_operator_events` in the same transaction as the action. Direct database mutation is not an operator interface.

## Environment validation gate

1. Run migrations against disposable PostgreSQL and verify the restore procedure.
2. Create test-mode Product, recurring Prices, portal configuration, and version-pinned webhook endpoint.
3. Exercise subscription and payment-mode Checkout completion, async completion, combined and later commissioning, token top-up issuance, exact replay, full/partial Refund handling, trial-defense, current-Price upgrade/downgrade, duplicate and delayed delivery, failed payment/read-only remediation, collection pause, cancellation-at-period-end, cancellation, recovery, invoice and dispute invalidation.
4. Confirm the Checkout return page remains `processing` until projection completes.
5. Compare local Subscriptions and snapshots with Stripe through the reconciliation queue.
6. Rotate the test webhook secret and verify overlap/retirement before live rollout.
