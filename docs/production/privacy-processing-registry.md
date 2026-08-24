# Launch privacy and analytics processing registry

- Status: Implemented technical registry; controller identity, vendor list and legal review remain release gates
- Registry version: 1
- Effective draft date: 2026-08-24
- Owner: Infinite Ocean release owner
- Related design: [Privacy, analytics and Affiliate architecture](privacy-analytics-affiliates.md)

This is the revision-controlled launch record for the new customer surface. It is an engineering and operations control, not a substitute for legal advice. Production may not enable optional analytics or Affiliate enrollment until the open release-owner fields and applicable legal review are complete.

## Processing classes

| Class | Purpose | Lawful basis / consent category | Data and recipients | Retention and rights |
|---|---|---|---|---|
| Privacy preference | Remember and prove the visitor's category choice | Compliance/legitimate operational need; strictly necessary | Random subject and decision IDs, policy version, surface, analytics/marketing booleans, time. Infinite Ocean-operated PostgreSQL only. | Until browser-subject erasure. Browser access/history and erasure are available through the privacy API. |
| Raw first-party analytics | Improve acquisition, checkout, onboarding and content-free product usability | Consent; `analytics` | Random privacy subject and event IDs, exact consent decision ID, registered event, bounded dimensions and times. Infinite Ocean-operated endpoint/PostgreSQL only at launch. | 395 days by default; automatic bounded pruning or immediate browser-subject erasure, whichever occurs first. No third-party browser SDK. |
| Affiliate enrollment | Operate the Affiliate agreement and code | Contract steps and legitimate fraud/commercial administration; not analytics consent | Affiliate ID, responsible User, owned settlement Account reference, code, accepted terms/rule versions and state. Internal staff and contracted infrastructure only until settlement is selected. | Active agreement plus the approved legal/accounting period. Access through Affiliate dashboard; correction/support, restriction and erasure limitation require an authenticated support workflow. |
| Referral attribution | Apply the customer's actively entered Affiliate code to one Checkout/subscription | Contract/commercial transaction | Opaque attribution ID, Affiliate ID, customer Account/Checkout references until Account erasure, offer/rule versions, subscription reference and state. Stripe receives only opaque attribution ID. | Preserved for commercial reconciliation; customer Account and Checkout references detach on Account erasure while non-identifying ledger evidence survives. |
| Commission ledger | Calculate and prove recurring Affiliate earnings | Contract and legal/accounting obligations | Affiliate/attribution/rule IDs, Stripe subscription and invoice references, cycle, amount, currency, state and timestamps. | Immutable for the approved accounting/legal period. Dashboard omits referred-customer identity. Reversals append; history is never edited. |
| Necessary application telemetry | Security, availability, fraud prevention and recovery | Legitimate interests and service necessity | Content-free route templates, result/error class, timings, bounded pseudonymous operational correlation. | Per the observability and security retention schedule; never used as optional product analytics. |

No launch processing permits fingerprinting, session replay, heatmaps, DOM/form capture, advertising pixels, customer content, full URLs/query strings/referrers, email, name, User/Account UUIDs, Stripe IDs or raw IP addresses in analytics events.

## Analytics event inventory

Every event requires an effective analytics consent receipt for the same privacy subject and surface. Common optional dimensions are `device_class`, `locale` and `route_name`; values are 1–80 characters from a restricted non-free-text alphabet. Events accept no fields except those listed.

| Event | Surface/purpose | Additional allowed dimensions |
|---|---|---|
| `landing_viewed` | Public landing reach | none |
| `primary_cta_selected` | Public CTA effectiveness | `cta_code` |
| `feature_viewed` | Public feature education | `feature_code`, `package_code` |
| `pricing_viewed` | Public pricing reach | none |
| `offer_selected` | Public offer intent | `offer_code` |
| `signup_handoff_started` | Public-to-private signup handoff | `offer_code`, `campaign_code` |
| `registration_started` | Private registration funnel | `offer_code` |
| `verification_completed` | Private identity verification milestone | none |
| `account_created` | Private onboarding milestone | none |
| `security_enrollment_completed` | Private security milestone | `method` |
| `checkout_reviewed` | Private checkout review | `offer_code`, `referral_present` |
| `referral_code_accepted` | Private active referral application | `offer_code`, `entry_method` |
| `checkout_redirected` | Private Stripe handoff | `offer_code`, `referral_present` |
| `checkout_returned` | Private checkout return | `offer_code`, `result` |
| `subscription_projected` | Private local billing projection | `offer_code`, `result` |
| `application_entered` | Private onboarding completion | `entry_point` |
| `your_turn_opened` | Private content-free queue usability | `queue_state` |
| `your_turn_item_completed` | Private first-value usability | `task_category`, `result`, `duration_bucket` |

The executable source of truth is `internal/modules/analytics`; unknown events, unsupported dimensions, prohibited identity/content fields, stale/future timestamps and events without matching consent fail closed at ingestion.

## Consent and rights operations

- `GET /api/v1/privacy/consent` returns the current category decision and whether a new policy version requires renewal.
- `PUT /api/v1/privacy/consent` records a new immutable decision. Rejecting both optional categories is a normal successful outcome.
- `GET /api/v1/privacy/consent/history` returns the decisions for the signed browser subject.
- `DELETE /api/v1/privacy/data` deletes that subject, all consent receipts and raw analytics, then clears the signed host-only preference cookie.
- Account export and Account erasure govern Account-owned data separately.
- The Affiliate dashboard returns the responsible User's enrollment and identity-free statement. Requests to correct Affiliate identity, restrict processing, object, close enrollment or obtain a machine-readable full identity export require the authenticated support process until self-service endpoints are added.

Optional analytics must stop before the next event after withdrawal. Referral attribution, authentication, security, checkout continuity, billing records and Affiliate ledger processing are not optional analytics and do not depend on analytics consent.

## Automated enforcement and evidence

- A signed `__Host-` cookie contains only a random subject reference, policy version and surface.
- The server binds each event to the exact immutable consent decision that allowed it.
- The event registry rejects arbitrary fields and known identity/content fields.
- PostgreSQL prevents consent-receipt mutation outside subject erasure and prevents analytics updates.
- The maintenance worker prunes raw events in batches under an execute-only database identity and exposes only aggregate backlog status.
- Disposable PostgreSQL tests prove consent history/erasure, exact replay, retention, Affiliate anti-self-referral, attribution lock, recurring commission replay and Account-erasure detachment.

## Production release-owner register

The following must be filled and approved before production:

- controller legal name, registration details, postal address and privacy contact;
- representative and data-protection officer details, if applicable;
- production infrastructure, email, payment, observability and support subprocessors, locations, DPAs and transfer safeguards;
- documented legitimate-interest assessments where that basis is used;
- DPIA threshold decision and rationale;
- final privacy/cookie notice version and policy effective date;
- authenticated data-subject request intake, identity verification, deadline tracking and response evidence;
- backup expiry/deletion behavior for each data class;
- Affiliate settlement mode, tax/payout data, accounting retention and erasure limitations;
- incident/breach assessment owner and supervisory-authority/data-subject notification workflow.

Marketing consent is reserved but has no launch processor, event, cookie or destination. Adding one requires a registry and policy-version change before code is enabled.
