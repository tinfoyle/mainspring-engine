# Affiliate program terms — launch policy draft

- Status: Commercial and legal draft; enrollment remains feature-flagged closed
- Terms version: 1 candidate
- Rule version: 1 candidate
- Draft date: 2026-08-24

An Affiliate uses their ordinary Infinite Ocean login and receives a generated public code after accepting the current terms with recent passkey confirmation. A referred customer must actively enter or apply that code during checkout review. The Affiliate must make a clear and conspicuous disclosure near any endorsement that they may receive recurring value from qualifying purchases.

Owner economics decisions (2026-08-26): the launch subscription has a **$50.00 USD base price before tax** (`5,000` minor units), and each qualifying successfully paid subscription invoice—including the initial invoice—creates a **$10.00 USD** (`1,000` minor units) Affiliate earning at full price. A customer discount applies the same percentage reduction to the earning, calculated from the eligible subscription charge after discounts and before tax; for example, a 10% discount produces a $45 charge and $9 earning. A successfully paid positive eligible proration charge also participates at the same 20% rate and performs the rolling maturity transition; a $25 eligible proration therefore creates a $5 earning, while zero or negative proration creates none. A free or zero-value trial creates no earning and does not advance the rolling window; the first positive paid invoice after the trial is the initial qualifying invoice. Any future paid-trial program requires a separately versioned rule. Attribution survives eligible upgrades and downgrades within the same Stripe subscription: positive eligible charges continue at 20%, while a free or ineligible plan earns nothing and does not advance maturity. A separate subscription never inherits the attribution silently. There is no maximum cycle count: qualifying earnings continue for the eligible lifetime of the attributed subscription. Partial payment attempts create no earning and do not advance maturity; only the invoice reaching fully paid state creates one invoice-bound earning, regardless of how many payment attempts or PaymentIntents satisfied it. An invoice left partial, made uncollectible or voided creates none. Any successful non-zero Refund voids the entire earning for its associated invoice: a pending earning cannot mature, while an available earning receives an immutable full reversal. Cancellation without a Refund remains non-adverse. A fractional-cent result is rounded independently per invoice to the nearest cent, with an exact half-cent rounded upward; no fractional balance carries between invoices. Stripe calculates and adds applicable tax to the customer's total; tax neither increases nor decreases the Affiliate earning. An earning begins pending. The next qualifying successfully paid invoice makes only the immediately preceding pending earning available for settlement and creates the next pending earning. Cancellation does not claw back an earning already made available. If the subscription actually terminates before a successor qualifying invoice, the final pending earning expires through immutable void evidence; merely scheduling cancellation does not void it, and withdrawing that cancellation before a successful invoice preserves normal maturity. Before enrollment opens, the release owner must approve and encode:

- the exact eligible Catalog offer identity and Stripe Price mapping, reconciled to the approved $50/5,000-minor-unit base price rather than the observed $49/4,900-minor-unit draft;
- credit-note, dispute and chargeback reversal rules;
- account-credit or cash settlement, minimums, expiry, transferability and failed-settlement handling;
- supported Affiliate countries, tax documentation and legally required withholding/reporting;
- suspension, closure, code replacement, appeal and support rules; and
- record retention and data-subject-right limitations.

Technical invariants already fixed:

- self-referral is denied against every Account owned by the Affiliate User;
- an active Affiliate can replace the public code for future referrals only with recent passkey confirmation; every retired code remains permanently unavailable, while existing locked attributions and ledger history are unchanged;
- one Checkout and subscription can lock at most one attribution;
- Stripe receives only an opaque local attribution ID;
- successful qualifying invoice processing is replay-safe and appends an immutable earning;
- one fully paid invoice creates at most one earning even when multiple payment attempts or PaymentIntents satisfy it;
- successor-invoice maturity and terminal-subscription voiding append immutable evidence rather than updating or deleting the original pending earning;
- reversals append evidence rather than rewriting an earning;
- customer identity and business content never appear in the Affiliate statement; and
- analytics consent has no effect on referral or commission state.

Until the commercial register is approved, `SPYGLASS_AFFILIATE_ENROLLMENT_OPEN` and `SPYGLASS_AFFILIATE_ATTRIBUTION_ENABLED` remain `false`, `SPYGLASS_AFFILIATE_SETTLEMENT_MODE` remains `unconfigured`, and the UI must describe the program as unavailable rather than advertise candidate economics. `SPYGLASS_AFFILIATE_TERMS_VERSION` and `SPYGLASS_AFFILIATE_RULE_VERSION` select the exact accepted policy and immutable database rule; opening enrollment or attribution while settlement is unconfigured fails process startup, enabling either gate requires the selected rule to exist, and enrollment requests fail closed when their settlement Account shape does not match the selected mode.
