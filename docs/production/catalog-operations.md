# Catalog Publication Operations

Spyglass treats the Catalog as versioned production configuration. Package definitions, plan composition, offers, and private Stripe mappings change through an immutable, audited workflow; they are not edited in place and do not require a customer-specific deployment.

## Lifecycle

```text
draft -> in_review -> approved -> published -> retired
                                      ^             |
                                      +-- republish-+
```

- Creating a draft allocates its version under a PostgreSQL advisory lock. The submitted JSON becomes immutable immediately; revisions create another version.
- Paid offers require at least one active private Stripe Price mapping before review can begin. Test and live mappings are distinct.
- The draft creator cannot approve their own version. Every action requires an operator identity and a meaningful reason and writes `catalog_operator_events` in the same transaction.
- Publication can be immediate or scheduled with an RFC3339 effective time. Account API replicas poll for the latest effective publication and atomically replace their local immutable snapshot.
- Retiring a publication is refused unless another effective publication is available. A retired, previously approved version can be republished as a rollback, including a lower version number.
- Catalog JSON and published provider-price mappings are protected by database triggers from mutation or deletion outside the workflow.

## Credential boundary

Run `catalog-admin` as a short-lived operator job with a dedicated database role allowed to modify Catalog and Catalog audit tables. Do not give it account-api, notification, webhook, SMTP, or Stripe secret keys. Deployment access controls must authenticate the human operator; `SPYGLASS_OPERATOR_ID` records that external identity and is not itself authentication.

Required for every action:

```powershell
$env:SPYGLASS_DATABASE_URL = '<operator database secret>'
$env:SPYGLASS_OPERATOR_ID = 'operator@example.com'
$env:SPYGLASS_OPERATOR_REASON = 'ticket IO-123: publish reviewed annual offers'
```

## Create and review a version

Catalog JSON contains public package, plan, and offer identities only. It must not contain Stripe IDs.

```powershell
$env:SPYGLASS_CATALOG_FILE = 'C:\reviewed\catalog.json'
spyglass catalog-admin draft

$env:SPYGLASS_CATALOG_VERSION = '3'
$env:SPYGLASS_CATALOG_OFFER_CODE = 'team-monthly-v1'
$env:SPYGLASS_STRIPE_MODE = 'test'
$env:SPYGLASS_STRIPE_PRICE_ID = 'price_...'
spyglass catalog-admin map-price

$env:SPYGLASS_OPERATOR_REASON = 'ticket IO-123: request independent catalog review'
spyglass catalog-admin request-review
```

A different operator reviews the exact content hash, dependency graph, prices, currencies, tax presentation, effective dates, and Stripe dashboard objects:

```powershell
$env:SPYGLASS_OPERATOR_ID = 'reviewer@example.com'
$env:SPYGLASS_OPERATOR_REASON = 'ticket IO-123: approved after commercial and security review'
spyglass catalog-admin approve
```

Publish immediately by omitting `SPYGLASS_CATALOG_EFFECTIVE_AT`, or schedule it:

```powershell
$env:SPYGLASS_OPERATOR_ID = 'release-operator@example.com'
$env:SPYGLASS_OPERATOR_REASON = 'ticket IO-123: release approved catalog'
$env:SPYGLASS_CATALOG_EFFECTIVE_AT = '2026-09-01T13:00:00Z'
spyglass catalog-admin publish
```

## Rollback

Republish the last known-good retired version first. Because effective publication time—not the largest version number—selects the current Catalog, this makes the prior version current again. Then retire the faulty version.

```powershell
$env:SPYGLASS_CATALOG_VERSION = '2'
$env:SPYGLASS_OPERATOR_REASON = 'incident IO-456: restore last known-good catalog'
Remove-Item Env:SPYGLASS_CATALOG_EFFECTIVE_AT -ErrorAction SilentlyContinue
spyglass catalog-admin publish

$env:SPYGLASS_CATALOG_VERSION = '3'
$env:SPYGLASS_OPERATOR_REASON = 'incident IO-456: retire faulty catalog after rollback'
spyglass catalog-admin retire
```

Confirm the public Catalog API reports the intended `version` and `published_at` on every environment before closing the change or incident. Catalog publication changes future authorization inputs; it does not rewrite existing entitlement snapshots by itself. A separate audited recomputation/reconciliation workflow is required when a publication must change existing Accounts.
