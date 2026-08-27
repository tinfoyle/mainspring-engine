# UI revamp milestone report

- Report date: 2026-08-27
- Scope: local construction only in UbuntuRojo Docker
- Overall UI-revamp status: **not complete**
- Milestone status: **commercial Catalog, AI Token commerce, commissioning, Agent execution admission, Affiliate settlement and Affiliate GDPR retention complete**
- Explicit exclusions: GHCR, Hostinger Stage, LKE and production

## What is complete

- The customer architecture is the intended split: a rendered Nuxt acquisition site and a mobile-first Vue SPA, both using the generated customer API contract.
- The public landing, feature inventory, privacy and pricing routes; private navigation; Your Turn; checkout review; onboarding/Baseline; Billing; Agents; Affiliate; GDPR controls; and the retained package workspaces are implemented locally.
- Launch acquisition now presents one complete `$50 USD` monthly team subscription, applicable Stripe-calculated tax, and an optional `$250` commissioning path. There is no public Free plan or trial.
- Registration creates an inactive team shell with no product grants. Signed billing projection remains the activation authority.
- Catalog version 3 publishes `team-monthly-v2`, the configurable included AI Token grant, purchasable bundle candidates and five public complexity rates. Provider/model mappings stay out of the public Catalog.
- The backend now has a persistent Account-owned AI Token ledger with included, purchased and promotional cohorts; deterministic expiry ordering; reservation, settlement and release; exact-once invoice grant projection; adversity reversal; and safe shared balance output.
- Billing shows the shared token balance and configured grant/bundle terms. Persona editing presents only the five-stop complexity slider and accessible estimates; customer-facing Agent views no longer show vendor/model choices.
- Persona publication now carries a first-class `complexity` value across the domain, OpenAPI contract, generated clients and Vue editor. Caller-owned provider/model/fallback/reasoning fields are rejected, run responses omit selected-model output, portability exports exclude private execution targeting, and startup requires a complete validated five-level private target map.
- Agent dispatch now reserves the maximum Catalog-governed AI Token charge immediately before provider provisioning. Admission freezes the exact rate, provider/model/fallback and reasoning targets on the invocation; insufficient balance stops before provider contact and preserves the queued customer work.
- Successful projection settles trusted normalized input, cached-input, output and actual tool usage exactly once. Failed, unstarted, rejected and terminally dead-lettered work releases its reservation; exact retries retain the original frozen rate even after a later Catalog publication.
- Strong-authenticated owners and billing administrators can now create one-time Checkout for the exact AI Token bundle frozen in the local Catalog. Payment-mode Checkout metadata selects a durable local attempt but cannot reprice it; verified completion issues the frozen quantity once, and a verified Refund reverses only the surviving unused remainder without rewriting consumed history.
- The optional `$250` commissioning service is now a first-class one-time Catalog item with a private Stripe Price mapping. It can share initial subscription Checkout or use a later one-time Checkout, projects no entitlement or Affiliate earning, and durable Account purchase evidence suppresses and rejects a second self-service purchase.
- The approved Affiliate rule now computes discounted pre-tax subscription commission, advances only the immediately preceding earning on a qualifying successor invoice, voids the unmatched final earning on terminal closure and excludes commissioning lines.
- Available Affiliate earnings reserve oldest-first for the next Affiliate-owned Account invoice or, above the versioned threshold, for Support-assisted check accounting after recent Affiliate passkey confirmation. Reservation policy versions are immutable; release, settlement and replay cannot double spend value.
- Succeeded Refunds, final lost disputes and issued line-complete credit notes append one whole-earning reversal. Customer-balance credits receive compensating Stripe debits, settled Support checks produce provider-free future-earning recovery, commissioning-only credit notes do nothing, and adverse-before-earning delivery converges exactly once.
- Verified Affiliate erasure now has a separate one-way, version-fenced restriction after terminal closure. It removes code, statement, support history and export from ordinary customer access while preserving referred subscriptions and constrained settlement authority. The customer sees only a safe retention notice and privacy-history route.
- A dedicated restore-gated local worker minimizes closed Affiliate graphs after seven calendar years from the later of closure or final relevant activity. Legal holds, open cases, in-flight settlement/recovery and unsettled earnings block deletion. One transaction removes readable codes, User/Affiliate/provider links and raw evidence; only immutable identity-free tombstones, currency totals and retired-code fingerprints remain.
- Governed AI Token promotions now publish arbitrary quantities, effective windows, grant lifetime, per-Account redemption limits, global issuance caps, disclosure and an explicit no-stacking rule. Owner/billing-admin redemption requires recent passkey proof, survives exact retries across later Catalog publications, and appends one expiring labeled grant through a transactionally capped ledger boundary.
- The launch/browser fixtures, checkout path and server-rendered identity entry are aligned to the inactive-shell and `$50` offer model.

## Verification evidence

The exact local source passed:

```text
dockerized go test ./...
dockerized go test -race ./...
go vet, formatting and API-contract checks
UI type checks, lint and production builds
73 private Vue component tests
22 public Nuxt tests
44 generated API-client tests
17 rendered public-route accessibility checks
private/public gzip budgets
393 Playwright checks; 11 intentional profile-specific skips
complete fresh PostgreSQL migration ledger
```

The Playwright matrix covered Chromium, Firefox and WebKit desktop; 360, 390 and 412 pixel phone widths; 320 pixel reflow; tablet; forced colors; reduced motion; 200% text enlargement; and 400% browser-scale reflow. No remote environment or provider was contacted.

## Why the revamp is not yet complete

The remaining work is feature construction, not hosting:

1. Finish the approved subscription nonpayment/cancellation remediation, notification and guarded 30-day erasure lifecycle.
2. Connect the completed token top-up, promotion and commissioning boundaries to the final Billing/Checkout presentation and run the customer-facing visual/content refinement.
3. Run a human product/visual review of the exact local artifacts, with particular attention to Your Turn, onboarding, checkout and real-device mobile ergonomics. Automated reflow/accessibility evidence is strong but is not a substitute for that review.

## Next implementation order

1. Complete subscription lifecycle behavior locally.
2. Wire the completed commercial APIs into Billing/Checkout and perform the customer-facing visual/content refinement.
3. Complete human review against the exact local artifacts.
4. Re-run the complete local Docker gate and declare the feature-complete boundary.
5. Only after that boundary, resume the separate Stage and production release plan.
