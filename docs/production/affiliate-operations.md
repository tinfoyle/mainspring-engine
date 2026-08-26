# Affiliate operations

Status: the original fixed-rule kernel was verified locally on 2026-08-25; owner policy decisions recorded on 2026-08-26 require a new immutable rule, ledger transitions and projector verification before launch. The enrollment and attribution feature flags remain closed. Nothing in this runbook authorizes Stage or production use.

The customer dashboard derives a proposal-only link from the application origin and generated public code. It carries no Affiliate identity, customer identity, offer, analytics subject or commercial attribution. Checkout displays the proposed code but requires the customer to select **Apply** before server validation. Suspension or closure removes the URL and disables both code/link copying while preserving the immutable statement and structured support path.

For both a new and returning customer, the complete relative Checkout destination survives sign-in and the necessary identity continuation. Account registration uses the encrypted queued verification email; password recovery preserves the same destination through its anti-enumeration response and encrypted reset email. Both return through password setup and final sign-in. The browser layer accepts only a relative same-origin `return_to`; either emailed link may carry the public code because it is intentionally shareable, but carries no Affiliate or referred-customer identity. This continuity never applies the code, creates an attribution or depends on analytics consent.

An active Affiliate may deliberately replace the public code after recent passkey confirmation. The request carries the observed enrollment version; stale requests fail without changing authority. Replacement retires the previous code before publishing a server-generated successor. Retired codes are permanently reserved in `affiliate_public_code_history`, cannot be looked up or reissued for future referrals, and cannot be changed or deleted. Already locked subscription attributions and their commission ledger are unaffected. The Vue dashboard uses a two-step warning because existing shared links stop working for new customers.

The customer statement counts only locked subscription attributions and never exposes their customer-side identifiers. It presents immutable commission entries in UTC calendar-month groups with earned and reversed subtotals; overall pending, settled and reversed totals remain authoritative server aggregates. A review request binds to the opaque commission-entry ID already present in that Affiliate's statement.

## Provider event projection

The billing worker projects Affiliate commercial evidence only from events already accepted by the signed Stripe webhook inbox. The launch webhook selection must include:

- `invoice.paid` for a qualifying earning;
- `refund.created` and `refund.updated` so a Refund that becomes `succeeded` is observed; and
- `charge.dispute.closed` so only a final `lost` dispute is adverse.

The pinned invoice parser accepts the Dahlia `payments.data[].payment.payment_intent` shape and the legacy top-level `payment_intent` during a rolling API-version transition. A commission stores the opaque Invoice, Subscription and PaymentIntent IDs but no customer, payment-method or referred-business data.

Approved adverse-event target (2026-08-26): successful Refund objects, finalized credit notes and disputes are semantically deduplicated across lifecycle events. Any successful non-zero Refund, finalized non-zero credit note, or final lost dispute/chargeback voids the entire commission associated with that invoice. A pending earning cannot mature; an available earning receives one immutable full reversal. Pending, failed or canceled refunds; draft or voided credit notes; and open, warning, pending or won disputes do not reverse. An adverse event received before its earning is retained and evaluated atomically when the earning later arrives. The current full-amount-threshold projector has no credit-note path and does not satisfy this target; it must be replaced before either launch flag opens.

Every reversal is a new settled ledger entry bound to the original earning. The earning, provider evidence and reversal cannot be updated or deleted. Webhook replay returns the existing semantic result.

## Enrollment lifecycle

`affiliate-admin` is a short-lived operator job, not a serving process. Its actions are:

- `inspect`: record a content-free inspection without changing the enrollment version;
- `inspect-risk`: record an inspection and return only content-free aggregate risk signals for manual review;
- `suspend`: stop new code attribution while preserving locked attributions and ledger history;
- `activate`: reactivate a suspended enrollment at the exact current version; and
- `close`: terminally close an enrollment. Closure cannot be reversed.

Every action requires the standard phishing-resistant operator authorization envelope, an exact environment confirmation, a bounded reason, and an exact Affiliate ID. Mutations additionally require the observed enrollment version.

Example shape, with secrets and authorization material supplied through the environment-specific secret mechanism:

```sh
SPYGLASS_ENVIRONMENT=local \
SPYGLASS_CONFIRM_ENVIRONMENT=local \
SPYGLASS_AFFILIATE_ID=00000000-0000-4000-8000-000000000000 \
SPYGLASS_AFFILIATE_VERSION=1 \
spyglass affiliate-admin suspend
```

The command also requires `SPYGLASS_DATABASE_URL`, `SPYGLASS_OPERATOR_ID`, `SPYGLASS_OPERATOR_REASON`, `SPYGLASS_OPERATOR_AUTH_ISSUER`, `SPYGLASS_OPERATOR_AUTH_VERIFY_KEYS`, and `SPYGLASS_OPERATOR_AUTHORIZATION`. Do not place database credentials or authorization envelopes in shell history.

The environment database role should be execute-only:

```sql
CREATE ROLE spyglass_affiliate_operator NOLOGIN;
GRANT USAGE ON SCHEMA public TO spyglass_affiliate_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_inspect_affiliate_enrollment(uuid,uuid,text,text,text)
  TO spyglass_affiliate_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_inspect_affiliate_risk(uuid,uuid,text,text,text)
  TO spyglass_affiliate_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_transition_affiliate_enrollment(uuid,uuid,bigint,text,text,text,text)
  TO spyglass_affiliate_operator;
```

Do not grant this role direct access to `affiliate_enrollments`, `affiliate_enrollment_events`, the attribution tables, or the commission ledger. Workload identity maps this logical permission to `global.affiliate-operator`; no standing operator deployment is required.

### Content-free risk review

`inspect-risk` writes a distinct immutable `risk_inspected` lifecycle event, then summarizes valid referral reservations in the preceding 24 hours and public-code replacements in the preceding 30 days. It returns the enrollment state/version, observation windows, reservation and distinct-account counts, repeated-account count, maximum reservations associated with one Account, aggregate cross-Affiliate code-cycling count, locked-attribution count, largest Account share in basis points, code-replacement count, and deterministic review flags. It never returns a referred Account or User ID, a public code, Checkout ID, Stripe/provider identifier, or customer content.

The current manual-review thresholds are:

- repeated Checkout creation: at least three valid reservations for one referred Account in 24 hours;
- cross-Affiliate code cycling: at least one referred Account has valid reservations with more than one Affiliate in 24 hours;
- referral concentration: at least ten valid reservations in 24 hours and one Account represents at least 50% of them; and
- rapid code replacement: at least three retired codes in 30 days.

These are investigation signals, not findings of abuse. The command cannot change enrollment state. A reviewer must evaluate context and use a separate, freshly authorized `suspend` action at the observed enrollment version when that decision is justified. Rejected or invalid code validations do not create an attribution and therefore are not represented in this summary.

Checkout applies a separate distributed validation budget before Affiliate code lookup: twenty code-bearing attempts per rolling 15-minute window. The rate-limit key is a one-way digest of the already pseudonymized network actor plus Account ID; neither an IP address, Account ID nor submitted code is stored in the limiter. Account scoping avoids one shared network consuming another Account's budget. Exhaustion fails before code lookup or Stripe access with `429 affiliate_code_rate_limited` and `Retry-After: 900`; missing network identity or limiter failure fails closed as billing unavailable. Code-free Checkout does not consume this budget. The restore-gated identity-maintenance worker removes stale limiter rows after 24 hours in bounded, replica-safe batches and exposes only aggregate retention status.

## Customer appeals and commission reviews

An enrolled Affiliate can submit one of two structured requests from the ordinary authenticated Vue dashboard:

- `enrollment_appeal`, only while the enrollment is suspended or closed; or
- `commission_review`, bound to one commission entry owned by that Affiliate.

The request contains no free text, attachment, referred-customer identity, payment method, or referred-business detail. Submission and pre-review cancellation require the authenticated User and exact application Origin. Owner-scoped reads and generic not-found responses prevent another User from probing commission or request identifiers. Only one open request for the same subject is allowed.

`affiliate-support-admin` is a separate short-lived operator job. `inspect` records an audit event, `start-review` moves a submitted request to review at its exact version, and `resolve` records either `approved` or `denied` after review. Approval resolves the support request; it does not automatically reactivate an enrollment or mutate a commission entry. Any commercial correction or enrollment transition remains a separate reviewed operation at its own least-authority boundary.

Example decision shape:

```sh
SPYGLASS_ENVIRONMENT=local \
SPYGLASS_CONFIRM_ENVIRONMENT=local \
SPYGLASS_AFFILIATE_SUPPORT_REQUEST_ID=00000000-0000-4000-8000-000000000000 \
SPYGLASS_AFFILIATE_SUPPORT_VERSION=2 \
SPYGLASS_AFFILIATE_SUPPORT_OUTCOME=approved \
spyglass affiliate-support-admin resolve
```

The command uses the standard operator authorization variables described above. Its environment database role is execute-only:

```sql
CREATE ROLE spyglass_affiliate_support_operator NOLOGIN;
GRANT USAGE ON SCHEMA public TO spyglass_affiliate_support_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_inspect_affiliate_support_request(uuid,uuid,text,text,text)
  TO spyglass_affiliate_support_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_transition_affiliate_support_request(uuid,uuid,bigint,text,text,text,text,text,text)
  TO spyglass_affiliate_support_operator;
```

Do not grant this role direct support-request/event table access or any enrollment, attribution, commission, settlement, Stripe, or serving credential. Immutable events retain each customer and staff transition. Workload identity maps this logical permission to `global.affiliate-support-operator`.

## Launch boundary

The operational ledger, lifecycle and structured review channel do not approve candidate economics or settlement. Enrollment and attribution flags stay closed until the release owner approves commercial terms, settlement, disclosure handling, and legal/privacy/vendor/transfer decisions. Stage and production credentials are not required for local verification.

Stripe references: [event types](https://docs.stripe.com/api/events/types), [Refund object](https://docs.stripe.com/api/refunds/object), [Dispute object](https://docs.stripe.com/api/disputes/object), and [Invoice Payment object](https://docs.stripe.com/api/invoice-payment/object).
