# UI revamp milestone report

- Report date: 2026-08-26
- Scope: local construction only in UbuntuRojo Docker
- Overall UI-revamp status: **not complete**
- Milestone status: **commercial Catalog and AI Token foundation complete**
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
- The launch/browser fixtures, checkout path and server-rendered identity entry are aligned to the inactive-shell and `$50` offer model.

## Verification evidence

The exact local source passed:

```text
dockerized go test ./...
UI type checks, lint and production builds
72 private Vue component tests
22 public Nuxt tests
42 generated API-client tests
17 rendered public-route accessibility checks
private/public gzip budgets
393 Playwright checks; 11 intentional profile-specific skips
```

The Playwright matrix covered Chromium, Firefox and WebKit desktop; 360, 390 and 412 pixel phone widths; 320 pixel reflow; tablet; forced colors; reduced motion; 200% text enlargement; and 400% browser-scale reflow. No remote environment or provider was contacted.

## Why the revamp is not yet complete

The remaining work is feature construction, not hosting:

1. Move the private execution target snapshot from Persona-publication compatibility into the run-admission boundary, where complexity, provider, model, adapter and immutable customer rate are frozen together.
2. Connect AI Token reservation and settlement to real Agent execution. Insufficient balance must stop before provider contact, while admitted runs retain their frozen rate and exact retries remain idempotent.
3. Implement strong-authenticated AI Token top-up Checkout, grant issuance, whole-unused-grant refund reversal and governed promotion issuance. The Billing screen currently discloses candidates but deliberately does not pretend purchase is available.
4. Encode the optional one-time commissioning purchase and duplicate-suppression flow. Additional engagements remain a Support path.
5. Finish the approved Affiliate settlement extension: Account billing-credit application, Support-reserved check eligibility at `$100`, and the remaining line-item/reversal rules. Enrollment and attribution flags remain closed until this is complete and reviewed.
6. Finish the approved subscription nonpayment/cancellation remediation, notification and guarded 30-day erasure lifecycle.
7. Run a human product/visual review of the current local artifacts, with particular attention to Your Turn, onboarding, checkout and real-device mobile ergonomics. Automated reflow/accessibility evidence is strong but is not a substitute for that review.

## Next implementation order

1. Make complexity a provider-neutral backend contract and integrate token admission/settlement with Agent runs.
2. Complete token commerce and commissioning Checkout locally.
3. Complete Affiliate settlement and subscription lifecycle policy locally.
4. Perform the customer-facing visual/content refinement and human review against the exact local artifacts.
5. Re-run the complete local Docker gate and declare the feature-complete boundary.
6. Only after that boundary, resume the separate Stage and production release plan.
