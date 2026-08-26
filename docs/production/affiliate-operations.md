# Affiliate operations

Status: implemented and verified locally on 2026-08-25. The enrollment and attribution feature flags remain closed. Nothing in this runbook authorizes Stage or production use.

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

Successful Refund objects are semantically deduplicated even when Stripe emits both creation and update events. Partial refunds are retained as immutable evidence; the fixed commission is reversed once their aggregate reaches the frozen rule's full eligible invoice amount. A lost dispute reverses once its amount reaches that threshold. Pending/failed/canceled refunds and won or warning disputes do not reverse. Refund and dispute amounts are not added together, preventing one loss from being counted twice. An adverse event received before its earning is retained and evaluated atomically when the earning later arrives.

Every reversal is a new settled ledger entry bound to the original earning. The earning, provider evidence and reversal cannot be updated or deleted. Webhook replay returns the existing semantic result.

## Enrollment lifecycle

`affiliate-admin` is a short-lived operator job, not a serving process. Its actions are:

- `inspect`: record a content-free inspection without changing the enrollment version;
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
GRANT EXECUTE ON FUNCTION public.spyglass_transition_affiliate_enrollment(uuid,uuid,bigint,text,text,text,text)
  TO spyglass_affiliate_operator;
```

Do not grant this role direct access to `affiliate_enrollments`, `affiliate_enrollment_events`, the attribution tables, or the commission ledger. Workload identity maps this logical permission to `global.affiliate-operator`; no standing operator deployment is required.

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
