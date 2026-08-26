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
| Raw first-party analytics | Improve acquisition, checkout, onboarding and content-free product usability | Consent; `analytics` | Random privacy subject and event IDs, exact consent decision ID, registered event, bounded dimensions and times. A signed 24-hour handoff mirrors reviewed private milestones onto the anonymous public subject without storing the private subject, User or Account. Infinite Ocean-operated endpoint/PostgreSQL only at launch. | 395 days by default; automatic bounded pruning or immediate browser-subject erasure, whichever occurs first. The cross-subdomain handoff cookie expires within 24 hours. No third-party browser SDK. |
| Privacy rights intake | Receive, authenticate and deadline-track data-subject requests | Legal obligation; strictly necessary | Request ID, authenticated User, right, scope, state, passkey-verification time, request/response-due/update times and immutable state events. Infinite Ocean-operated PostgreSQL and authorized staff only. | Retained for the approved compliance-evidence period. Duplicate open right/scope pairs are rejected; the requester can cancel a submitted request. Fulfillment evidence must identify erased data and any restricted-retention categories, purpose, deadline or scoped legal hold. |
| Affiliate enrollment | Operate the Affiliate agreement and code | Contract steps and legitimate fraud/commercial administration; not analytics consent | Affiliate ID, responsible User, owned settlement Account reference, current code, retention-bounded readable no-reuse code history, accepted terms/rule versions and state. Internal staff and contracted infrastructure only until settlement is selected. | A verified Affiliate erasure request closes enrollment, disables the active code/link, stops future attribution/earnings and erases unneeded data without affecting customers or available credit. Required records remain access-restricted and outside ordinary product/marketing use until seven years after the later of closure or final settlement, reversal or tax-relevant transaction, then delete or irreversibly minimize absent a scoped legal hold. Erase readable retired codes and Affiliate/User links at expiry; retain only a protected irreversible identity-free fingerprint permanently to prevent reissue. |
| Referral attribution | Apply the customer's actively entered Affiliate code to one Checkout/subscription | Contract/commercial transaction | Opaque attribution ID, Affiliate ID, customer Account/Checkout references until Account erasure, offer/rule versions, subscription reference and state. Stripe receives only opaque attribution ID. | Preserved for commercial reconciliation; customer Account and Checkout references detach on Account erasure while non-identifying ledger evidence survives. |
| Commission ledger | Calculate and prove recurring Affiliate earnings | Contract and legal/accounting obligations | Affiliate/attribution/rule IDs, Stripe subscription and invoice references, cycle, amount, currency, state and timestamps. | Seven years after the later of Affiliate closure or final settlement, reversal or tax-relevant transaction, then deletion or irreversible minimization unless legal hold applies. Dashboard exposes only aggregate locked-subscription count and UTC monthly statement groups, never referred-customer identity. Reversals append; history is never edited. |
| Necessary application telemetry | Security, availability, fraud prevention and recovery | Legitimate interests and service necessity | Content-free route templates, result/error class, timings, bounded pseudonymous operational correlation. | Per the observability and security retention schedule; never used as optional product analytics. |

No launch processing permits fingerprinting, session replay, heatmaps, DOM/form capture, advertising pixels, customer content, full URLs/query strings/referrers, email, name, User/Account UUIDs, Stripe IDs or raw IP addresses in analytics events.

## Analytics event inventory

Every event requires an effective analytics consent receipt for the same privacy subject and its registered public or private surface. Common optional dimensions are `device_class`, `locale` and `route_name`; values are 1–80 characters from a restricted non-free-text alphabet. `device_class` is additionally restricted to `desktop`, `phone` or `tablet`. Events accept no fields except those listed. Catalog, feature, package, campaign, CTA, locale and route codes remain bounded identifiers rather than categorical enums; every categorical dimension has its complete launch vocabulary below and is rejected when it does not match.

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
| `security_enrollment_completed` | Private security milestone | `method` (`passkey_recovery_codes`) |
| `checkout_reviewed` | Private checkout review | `offer_code`, `referral_present` (`true` or `false`) |
| `referral_code_accepted` | Private active referral application | `offer_code`, `entry_method` (`manual` or `link`) |
| `checkout_redirected` | Private Stripe handoff | `offer_code`, `referral_present` (`true` or `false`) |
| `checkout_returned` | Private checkout return | `offer_code`, `result` (`cancelled` or `returned`) |
| `subscription_projected` | Private local billing projection | `offer_code`, `result` (`active`, `attention` or `failed`) |
| `application_entered` | Private onboarding completion | `entry_point` (`checkout`, `your_turn` or `deep_link`) |
| `your_turn_opened` | Private content-free queue usability | `queue_state` (`empty` or `open`) |
| `your_turn_item_completed` | Private first-value usability | `task_category` (`information`, `review`, `approval` or `action`), `result` (`completed`), `duration_bucket` (`under_1m`, `1m_5m` or `over_5m`) |

The executable source of truth is `internal/modules/analytics`; unknown events, unsupported dimensions, prohibited identity/content fields, stale/future timestamps and events without matching consent fail closed at ingestion.

## Consent and rights operations

- `GET /api/v1/privacy/consent` returns the current category decision and whether a new policy version requires renewal.
- `PUT /api/v1/privacy/consent` records a new immutable decision. Rejecting both optional categories is a normal successful outcome.
- `GET /api/v1/privacy/consent/history` returns the decisions for the signed browser subject.
- `DELETE /api/v1/privacy/data` deletes that subject, all consent receipts and raw analytics, then clears the signed host-only preference cookie.
- On the private host, that deletion also erases the anonymous public acquisition subject when a still-valid handoff cookie proves the association, then clears the handoff cookie.
- Account export and Account erasure govern Account-owned data separately.
- The Affiliate dashboard returns the responsible User's enrollment and identity-free statement. `GET /api/v1/affiliate/data-export` requires a recent passkey confirmation and downloads that User's machine-readable enrollment, public-code history, sanitized lifecycle, attribution aggregates, provider-free commission ledger and sanitized support history. The database projection cannot return referred-Account IDs, checkout or subscription identifiers, payment-provider objects, staff actors or free-form reasons. Correction, restriction, objection, closure and legally reviewed erasure remain tracked authenticated rights workflows.

Optional analytics must stop before the next event after withdrawal. Public withdrawal also clears any live conversion handoff, and the database refuses a new mirror when the source subject's latest public decision does not allow analytics. Referral attribution, authentication, security, checkout continuity, billing records and Affiliate ledger processing are not optional analytics and do not depend on analytics consent.

## Automated enforcement and evidence

- A signed `__Host-` cookie contains only a random subject reference, policy version and surface.
- After a consented `signup_handoff_started`, a separate signed `__Secure-spyglass_analytics_handoff` cookie is scoped to the configured Infinite Ocean parent domain and `/api/v1`, is HTTP-only/Secure/SameSite=Lax, contains only the anonymous public subject, handoff event and expiry, and expires within 24 hours. The HMAC uses a separate domain label from the privacy-preference token.
- The server binds each event to the exact immutable consent decision that allowed it.
- Reviewed private milestones are mirrored into `analytics_conversion_events` under the public subject and public consent evidence. That table has no private-subject, User, Account or private-event identifier column; one receipt records at most one copy of each milestone.
- The event registry rejects arbitrary fields, known identity/content fields, events submitted on an unreviewed surface and unreviewed categorical values.
- PostgreSQL prevents consent-receipt mutation outside subject erasure and prevents analytics updates.
- The maintenance worker prunes raw events in batches under an execute-only database identity and exposes only aggregate backlog status.
- Disposable PostgreSQL tests prove consent history/erasure, exact replay, cross-host milestone aggregation without target identity, small-cohort suppression, retention, Affiliate anti-self-referral, pre- and post-lock code replacement, permanent retired-code non-reuse/immutability, attribution lock, recurring commission replay, identity-owned portability without referred-customer/provider/staff leakage and Account-erasure detachment.

## Production release-owner register

The following must be filled and approved before production:

- controller legal name, registration details, postal address and privacy contact;
- representative and data-protection officer details, if applicable;
- production infrastructure, email, payment, observability and support subprocessors, locations, DPAs and transfer safeguards;
- documented legitimate-interest assessments where that basis is used;
- DPIA threshold decision and rationale;
- final privacy/cookie notice version and policy effective date;
- approved staff fulfillment, response-evidence, escalation and legal-retention-limitation workflow for the implemented authenticated request intake;
- backup expiry/deletion behavior for each data class;
- Affiliate settlement mode, tax/payout data, accounting retention and erasure limitations;
- incident/breach assessment owner and supervisory-authority/data-subject notification workflow.

Marketing consent is reserved but has no launch processor, event, cookie or destination. Adding one requires a registry and policy-version change before code is enabled.

Launch enforcement amendment (2026-08-26): public, private and server-rendered consent controls solicit only the configured first-party analytics purpose. They describe Marketing tracking as not used rather than presenting a meaningless checkbox. The reserved transport field remains required for compatibility but is contractually fixed to `false`; the application service rejects `true` without appending a consent decision or setting a preference cookie. Immutable history can still display any earlier Marketing value as evidence rather than rewriting it. Exact built-artifact coverage passes all 393 applicable cases in the 404-case local browser matrix, with eleven intentional compact-navigation skips.

Rights-deadline enforcement amendment (2026-08-26): one-calendar-month response dates clamp at the following month's final valid day. Both the domain model and PostgreSQL enforce the UTC calendar-month value, and migration 55 repairs earlier overstated rows before validating the constraint. The privacy-rights operator can list an authorization-bound, deadline-ordered open queue without User, contact, Account, Affiliate or content fields. Queue access records only its opaque access ID, cutoff, limit, result count and operator audit fields and is immutable. Exact-request identity access still requires the separate audited inspection function.
