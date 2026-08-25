# Affiliate operations

Status: implemented and verified locally on 2026-08-25. The enrollment and attribution feature flags remain closed. Nothing in this runbook authorizes Stage or production use.

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

## Launch boundary

The operational ledger and lifecycle do not approve candidate economics or settlement. Enrollment and attribution flags stay closed until the release owner approves commercial terms, settlement, disclosure, support/appeal handling, and legal/privacy/vendor/transfer decisions. Stage and production credentials are not required for local verification.

Stripe references: [event types](https://docs.stripe.com/api/events/types), [Refund object](https://docs.stripe.com/api/refunds/object), [Dispute object](https://docs.stripe.com/api/disputes/object), and [Invoice Payment object](https://docs.stripe.com/api/invoice-payment/object).
