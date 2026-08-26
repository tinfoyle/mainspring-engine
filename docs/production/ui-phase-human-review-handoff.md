# UI-focused phase — human review handoff

- Status: local construction boundary reached; not release-approved
- Audited implementation snapshot: `c0e82f1` on `main`; this handoff adds documentation only
- Review date: unset
- Reviewer: unset
- Approval record: unsigned

This handoff separates implemented local behavior from the decisions and external evidence still required to exit UI7. It is not an approval, legal conclusion or deployment instruction. Stage, GHCR, LKE and production work remain outside this checkpoint.

## Locally implemented and verified

| Requirement | Current evidence |
|---|---|
| Mobile-first Vue SPA with Your Turn as the default workflow | All launch routes use the Vue shell and generated contracts. The exact built artifact is exercised across desktop engines, phone widths, tablet, 320-pixel reflow, forced colors, reduced motion, 200% text and emulated 400% browser scale. |
| Rendered acquisition site, feature inventory, pricing and Checkout handoff | Seventeen Nuxt-rendered public routes have canonical, metadata, structured-data, link, accessibility and bundle-budget gates. Checkout retains an explicitly applied referral independently of analytics consent. |
| GDPR-capable consent and analytics | Equal accept/reject controls, immutable consent history, immediate browser-subject erasure, consent-bound event ingestion, bounded first-party conversion continuity, raw-event retention and privacy-bounded aggregate reporting are implemented. Marketing tracking is fixed off. |
| Authenticated privacy rights | Access, correction, erasure, restriction, objection and portability intake covers Identity, Account, Affiliate and Analytics. Open duplicates are denied, deadlines are a database-enforced clamped calendar month, and an execute-only identity-free due queue precedes audited exact inspection. |
| Customer privacy communication | Customers can refresh their safe request projection, retain prior history through a read failure, see update times and understand every open and terminal state. Operator evidence, staff identity, reasons and case material stay outside the customer contract. |
| Affiliate customer flow | An ordinary User can enroll only when the program is opened under an approved mode, receive and replace a generated code, share a proposal-only link, accept terms, inspect identity-free aggregate/monthly history, export identity-owned data and open structured appeals or reviews. |
| Affiliate commercial integrity | Explicit Checkout application, multi-Account self-referral denial, immutable attribution, replay-safe recurring earnings, append-only Refund/lost-dispute reversals, adverse-before-earning reconciliation, permanent retired-code non-reuse and customer-identity concealment are covered locally. |
| Operator boundaries | Privacy fulfillment, Affiliate lifecycle, Affiliate support and analytics reporting use separate audited execute-only database roles and do not grant direct table access. |

The exact UbuntuRojo Docker gate for the snapshot passed 393 applicable browser checks with eleven intentional compact-menu skips, 72 private UI tests, 22 public tests, 42 API-client tests, all seventeen rendered public routes, type checking, lint, both production builds, gzip budgets and a high-severity dependency audit with zero findings. The current private/public JavaScript budgets measured 113.8/90.4 KiB gzip and CSS measured 10.4/4.7 KiB gzip.

## Owner decisions required

### Affiliate commercial register

The launch flags must remain closed until every item below is recorded in an approved revision of the [Affiliate terms draft](affiliate-program-terms-launch-draft.md):

- **Decided 2026-08-26:** launch pricing is `$50.00 USD` (`5,000` minor units) per month per team plus an optional one-time `$250.00 USD` (`25,000` minor units) onboarding/commissioning package, before applicable Stripe-calculated tax. The checked-in `$49` Team and `$149` Operating candidates are obsolete; exact replacement Catalog identities and private Stripe Price mappings remain to be encoded;
- **Decided 2026-08-26:** the single `$50` subscription includes every completed launch package and feature. There are no software tiers or paid feature add-ons; operational limits protect capacity, and commissioning grants no additional entitlement;
- **Decided 2026-08-26:** flat team pricing includes 25 active members with no per-seat charge. Pending invitations reserve slots; admission above the combined limit fails atomically. Support may grant a reviewed versioned capacity increase without changing price or creating a tier;
- **Decided 2026-08-26:** include 1,000 logical Documents per team. Revisions share the Document slot; capacity releases after guarded physical deletion completes. Admission above the limit creates no partial state, and reviewed Support overrides do not create tiers;
- **Decided 2026-08-26:** retain the 100-active-Work-item limit. Active child items count individually; completed/cancelled history releases capacity. Support may grant a reviewed versioned override without changing price or features;
- **Decided 2026-08-26:** retain two concurrent Agent Runs per team. Queued Runs consume no slot before admission; terminal/cancelled/expired reservations release once. A third cannot start provider work, and Support may grant a reviewed versioned override;
- **Decided 2026-08-26:** there is no Free plan at launch. The checked-in `free-v1` offer, free entitlement and permanent-free acquisition/signup copy are migration work and may not appear in the effective launch Catalog or customer surface;
- **Decided 2026-08-26:** there is no free trial. Normal product access begins only after the first `$50` subscription invoice is fully paid and signed-webhook projection activates the team; zero-value/trial implementation states are defensive only;
- **Decided 2026-08-26:** the first unresolved renewal failure starts a 30-day clock. Days 0–7 are read-only remediation; afterward only billing, security, privacy, export and sign-out remain. Projected payment before day 30 cancels deletion; otherwise guarded Account/live-data erasure completes at day 30 while required evidence follows separate restricted retention;
- **Decided 2026-08-26:** after Account erasure, delete the live User identity only when it has no Membership in another Account and no active Affiliate relationship. Otherwise preserve the identity and unrelated authority while removing every erased-Account link;
- **Decided 2026-08-26:** send idempotent email and in-app notices immediately, at day 7, day 23 and day 29. State the team, exact deletion time, payment/export links and possible orphaned-identity deletion; projected recovery cancels unsent notices and Stripe dunning is supplemental;
- **Decided 2026-08-26:** voluntary cancellation retains full access through paid period end, then restricts access and begins the same 30-day deletion clock. Withdrawal before term end prevents it; projected resubscription before deletion cancels it. Notify at scheduling, term end, day 23 and day 29;
- **Decided 2026-08-26:** registration creates a verified identity and inactive team shell. Before signed-webhook payment projection, the owner can use only security, privacy, billing/checkout and sign-out and has no product entitlement. Redirect, cancellation, abandonment, pending or failure never activates the team;
- **Decided 2026-08-26:** each qualifying paid renewal earns a fixed `$10.00 USD` (`1,000` minor units), independent of any tax Stripe adds to the customer invoice;
- **Decided 2026-08-26:** the initial qualifying paid invoice creates the first pending earning. Each later qualifying successfully paid renewal makes only the immediately preceding pending earning available for settlement and creates a new pending earning. Cancellation does not claw back earnings already available;
- **Decided 2026-08-26:** when a subscription actually terminates before another qualifying renewal, its unmatched pending earning expires through immutable void evidence. Scheduled cancellation alone does not void it; withdrawing the schedule and renewing successfully preserves normal maturity;
- **Decided 2026-08-26:** a customer discount applies the same percentage reduction to the Affiliate earning, calculated from the eligible subscription charge after discounts and before tax. At full price the earning is `$10`; a 10% discount produces `$9`;
- **Decided 2026-08-26:** fractional-cent earnings round independently per invoice to the nearest cent, with an exact half-cent rounded upward and no carried fractional balance;
- **Decided 2026-08-26:** a successfully paid positive eligible proration charge creates an earning at the same 20% rate and performs the rolling maturity transition. Zero or negative proration creates no earning;
- **Decided 2026-08-26:** free and zero-value trials create no earning and do not advance maturity. The first positive paid invoice afterward is the initial qualifying invoice; a future paid-trial program requires a separate rule version;
- **Decided 2026-08-26:** attribution survives eligible upgrades and downgrades within the same Stripe subscription. Eligible positive charges continue at 20%; free/ineligible periods do not earn or advance maturity; a new subscription does not silently inherit attribution;
- **Decided 2026-08-26:** partial payment attempts create no earning and do not advance maturity. A fully paid invoice creates one invoice-bound earning regardless of the number of payment attempts or PaymentIntents; partial, uncollectible or void invoices create none;
- **Decided 2026-08-26:** there is no maximum earning-cycle count; qualifying earnings continue for the eligible lifetime of the attributed subscription;
- **Decided 2026-08-26:** any successful non-zero Refund voids the entire associated invoice earning. Pending earnings cannot mature; available earnings receive an immutable full reversal. Cancellation without a Refund remains non-adverse;
- **Decided 2026-08-26:** any finalized non-zero credit note follows the same full-void rule as a Refund. Draft or voided credit notes have no effect;
- **Decided 2026-08-26:** a final lost dispute or chargeback follows the same full-void rule. Open, warning, pending and won disputes have no effect;
- **Decided 2026-08-26:** settlement is account-credit-first. Available earnings accrue as billing credit for one Affiliate-owned Infinite Ocean Account; a balance at or above a separately approved threshold becomes eligible for payment by check. It is not an unrestricted cash wallet;
- **Decided 2026-08-26:** check eligibility begins at `$100.00 USD` of available credit. Pending earnings do not count toward the threshold;
- **Decided 2026-08-26:** checks are never automatic or directly self-service. The Affiliate contacts Support, completes recent passkey confirmation and verifies the authenticated Affiliate, mailing address and required tax information before Support initiates a reviewable request against reserved available credit;
- **Decided 2026-08-26:** Support determines each check amount case by case. Product code does not choose an amount or issue checks; immutable reservation/debit evidence still prevents double spending;
- **Decided 2026-08-26:** oldest available credit automatically reduces the Affiliate-owned Account's next invoice up to the amount due after Stripe calculates tax. Pending, voided, reversed and Support-reserved value is excluded; unused credit carries forward;
- **Decided 2026-08-26:** available credit has no arbitrary product expiration. Legally required dormant-property treatment is jurisdiction-specific rather than a hidden expiry rule;
- **Decided 2026-08-26:** credit cannot be sold, gifted, assigned or transferred to another User/Affiliate. With recent passkey confirmation, the same Affiliate may change the billing destination to another Infinite Ocean Account it owns; reserved credit cannot move;
- **Decided 2026-08-26:** lost, returned, stopped, reissued and uncashed checks are handled by Support procedure, not product automation. Software prevents double settlement but has no check-delivery/reissue state machine;
- **Decided 2026-08-26:** launch Affiliate enrollment is U.S.-only for U.S. persons/entities with a valid U.S. mailing address and approved payer tax documentation. Referred customers remain geographically unrestricted by this enrollment boundary;
- **Procedural 2026-08-26:** tax-document collection, validation, withholding and reporting belong to Support/accounting, not product logic. Spyglass does not ingest tax forms or implement a tax-verification workflow;
- **Decided 2026-08-26:** suspension stops new code use and attribution but existing locked subscriptions continue earning and maturing. Customer subscriptions are unaffected; history and appeal access remain;
- **Decided 2026-08-26:** permanent closure stops future attribution/earnings, expires the final pending earning through immutable void evidence, and preserves already available credit. Customers and prior available earnings are unaffected;
- **Decided 2026-08-26:** Affiliate financial/audit records are retained seven years after the later of closure or final settlement, reversal or tax-relevant transaction, then deleted or irreversibly minimized unless legal hold applies; and
- **Decided 2026-08-26:** after the seven-year retention window, erase readable retired Affiliate codes and their Affiliate/User linkage; retain only a protected irreversible identity-free fingerprint permanently to prevent code reuse;
- **Decided 2026-08-26:** a verified Affiliate erasure request closes enrollment, disables the code/link, stops future attribution/earnings and erases unneeded data while preserving customers and available credit. Required records remain access-restricted and outside marketing/ordinary product use until deadline; fulfillment discloses retained categories, purpose and deadline, and legal holds are scoped; and
- **Decided 2026-08-26:** the optional `$250` onboarding/commissioning package never earns Affiliate commission. It is excluded from the eligible basis even on a shared invoice, and package-only adverse evidence does not reverse the subscription earning;
- **Decided 2026-08-26:** the authorized owner may add commissioning to initial subscription Checkout or buy it later from Billing. The standard package is self-service-purchasable once per team; verified local state suppresses and rejects duplicates, and additional engagements route through Support;
- remaining privacy-controller and operational release fields.

Until approval, retain:

```text
SPYGLASS_AFFILIATE_ENROLLMENT_OPEN=false
SPYGLASS_AFFILIATE_ATTRIBUTION_ENABLED=false
SPYGLASS_AFFILIATE_SETTLEMENT_MODE=unconfigured
SPYGLASS_AFFILIATE_TERMS_VERSION=1
SPYGLASS_AFFILIATE_RULE_VERSION=1
```

Do not flip only the flags. Startup deliberately rejects an open program with an unconfigured settlement mode or a rule version absent from the immutable database rule set.

### Privacy and compliance register

Complete and approve the release-owner fields in the [processing registry](privacy-processing-registry.md):

- controller identity, address and privacy contact;
- representative and data-protection officer determination;
- infrastructure, email, payment, observability and support subprocessors, locations, DPAs and transfer safeguards;
- legitimate-interest assessments and DPIA threshold decision;
- final privacy/cookie notice version and effective date;
- staff fulfillment, response-evidence, escalation and lawful-retention-limitation procedure;
- backup expiry/deletion behavior for every processing class;
- incident/breach assessment and notification ownership; and
- Affiliate tax, accounting-retention and erasure limitations after the settlement decision.

The software enforces evidence integrity and deadlines. It cannot decide whether a request may lawfully be declined or partly fulfilled, or how long regulated evidence must be retained.

## External acceptance still required

These items need real hardware, a real provider or an approved environment and are not substitutes for additional local fixtures:

1. Hosted Stripe test-mode Checkout, cancellation, delayed webhook, payment-attention and portal journeys using approved Catalog-to-Price mappings and a local endpoint-specific webhook secret.
2. Physical passkey creation, discoverable login, loss and recovery on supported platforms.
3. Current iOS Safari and Android Chrome device sessions.
4. Keyboard and screen-reader sessions with the release accessibility matrix.
5. Target-environment privacy/operator role grants and staff procedure rehearsal.

No target-environment work should begin merely because this handoff exists. Resume Stage and production planning only after the local policy-dependent configuration is approved and UI7's external certificates are recorded.

## Human review record

Leave the following fields unset until the owner has actually reviewed the linked material:

```text
Affiliate commercial register revision:
Launch subscription base price: USD 50.00 before applicable tax (owner decision 2026-08-26)
Affiliate earning: fixed USD 10.00 per qualifying paid renewal, independent of tax (owner decision 2026-08-26)
Initial invoice and rolling hold: initial invoice qualifies; each successor qualifying renewal releases only the prior earning (owner decision 2026-08-26)
Final pending earning: expires with immutable void evidence only when the subscription actually terminates (owner decision 2026-08-26)
Discount treatment: apply the same discount percentage to the pre-tax Affiliate earning (owner decision 2026-08-26)
Commission rounding: nearest cent per invoice, exact half-cent upward, no fractional carry (owner decision 2026-08-26)
Proration: positive eligible paid proration earns 20% and advances the rolling window; zero/negative proration earns nothing (owner decision 2026-08-26)
Free trials: zero-value trials earn nothing and do not advance maturity; first later positive invoice is initial (owner decision 2026-08-26)
Plan changes: eligible changes within the same subscription retain attribution; free/ineligible periods pause it; new subscriptions do not inherit it (owner decision 2026-08-26)
Partial payments: only fully paid invoice creates one invoice-bound earning; attempts never duplicate or advance maturity (owner decision 2026-08-26)
Maximum cycles: none; qualifying earnings continue while the attributed subscription remains eligible (owner decision 2026-08-26)
Refunds: any successful non-zero Refund voids the complete associated invoice earning (owner decision 2026-08-26)
Credit notes: any finalized non-zero credit note voids the complete associated invoice earning (owner decision 2026-08-26)
Disputes/chargebacks: final loss voids the complete associated invoice earning; non-final/won states do nothing (owner decision 2026-08-26)
Affiliate settlement decision: account-credit-first with threshold check eligibility (owner decision 2026-08-26)
Check eligibility threshold: USD 100.00 available credit; pending excluded (owner decision 2026-08-26)
Check initiation: Support-assisted only after recent passkey and identity/address/tax verification; never automatic/self-service (owner decision 2026-08-26)
Check amount: determined manually by Support case by case; no product amount-selection or check-issuance automation (owner decision 2026-08-26)
Billing-credit order: automatically apply oldest available eligible credit after tax, excluding unavailable/reserved value; carry remainder (owner decision 2026-08-26)
Credit expiration: none as product policy; jurisdictional dormant-property duties remain separate (owner decision 2026-08-26)
Credit transferability: non-transferable between people; same Affiliate may change to another owned billing Account with passkey when unreserved (owner decision 2026-08-26)
Check exceptions: Support procedure only; no delivery/reissue product workflow beyond preventing double settlement (owner decision 2026-08-26)
Affiliate launch jurisdiction: U.S. persons/entities with valid U.S. mailing address and approved tax documentation; customer geography unaffected (owner decision 2026-08-26)
Suspension: stop new attribution only; preserve existing-subscription earnings, customers, history and appeal (owner decision 2026-08-26)
Permanent closure: stop future earnings, void final pending, retain available credit, never affect customers or claw back available value (owner decision 2026-08-26)
Affiliate financial retention: seven years after later of closure or final tax-relevant ledger activity, then delete/minimize absent legal hold (owner decision 2026-08-26)
Retired Affiliate codes: after retention expiry erase readable code and Affiliate/User link; retain only an irreversible identity-free fingerprint for permanent non-reuse (owner decision 2026-08-26)
Affiliate erasure during retention: close enrollment and erase unneeded data; preserve customers/available credit; restrict required records until deadline and disclose the limitation (owner decision 2026-08-26)
Launch pricing: $50/month per team plus optional one-time $250 onboarding/commissioning package; legacy $49/$149 offers are obsolete (owner correction 2026-08-26)
Launch entitlement: one complete-product subscription with all completed packages/features; no software tiers/add-ons; commissioning grants none (owner decision 2026-08-26)
Team members: 25 active members included, pending invitations reserve slots, no per-seat charge; reviewed Support capacity override (owner decision 2026-08-26)
Documents: 1,000 logical Documents per team; revisions share a slot, completed guarded deletion releases it; reviewed Support override (owner decision 2026-08-26)
Active Work: 100 items per team; children count, terminal history releases capacity; reviewed Support override (owner decision 2026-08-26)
Agent concurrency: two admitted Runs per team; queued Runs do not count, terminal/cancelled/expired releases; reviewed Support override (owner decision 2026-08-26)
Commissioning Affiliate treatment: never commission-eligible; exclude its line from earning and reversal qualification (owner decision 2026-08-26)
Free plan: none at launch; remove free-v1 and permanent-free claims from effective Catalog and customer surfaces (owner decision 2026-08-26)
Free trial: none at launch; first fully paid $50 subscription invoice and signed-webhook projection begin access (owner decision 2026-08-26)
Failed renewal: seven-day read-only remediation, restricted access through day 30, then guarded Account/live-data erasure unless payment is projected first (owner decision 2026-08-26)
Orphaned identity: after Account erasure delete only when no other Account Membership and no active Affiliate relationship remain (owner decision 2026-08-26)
Nonpayment notices: email plus in-app at failure/day 7/day 23/day 29 with exact deletion time, recovery/export and identity consequence (owner decision 2026-08-26)
Voluntary cancellation: full access through paid term, then restricted access and 30-day erasure clock; notify at scheduling/term end/day 23/day 29 (owner decision 2026-08-26)
Registration/payment: verified identity plus inactive team shell first; restricted pre-payment routes; only signed Stripe webhook projection grants product access (owner decision 2026-08-26)
Commissioning purchase: optional in initial Checkout or later from Billing, self-service once per team; additional engagements via Support (owner decision 2026-08-26)
Affiliate terms version:
Affiliate rule version:
Privacy processing-registry revision:
Privacy/cookie notice version:
Staff privacy-fulfillment procedure revision:
External certificate exceptions accepted:
Owner name:
Decision timestamp:
Decision signature or revision-controlled approval reference:
```

## Resume order

1. Record the owner decisions above without enabling any feature flag.
2. Reconcile the approved economics with the published Catalog and add a new immutable commission rule/terms version if the candidate changed.
3. Run the full local PostgreSQL, Go, generated-contract and exact-artifact UI gates with the approved configuration still fail-closed by default.
4. Complete the provider and physical accessibility certificates.
5. Re-audit UI7 against the exact committed artifact.
6. Only then hand the immutable application and website digests to the separate Stage/production deployment plan.
