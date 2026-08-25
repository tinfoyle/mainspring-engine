# Affiliate program terms — launch policy draft

- Status: Commercial and legal draft; enrollment remains feature-flagged closed
- Terms version: 1 candidate
- Rule version: 1 candidate
- Draft date: 2026-08-24

An Affiliate uses their ordinary Infinite Ocean login and receives a generated public code after accepting the current terms with recent passkey confirmation. A referred customer must actively enter or apply that code during checkout review. The Affiliate must make a clear and conspicuous disclosure near any endorsement that they may receive recurring value from qualifying purchases.

The current product candidate is **$10 USD for each qualifying successfully paid $50 USD monthly renewal**. This is not yet an operative promise. Before enrollment opens, the release owner must approve and encode:

- the actual published offer and exact eligible amount (the current Catalog appears to use $49/4,900 minor units, not $50/5,000);
- whether the initial invoice qualifies;
- coupon, tax, proration, trial, upgrade, downgrade and partial-payment behavior;
- maximum paid cycles, if any;
- refund, credit-note, dispute and chargeback reversal rules;
- the hold period and when an earning becomes settled;
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
- reversals append evidence rather than rewriting an earning;
- customer identity and business content never appear in the Affiliate statement; and
- analytics consent has no effect on referral or commission state.

Until the commercial register is approved, `SPYGLASS_AFFILIATE_ENROLLMENT_OPEN` and `SPYGLASS_AFFILIATE_ATTRIBUTION_ENABLED` remain `false`, `SPYGLASS_AFFILIATE_SETTLEMENT_MODE` remains `unconfigured`, and the UI must describe the program as unavailable rather than advertise candidate economics. `SPYGLASS_AFFILIATE_TERMS_VERSION` and `SPYGLASS_AFFILIATE_RULE_VERSION` select the exact accepted policy and immutable database rule; opening enrollment or attribution while settlement is unconfigured fails process startup, enabling either gate requires the selected rule to exist, and enrollment requests fail closed when their settlement Account shape does not match the selected mode.
