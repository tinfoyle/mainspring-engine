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

- **Decided 2026-08-26:** the launch subscription base price is `$50.00 USD` (`5,000` minor units) before tax, and Stripe calculates and adds applicable tax to the customer's total. The exact Catalog offer identity and Stripe Price mapping must be reconciled from the observed `$49`/`4,900` draft;
- **Decided 2026-08-26:** each qualifying paid renewal earns a fixed `$10.00 USD` (`1,000` minor units), independent of any tax Stripe adds to the customer invoice;
- **Decided 2026-08-26:** the initial qualifying paid invoice creates the first pending earning. Each later qualifying successfully paid renewal makes only the immediately preceding pending earning available for settlement and creates a new pending earning. Cancellation does not claw back earnings already available;
- coupon, proration, trial, upgrade, downgrade and partial-payment treatment;
- maximum earning cycles, if any;
- Refund, credit-note, dispute and chargeback reversal policy;
- disposition of the final unmatched pending earning when the subscription ends before another qualifying renewal;
- `account_credit` or `cash` settlement, including minimums, expiry, transferability and failed-settlement handling;
- supported countries, tax documentation, withholding and reporting;
- suspension, closure, code-replacement, appeal and support policy; and
- commercial/accounting retention and data-right limitations.

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
Affiliate settlement decision:
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
