# Affiliate operations

## Operations Console surface

The local Operations Console exposes exact enrollment inspection, content-free risk signals and version-fenced reactivate/suspend/close transitions to `affiliate` and `operations_administrator` roles through a dedicated execute-only credential. Ticket/reason and stable staff actor identity flow into existing immutable Affiliate operator events. Settlement, checks, billing-credit conversion, legal retention/hold and monetary adjustment remain offline procedures and are intentionally absent from the console. Stage and production remain unchanged.

Status: the approved recurring-rule, adverse-event, account-credit-with-Support-check, verified-erasure restriction and seven-year minimization boundaries are implemented and verified locally as of 2026-08-27. The enrollment and attribution feature flags remain closed pending external legal, accounting, Support and provider acceptance. Nothing in this runbook authorizes Stage or production use.

The customer dashboard derives a proposal-only link from the application origin and generated public code. It carries no Affiliate identity, customer identity, offer, analytics subject or commercial attribution. Checkout displays the proposed code but requires the customer to select **Apply** before server validation. Suspension or closure removes the URL and disables both code/link copying while preserving the immutable statement and structured support path.

For both a new and returning customer, the complete relative Checkout destination survives sign-in and the necessary identity continuation. Account registration uses the encrypted queued verification email; password recovery preserves the same destination through its anti-enumeration response and encrypted reset email. Both return through password setup and final sign-in. The browser layer accepts only a relative same-origin `return_to`; either emailed link may carry the public code because it is intentionally shareable, but carries no Affiliate or referred-customer identity. This continuity never applies the code, creates an attribution or depends on analytics consent.

An active Affiliate may deliberately replace the public code after recent passkey confirmation. The request carries the observed enrollment version; stale requests fail without changing authority. Replacement retires the previous code before publishing a server-generated successor. During the approved seven-year retention period, retired codes remain immutable, cannot be looked up or reissued, and preserve the evidence needed for attribution and audit. At retention expiry, the readable code and Affiliate/User linkage are erased; only a protected irreversible, identity-free SHA-256 fingerprint remains permanently for collision rejection. The fingerprint table has no Affiliate, User, Account, provider or readable-code column and is unavailable to serving roles. Already locked subscription attributions and their commission ledger are unaffected. The Vue dashboard uses a two-step warning because existing shared links stop working for new customers.

The customer statement counts only locked subscription attributions and never exposes their customer-side identifiers. It presents immutable commission entries in UTC calendar-month groups with earned and reversed subtotals; overall pending, settled and reversed totals remain authoritative server aggregates. A review request binds to the opaque commission-entry ID already present in that Affiliate's statement.

## Provider event projection

The billing worker projects Affiliate commercial evidence only from events already accepted by the signed Stripe webhook inbox. The launch webhook selection must include:

- `invoice.paid` for a qualifying earning;
- `refund.created` and `refund.updated` so a Refund that becomes `succeeded` is observed;
- `credit_note.created` and `credit_note.updated` so an issued, line-complete credit note is observed; and
- `charge.dispute.closed` so only a final `lost` dispute is adverse.

The pinned invoice parser accepts the Dahlia `payments.data[].payment.payment_intent` shape and the legacy top-level `payment_intent` during a rolling API-version transition. A commission stores the opaque Invoice, Subscription and PaymentIntent IDs but no customer, payment-method or referred-business data.

Successful Refund objects, issued credit notes and final lost disputes are semantically deduplicated across lifecycle events. Any successful non-zero Refund or final lost dispute/chargeback voids the entire PaymentIntent-associated invoice commission. An issued non-zero credit note does so only when its complete line collection contains the eligible subscription invoice line; a commissioning-only credit note has no effect. A webhook with a paginated or incomplete credit-note line collection fails closed for reconciliation. A pending earning cannot mature; an available earning receives one immutable full reversal. Pending, failed or canceled refunds; draft or voided credit notes; and open, warning, pending or won disputes do not reverse. Adverse evidence received before its earning is retained and evaluated atomically when the earning later arrives.

Every reversal is a new settled ledger entry bound to the original earning. The earning, provider evidence and reversal cannot be updated or deleted. Webhook replay returns the existing semantic result.

## Enrollment lifecycle

`affiliate-admin` is a short-lived operator job, not a serving process. Its actions are:

- `inspect`: record a content-free inspection without changing the enrollment version;
- `inspect-risk`: record an inspection and return only content-free aggregate risk signals for manual review;
- `suspend`: stop new code attribution while preserving locked attributions and ledger history;
- `activate`: reactivate a suspended enrollment at the exact current version;
- `close`: terminally close an enrollment. Closure cannot be reversed;
- `set-check-threshold`: publish the next immutable settlement-policy version and reviewed USD threshold;
- `reserve-check`: after the Affiliate's recent passkey confirmation, reserve an operator-selected available amount;
- `settle-check`: record that the reserved amount was externally paid; and
- `release-check`: return a canceled reservation to available credit.
- `restrict-retention`: after terminal closure and verified erasure review, irreversibly remove the Affiliate graph from ordinary customer access;
- `hold-retention`: apply a scoped legal hold at the observed retention-control version; and
- `release-retention`: release that exact hold after the documented review.

The check actions are an accounting boundary only. They neither select a payment amount for Support nor issue, mail, stop or reissue a check. Every reservation freezes the settlement-policy version under which it was made. A later reversal of an earning already applied to a customer balance produces an idempotent Stripe customer-balance debit. A reversal of an earning already settled through Support creates a provider-free recovery offset against future available earnings; it does not attempt to claw back a check.

Suspension disables public-code lookup and new attribution only. Existing locked subscriptions continue producing qualifying earnings and rolling maturity. Permanent closure stops new attribution and all future earnings from its effective time, expires the final pending earning through immutable void evidence, and preserves already available credit for ordinary billing or Support-assisted check settlement. Neither state affects customer subscriptions, billing or entitlements; immutable history and structured appeal access remain visible. The projector enforces the closure cutoff and exact-replay terminal void.

Approved erasure policy (2026-08-26): a verified Affiliate erasure request invokes permanent closure, followed by the separately authorized one-way `restrict-retention` transition. The active code/share link, statement, support history and Affiliate export then disappear from ordinary product access; the Vue surface shows only a safe restricted-retention notice and privacy-request-history link. Available credit remains settleable through constrained billing/Support operations and referred-customer subscriptions remain untouched. Required financial, accounting, settlement and audit evidence remains outside ordinary product and marketing access until the approved deadline. A documented legal hold may extend only the scoped graph.

The restore-gated `affiliate-retention-worker` runs immediately and then on a bounded schedule. PostgreSQL computes eligibility as seven calendar years after the later of terminal closure or final commission, settlement, reversal-adjustment or support-case activity. A legal hold, open support case, reserved/credited settlement, pending provider adjustment, pending earning, or available unsettled earning blocks deletion. Eligible graphs are locked and rechecked before one transaction deletes enrollment, User/code linkage, attribution, raw ledger, provider-evidence linkage and staff/customer events. It retains only a random identity-free tombstone, per-currency aggregate totals and domain-separated retired-code fingerprints. Tombstones and fingerprints are immutable. The worker status exposes only total/eligible/oldest-age/minimized/failure counters and an alert flag.

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

Settlement-policy publication uses `SPYGLASS_AFFILIATE_SETTLEMENT_POLICY_VERSION`, `SPYGLASS_AFFILIATE_SETTLEMENT_NEW_POLICY_VERSION` and `SPYGLASS_AFFILIATE_CHECK_THRESHOLD_MINOR`. Check reservation uses `SPYGLASS_AFFILIATE_ID`, `SPYGLASS_AFFILIATE_CUSTOMER_SESSION_ID` and `SPYGLASS_AFFILIATE_CHECK_AMOUNT_MINOR`. Settlement or release uses `SPYGLASS_AFFILIATE_CHECK_RESERVATION_ID` and `SPYGLASS_AFFILIATE_CHECK_RESERVATION_VERSION`. Retention restriction and hold actions use `SPYGLASS_AFFILIATE_ID` plus the independently observed `SPYGLASS_AFFILIATE_RETENTION_VERSION`; they never reuse the enrollment version.

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
GRANT EXECUTE ON FUNCTION public.spyglass_publish_affiliate_settlement_policy(uuid,bigint,bigint,bigint,text,text,text)
  TO spyglass_affiliate_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_reserve_affiliate_support_check(uuid,uuid,uuid,uuid,bigint,text,text,text)
  TO spyglass_affiliate_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_transition_affiliate_support_check(uuid,uuid,bigint,text,text,text,text)
  TO spyglass_affiliate_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_set_affiliate_retention_hold(uuid,uuid,bigint,boolean,text,text,text)
  TO spyglass_affiliate_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_restrict_affiliate_retention(uuid,uuid,bigint,text,text,text)
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

### Retention worker authority

The persistent worker uses a separate login with no direct Affiliate or minimization-table access:

```sql
GRANT USAGE ON SCHEMA public TO spyglass_affiliate_retention_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_minimize_due_affiliates(timestamptz,integer),
  public.spyglass_affiliate_minimization_stats(timestamptz)
  TO spyglass_affiliate_retention_worker;
```

Its only policy inputs are `SPYGLASS_AFFILIATE_RETENTION_INTERVAL` (`1m`–`168h`, default `24h`), `SPYGLASS_AFFILIATE_RETENTION_BATCH` (`1`–`1000`, default `100`) and `SPYGLASS_AFFILIATE_RETENTION_ALERT_BACKLOG` (`1`–`1000000`, default `100`). The seven-calendar-year rule is fixed in the reviewed database boundary and is not a runtime duration knob.

## Launch boundary

The owner-approved recurring rule, account-credit-first Support-check settlement, verified-erasure restriction and readable-code/identity minimization mechanisms are implemented locally. This does not release-approve the program. Enrollment and attribution flags stay closed until Catalog/Stripe mapping, disclosures, legal/privacy/vendor/transfer review and Support/accounting procedure are complete. Stage and production credentials are not required for local verification.

Stripe references: [event types](https://docs.stripe.com/api/events/types), [Refund object](https://docs.stripe.com/api/refunds/object), [Credit Note object](https://docs.stripe.com/api/credit_notes/object), [Dispute object](https://docs.stripe.com/api/disputes/object), and [Invoice Payment object](https://docs.stripe.com/api/invoice-payment/object).
