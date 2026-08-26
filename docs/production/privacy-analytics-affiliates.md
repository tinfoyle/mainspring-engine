# Privacy, analytics and affiliate architecture

- Status: GDPR-capable analytics scope approved for Phase 3; affiliate attribution approved in principle; settlement mode requires commercial approval
- Decision date: 2026-08-24
- Parent plan: [Phase 3 Vue customer-surface and SPA plan](phase-3-vue-spa-plan.md)
- Commercial boundary: [Website, Accounts, Packages, and Billing Architecture](accounts-packages-billing.md)
- Release boundary: Local construction only until the parent plan's exit gate passes

Implementation checkpoint (2026-08-25): global migrations 37–48, signed host-only preference references, immutable consent receipts/history, consent-bound first-party event ingestion, browser-subject erasure, 395-day raw-event pruning, short-lived public-to-private conversion continuity, privacy-bounded aggregate reporting, Affiliate enrollment/code/attribution/ledger services and generated HTTP contracts are implemented and pass fresh PostgreSQL 17 tests. The consented handoff uses a separate signed cookie for the configured Infinite Ocean parent domain and `/api/v1`, expires within 24 hours and mirrors only reviewed private milestones onto the anonymous public subject. It never stores the private subject, User, Account, email, payment identity or private event ID. The one-shot [analytics reporting boundary](analytics-reporting-operations.md) returns only reviewed bucket/event/surface/dimension aggregates, enforces at least five distinct consent subjects per row and gives its dedicated role no raw-table access. Enrollment and attribution remain off until the [Affiliate terms draft](affiliate-program-terms-launch-draft.md) commercial decisions are approved. The executable registry is paired with the [launch processing registry](privacy-processing-registry.md) and [privacy/cookie copy draft](privacy-cookie-notice-launch-draft.md). The public Vue surface resolves consent before optional landing, feature, pricing, offer and signup-handoff emission, keeps analytics off on lookup/save failure and exposes equal first-layer accept/reject/manage actions plus a persistent preference control. Optional event transport is navigation-safe and cannot mutate that consent projection or invoke application session-expiry behavior. The application origin presents its own equal-choice control when its host-only reference is undecided and records successful registration, verification, Account creation, completed owner security enrollment and first application entry only after authoritative transitions. Server-rendered onboarding markers use stable transition-time event envelopes across bounded retries and refreshes, while the Vue Security route requires an authoritative incomplete-to-ready posture transition and does not count recovery-code replacement. A transition before consent is not backfilled after later acceptance. Authenticated private subjects are linked to the current User; a privacy reference already owned by a different User is replaced rather than cross-linked. The private Privacy route combines consent control, confirmed immediate browser-subject erasure, direct Account export/lifecycle/identity-correction paths, and passkey-verified access, correction, erasure, restriction, objection and portability intake across identity, Account, Affiliate and analytics scopes. Confirmed browser erasure is single-flight and blocks navigation until the server responds. Requests have a calendar-month response deadline, reject duplicate open scope/right pairs and expose their current fulfillment state. The separate [privacy-rights operator boundary](privacy-rights-operations.md) records audited inspection, exact-version review and terminal resolution with an opaque evidence UUID/SHA-256 binding through execute-only database functions. The private checkout review actively applies and confirms Affiliate codes independently of analytics consent, uses stable purchase idempotency and waits for signed-webhook projection on return. The private Affiliate route exposes generated code, a proposal-only referral link and identity-safe aggregate ledger entries, gates enrollment on current terms/strong authentication/approved settlement mode, and makes no candidate payout promise; suspension removes the shareable URL and disables copying. The public terms route explicitly identifies the policy as a closed launch draft. Verified Stripe `invoice.paid`, succeeded Refund and lost-dispute events now append replay-safe earnings and full-qualifying-amount reversals without customer data; out-of-order adverse evidence is reconciled when an earning appears. The [Affiliate operator boundary](affiliate-operations.md) records immutable enrollment history and provides exact-version suspension/reactivation plus terminal closure through execute-only functions. The Affiliate dashboard now submits and cancels content-free enrollment appeals and commission-entry reviews; a distinct execute-only operator boundary records inspection, exact-version review and approved/denied resolution without automatically mutating enrollment or ledger state. Settlement approval and final legal/vendor/transfer release approval remain open.

Affiliate-code replacement amendment (2026-08-25): global migration 49 and the generated HTTP contract add passkey-confirmed, exact-version public-code replacement. A retired code is held in immutable no-reuse history, immediately stops future lookup and can never be issued again. Replacement before attribution invalidates the old proposal; replacement after subscription lock leaves the opaque attribution and commission ledger untouched. The earlier checkpoint's migration range is therefore extended through 49.

Affiliate privacy amendment (2026-08-26): global migrations 50–53 add content-minimized risk inspection, account-scoped Affiliate-code rate limiting with bounded pseudonymous retention, and a strong-authenticated customer-owned Affiliate JSON export. The execute-only export projection returns the Affiliate's enrollment, public-code history, sanitized lifecycle, aggregate attribution counts, provider-free commission ledger, and sanitized support history; its schema has no referred-Account, checkout, subscription, payment-provider, staff-actor, or free-form reason field. The Vue Privacy route downloads the no-store artifact without browser persistence. Fresh PostgreSQL and clean local Docker UI tests cover the boundary.

Affiliate-statement amendment (2026-08-25): the identity-safe statement now returns the authoritative count of locked referred subscriptions and the Vue dashboard groups immutable commission entries into UTC calendar-month statements with earned and reversed subtotals. Neither representation includes a referred User, Account, email, business name or customer content.

Affiliate acquisition-continuity amendment (2026-08-25): a same-origin Affiliate Checkout destination, including its public proposal code, now survives sign-in and both identity continuations: free-Account registration/verification and returning-customer password recovery/reset. The destination crosses each encrypted queued email and final sign-in, is validated as a relative local URL whenever it enters a rendered browser boundary, and does not alter the generic anti-enumeration recovery response. The code is still not applied or attributed until the customer deliberately selects **Apply**, confirms the reviewed Checkout and the server validates it. No optional event is required for continuity. Private `application_entered` measurement uses only `checkout`, `your_turn` and `deep_link`; direct Checkout acquisition is no longer mislabeled as Your Turn and the ingestion registry rejects any other entry value.

Analytics taxonomy completion amendment (2026-08-25): the executable launch registry now binds every event to its reviewed public or private surface and rejects cross-surface submission even when that surface has a valid consent receipt. It also fail-closes every categorical launch dimension: device class, security method, referral presence and entry method, Checkout return/projection result, application entry point, Your Turn queue state, task category, completion result and duration bucket. Tests accept the complete reviewed vocabularies and reject plausible but unreviewed values; bounded Catalog, feature, package, campaign, CTA, locale and route identifiers remain non-free-text codes. The revision-controlled processing registry lists the exact categorical vocabulary.

Consent-history visibility amendment (2026-08-26): the immutable receipt history is now customer-visible on both browser surfaces. The public preference manager links to public-host history and offers confirmed public browser-subject erasure; private Privacy combines private-host history with its current preference and verified rights workflows. Both show only policy, surface, time and optional-purpose choices, never rendering receipt/subject identifiers. A private history read independently links the subject to the authenticated User and replaces a stale subject already owned by another User before selecting history, closing a cross-identity race that would otherwise arise when current consent and history load separately. Exact-browser coverage proves public history, erasure, equal-choice restoration, optional-event silence, axe and reflow in every standard profile.

Privacy-rights deadline amendment (2026-08-26): the response calendar month clamps to the target month's last valid day, eliminating the January-31-to-March normalization error. PostgreSQL repairs any preexisting overstated value and enforces the same UTC calendar-month invariant on every row. The least-authority operator role can discover at most 100 open requests through an explicitly cutoff-bound `list-open` function. Results contain only opaque request workflow metadata, the listing creates immutable access evidence, and direct request/audit table access remains denied. Identity-bearing inspection and evidence-bound resolution remain distinct authorized operations.

## 1. Outcomes

The launch UI must measure acquisition and onboarding well enough to improve them without collecting customer business content or excluding EU customers. It must also support an affiliate program whose referral attribution and recurring earnings survive browser loss, webhook replay, subscription renewal and analytics-consent withdrawal.

Launch analytics prioritize:

1. the public landing page and product/feature discovery;
2. offer selection, signup and Stripe Checkout;
3. identity verification, Account creation, security enrollment and first successful application entry; and
4. first value in Your Turn without recording the task's content.

Affiliate attribution is commercial transaction state. It is not marketing analytics and cannot disappear when analytics consent is refused or withdrawn.

## 2. Fixed privacy decisions

- GDPR-capable operation is a Phase 3 product-surface release requirement, not a future regional enhancement.
- The public site and private application expose one clear, versioned privacy-preference experience.
- Strictly necessary storage is enabled by default. Analytics and marketing storage, scripts and requests remain disabled until the applicable consent is granted.
- The first consent layer gives equally accessible **Accept analytics**, **Reject non-essential** and **Manage preferences** actions. Optional categories are never preselected.
- Access to the landing page, signup, checkout and Spyglass cannot depend on optional analytics or marketing consent.
- Withdrawal is as easy as granting consent and takes effect before the next optional event is emitted.
- Consent is purpose-specific. A new purpose, vendor or materially expanded field set requires a new policy version and, where required, renewed consent.
- Consent receipts, referral state, product analytics and operational telemetry remain separate data classes with separate retention and access rules.
- Pseudonymous identifiers are treated as personal data until irreversible anonymization is proven.
- No fingerprinting, cross-site identity, advertising pixel, session replay, heatmap, keystroke capture, form recording or customer-content analytics is permitted at launch.

## 3. Lawful-purpose registry

Every emitted event and stored identifier must have one revision-controlled registry entry containing:

- stable event or data name;
- controller purpose and lawful basis;
- consent category, if any;
- public or private surface;
- exact allowed fields and enum values;
- prohibited fields;
- recipients and processor/subprocessor path;
- retention and aggregation schedule;
- User/Account access, erasure, restriction and objection behavior;
- international-transfer disposition; and
- owner and tests.

An event absent from the registry fails build and contract tests. Arbitrary event properties, full URLs, query strings, referrers, free text and provider payloads are rejected at the ingestion boundary.

## 4. Consent system

### 4.1 Preference model

The initial categories are:

- `necessary`: authentication, security, checkout continuity and privacy-preference storage;
- `analytics`: first-party acquisition and product-improvement measurement; and
- `marketing`: future advertising or campaign integrations. It remains operationally empty until a separately reviewed provider and event inventory exist.

The browser stores only a signed or integrity-protected preference reference and policy version. The server retains an immutable, content-minimized receipt history with random receipt ID, policy version, selected categories, public/private surface, effective time and optional withdrawal time. It does not use a full IP address or browser fingerprint as proof.

### 4.2 Enforcement

- Optional SDKs are not downloaded and optional endpoints are not called before consent.
- Route transitions and SPA hydration cannot race ahead of preference resolution.
- Server ingestion rechecks the receipt/category and rejects events that the browser should not have sent.
- Withdrawal disables optional emission immediately and schedules deletion or de-identification according to the event registry.
- Consent-denied and consent-withdrawn journeys are first-class automated tests.
- Necessary operational logs remain content-free and are governed by their documented non-consent lawful basis and retention.

## 5. Analytics architecture

### 5.1 Separate streams

| Stream | Purpose | Identity boundary |
|---|---|---|
| Acquisition | Landing, feature, pricing, campaign, signup and checkout funnel | short-lived anonymous session and one-time conversion receipt |
| Product | Onboarding and feature usability | environment-keyed pseudonymous User/Account subject, never exported to marketing |
| Operational | Availability, latency, error class and capacity | existing content-safe OpenTelemetry rules |
| Commercial | Checkout, subscription, affiliate attribution and commission | durable application aggregates; never an analytics source of authority |

The analytics sink is replaceable behind a first-party ingestion adapter. Browser code sends only reviewed event envelopes to an Infinite Ocean origin. No browser analytics SDK receives customer API responses or DOM scraping authority.

### 5.2 Launch funnel

The initial event inventory must measure, at minimum:

- landing impression and primary-call-to-action selection;
- feature/package and cross-package workflow views;
- pricing view, comparison interaction and published offer selection;
- signup handoff, registration start, verification completion and Account creation;
- security/passkey enrollment completion;
- checkout review, referral-code acceptance, Checkout redirect, cancellation return, pending projection, payment failure and projected subscription success;
- first authenticated application entry;
- first Your Turn queue view; and
- first eligible Your Turn completion by task category and result class.

No event contains name, email, Account/User UUID, customer text, prompt, answer, evidence, decision, document, payment method, Stripe object ID, authentication value or full route parameters.

Collected events are available through the execute-only aggregate boundary described in [Analytics reporting operations](analytics-reporting-operations.md). Each result row is bucketed by time, event, surface and at most one reviewed dimension; it is omitted unless at least five distinct consent subjects contributed. These independent aggregates support volume and directional funnel analysis but do not prove same-subject progression across public and private hosts.

### 5.3 Attribution

- Campaign parameters are parsed through a bounded allowlist and normalized before storage; arbitrary query parameters and referrers are discarded.
- Public acquisition uses a short-lived random first-party session.
- A signed opaque conversion receipt crosses the public-to-private signup handoff only after analytics consent. It contains the anonymous public subject, exact handoff event and expiry—no identity, email, Account ID, offer internals or campaign text.
- The receipt cookie is HTTP-only, Secure, SameSite=Lax, restricted to `/api/v1`, scoped only to the configured Infinite Ocean parent domain and expires within 24 hours. Reviewed private milestones are mirrored under the anonymous public subject without persisting a private subject or identity. It cannot become a durable cross-site User profile.
- Affiliate referral intent uses a distinct token and lifecycle. Analytics consent cannot create, alter or erase a commercial referral attribution.

### 5.4 GDPR operations

Before release, analytics must join:

- the record of processing activities and privacy/cookie notices;
- processor agreements and international-transfer review;
- consent access and withdrawal history;
- User and Account access/export, erasure, restriction and objection workflows;
- retention, aggregation and backup-expiry schedules;
- incident response and breach assessment; and
- a documented data-protection impact assessment decision.

## 6. Affiliate program

### 6.1 Enrollment and identity

- A normal Infinite Ocean identity may apply to or enable an Affiliate enrollment. Affiliate status does not create a second login system.
- The Affiliate is a distinct commercial aggregate bound to the responsible User and, when required for billing-credit settlement, one owned Spyglass Account.
- Enrollment records accepted affiliate-terms version, program/rule version, state, generated public code, creation time and suspension/closure history.
- Public codes are case-insensitive, human-enterable, unique and replaceable for future referrals. A replaced or suspended code cannot affect an attribution already locked to a subscription.
- Replacement requires recent passkey confirmation and the exact current enrollment version. During the approved retention period, a retired code is kept in immutable no-reuse history, immediately stops new lookup and can never be issued again; existing locked attributions and ledger entries remain bound to their original opaque attribution. At retention expiry, its readable value and Affiliate/User link are erased while an irreversible identity-free fingerprint continues the permanent non-reuse control.
- Affiliate terms require truthful claims and clear, conspicuous disclosure that the Affiliate earns recurring value from qualifying purchases.

### 6.2 Referral capture

- A customer may enter an Affiliate code during the authenticated pre-checkout review.
- The Affiliate dashboard's link displays a proposed code and preserves that proposal through same-origin sign-in, signup/email verification or password recovery/reset, but it does not silently persist attribution. The visitor must actively apply the referral before the server establishes a short-lived transactional intent; checkout then displays the attributed Affiliate and allows the customer to remove or replace it before purchase.
- The server validates the code, Affiliate state, offer eligibility, terms version and anti-self-referral policy before creating Checkout.
- One subscription can have at most one locked Affiliate attribution.
- Attribution becomes immutable when the Checkout subscription is durably projected. Later code changes apply only to a new subscription under an explicitly reviewed policy.
- Stripe Checkout and Subscription metadata carry only an opaque local attribution ID for webhook reconciliation. The public code, Affiliate identity and referred customer identity do not need to enter Stripe metadata.
- Referred customer identity and business details are never disclosed in the Affiliate dashboard.

### 6.3 Commission rules and ledger

Commission policy is versioned and frozen with the locked attribution. It defines:

- eligible Catalog offer and version;
- currency and exact commission amount or formula;
- whether the initial invoice qualifies;
- qualifying renewal cadence and any maximum cycle count;
- treatment of trials, coupons, proration, upgrades, downgrades and taxes;
- refund, credit-note, dispute and chargeback reversal policy;
- pending/hold transition before settlement; and
- code/Enrollment suspension behavior.

The launch rule has no maximum cycle count: a qualifying attributed subscription continues producing earnings for its full eligible lifetime.

The approved launch amount is **$10 USD for each qualifying successfully paid $50 USD subscription invoice**, including the initial invoice, independent of tax. A customer discount applies the same percentage reduction to the commission, using the eligible subscription charge after discounts and before tax; a 10% discount therefore produces a $9 earning. A successfully paid positive eligible proration charge participates at the same 20% rate and performs the rolling maturity transition; zero or negative proration creates no earning. A free or zero-value trial creates no earning and does not advance maturity; the first positive paid invoice after it is the initial qualifying invoice, while any future paid-trial program requires a separately versioned rule. Attribution survives eligible plan changes within the same Stripe subscription; free or ineligible periods earn nothing and do not advance maturity, and a separate subscription does not silently inherit the attribution. Fractional cents are rounded independently per invoice to the nearest cent with an exact half-cent rounded upward; no fractional remainder carries forward. Each qualifying invoice creates one pending earning. The next qualifying successfully paid invoice makes only the immediately preceding earning available and creates the next pending earning; this is a successor-invoice transition, not a fixed number of elapsed days. Cancellation does not claw back an earning already available. If the subscription actually terminates before another qualifying invoice, the final pending earning expires through an immutable void event; a scheduled cancellation alone does not void it, and a later successful invoice after the schedule is withdrawn matures it normally. Adverse-event and settlement-mode decisions remain open.

Each verified `invoice.paid` projection evaluates the frozen rule and appends at most one immutable commission entry. A unique attribution/subscription/invoice/rule binding makes webhook replay and multiple payment attempts exactly once; payment evidence remains retained without making one PaymentIntent the earning identity. Partial payment attempts do not earn or advance maturity. Failed, void, uncollectible, zero-value or ineligible invoices earn nothing. Any successful non-zero Refund, finalized non-zero credit note, or final lost dispute/chargeback voids the entire earning for its associated invoice: pending earnings cannot mature and available earnings receive one immutable full reversal. Pending, failed or canceled refunds; draft or voided credit notes; and open, warning, pending or won disputes do not reverse. Duplicate lifecycle events remain semantic no-ops, and an adverse event received before its earning is retained and applied atomically when that earning appears. Cancellation without an adverse object is non-adverse. Entitlement projection and customer access never depend on Affiliate settlement success. See [Affiliate operations](affiliate-operations.md).

### 6.4 Affiliate dashboard

The private Vue surface provides:

- current public code and copyable disclosure-safe referral link;
- accepted terms and current program summary;
- aggregate referred subscriptions without customer identity;
- pending, earned, reversed and settled commission totals;
- monthly statement history; and
- suspension, dispute and support paths.

Analytics consent does not control access to this commercial ledger. Affiliate dashboard analytics, if enabled, follow the ordinary optional product-analytics category and never copy ledger detail to the acquisition sink.

### 6.5 Support and appeals

The customer dashboard offers only a structured enrollment appeal or a review bound to one owned commission entry. It intentionally accepts no free text or attachment and warns against sending customer names, payment details, or referred-business information. Submitted requests can be canceled until staff begins review. Staff inspection, review start, and approved/denied resolution use a separate execute-only operator boundary with exact-version transitions and immutable events. A support approval is evidence of a reviewed decision, not authority to mutate the enrollment or commission ledger automatically.

### 6.6 Abuse and compliance

- Deny referral between the same User, the same controlled Account or another deterministically known self-referral relationship.
- Rate-limit code validation without treating code secrecy as an authorization boundary.
- Detect repeated checkout creation, code cycling and suspicious concentration through content-free risk signals and manual review.
- Affiliate content must disclose the financial relationship clearly and conspicuously near the endorsement or link.
- Program suspension stops new attribution while preserving earned/reversed ledger history and referred-customer subscriptions.
- Subscriptions already locked before suspension continue producing qualifying earnings and rolling maturity; suspension does not alter customer access or billing.
- Permanent closure stops all future earnings and expires the final pending earning through immutable void evidence. Already available credit remains usable for billing or Support-assisted check settlement; no customer subscription or previously available earning is disturbed.
- Affiliate identity, terms acceptance, tax/payout material and earnings are personal/commercial data with explicit retention, access, erasure limitations and legal-hold rules.

Local abuse-review checkpoint (2026-08-26): the execute-only Affiliate operator boundary now exposes an audited `inspect-risk` action with only 24-hour reservation aggregates, 30-day code-replacement aggregates and documented deterministic review flags. It returns no referred-customer, public-code, Checkout or provider identifiers and has no authority to suspend automatically. Checkout also enforces a distributed 20-per-15-minute code-validation budget keyed by a content-free digest of network actor plus Account, before lookup or Stripe access. Neither raw network identity nor submitted code enters the limiter, and the existing restore-gated maintenance worker removes stale digests after 24 hours with content-free retention metrics.

## 7. Settlement policy

The release owner selected an account-credit-first hybrid on 2026-08-26. Oldest available earnings automatically reduce the selected Affiliate-owned Account's next Infinite Ocean invoice up to the amount due. Stripe calculates tax first; credit then reduces the resulting balance. Pending, voided, reversed and Support-reserved value is excluded, and unused available credit carries forward without arbitrary product expiration. Legally required dormant-property treatment remains jurisdiction-specific. Credit cannot be sold, gifted, assigned or transferred to another User or Affiliate. With recent passkey confirmation, the same Affiliate may change its billing destination to another Infinite Ocean Account it owns; reserved credit cannot move. Once the available balance reaches **$100.00 USD**, it becomes eligible for payment by check; pending earnings do not count. Checks are never automatic or directly self-service. The Affiliate contacts Support, completes recent passkey confirmation and verifies identity, mailing address and required tax information before Support creates a reviewable request against atomically reserved available credit. Support chooses the amount case by case outside product automation; the application prevents double settlement but does not select an amount, issue a check, or model delivery/reissue state. Lost, returned, stopped, reissued and uncashed checks remain an external Support procedure. No payment promise may be exposed until the balance-integrity rules and procedure are approved.

### Account billing credit

Oldest available commission is applied automatically as bounded credit toward the next invoice for one Affiliate-owned Spyglass Account, after Stripe calculates tax and up to the resulting amount due. Pending, voided, reversed and Support-reserved value is excluded; unused credit carries forward without arbitrary product expiration. Legally required dormant-property treatment remains jurisdiction-specific. Credit cannot move to another User or Affiliate, though the same Affiliate may select another owned Infinite Ocean Account with recent passkey confirmation when no reservation blocks it. This requires exact Stripe/customer-balance reconciliation and treatment when the Affiliate has no paid subscription.

### Threshold check payment

Only an available balance of at least **$100.00 USD** becomes eligible for payment by check. Pending earnings do not count. The authenticated Affiliate must contact Support and complete recent passkey, mailing-address and tax-information verification; there is no direct payout button. Support determines the amount case by case, while the application prevents double settlement. Product code does not calculate the check amount, issue it or model delivery, stop, reissue or uncashed-check states; those remain external Support procedure and bookkeeping. Statements and applicable reporting remain required. It is not an unrestricted withdrawable cash balance.

The immutable earning ledger remains independent of settlement. The UI cannot promise a check or label pending earnings as available account credit until the remaining settlement rules and implementation are approved.

Launch pricing is $50.00 USD per month per team plus an optional $250.00 USD one-time onboarding/commissioning package, before applicable Stripe-calculated tax. There is no Free plan. The optional package is never Affiliate-commission eligible. Its line amount is excluded even when subscription and commissioning share an invoice, and adverse evidence confined to that line cannot reverse a subscription earning. The checked-in `free-v1`, `$49` Team and `$149` Operating candidate offers are obsolete and must not be presented as the approved launch model. Exact immutable Catalog identities, private Stripe Price mappings and line-item-aware Affiliate projection remain to be encoded before launch.

The authorized owner may select commissioning during initial subscription Checkout or buy it later from Billing. The standard package is available once per team; verified local purchase state removes the self-service offer and rejects duplicates, while additional engagements go through Support. A later standalone commissioning Checkout neither changes the subscription nor creates Affiliate attribution or earnings.

Registration creates a verified identity and inactive team shell before Checkout, not a Free plan. Until signed-webhook projection confirms the paid subscription, the owner is restricted to security, privacy, billing/checkout and sign-out and receives no product/package entitlement. Checkout cancellation, abandonment, pending state or failure does not activate the team. Analytics consent remains independent of both this necessary commercial flow and Affiliate attribution.

Launch has no free trial. Normal product access begins only after the first $50 subscription invoice is successfully paid and projected. Any zero-value/trial state in the implementation is defensive compatibility behavior, not an available acquisition offer or analytics funnel stage.

An unresolved renewal failure starts a fixed 30-day Account-retention clock. The first seven days permit read-only remediation while Stripe retries; after that, access is limited to billing, security, privacy, export and sign-out. A successfully paid invoice projected before day 30 cancels pending deletion. Otherwise the Account and live customer data pass through the guarded export/erasure workflow at day 30. Required financial, Affiliate and legal-hold evidence is detached into its separately approved restricted retention. After Account erasure, live User identity data is also erased only if an atomic check finds no Membership in another Account and no active Affiliate relationship. Otherwise the identity and unrelated relationships remain.

Spyglass sends email and in-app lifecycle notices immediately, at day 7, day 23 and day 29. Each states the affected team, exact deletion time, recovery/export routes and conditional orphaned-identity result. Stripe dunning is supplemental. Notices use necessary service communications rather than optional analytics/marketing consent, are idempotent per milestone, and stop when a successful payment is projected.

Launch Affiliate enrollment is limited to U.S. persons and entities with a valid U.S. mailing address and approved U.S. payer tax documentation. Referred customers remain geographically unrestricted by this Affiliate-enrollment boundary. Tax-document collection, validation, withholding and reporting remain external Support/accounting procedure; Spyglass does not ingest tax forms or implement a tax-verification workflow. International Affiliate enrollment requires a later terms version and reviewed foreign-payee, withholding, treaty, local advertising-disclosure and check-delivery procedure.

Affiliate terms acceptance, earnings, reversals, billing-credit applications, check settlements and related audit evidence are retained for seven years after the later of Affiliate closure or the final settlement, reversal or tax-relevant transaction. Identifiable commercial records are then deleted or irreversibly minimized unless a documented legal hold applies. At that deadline, readable retired codes and their Affiliate/User linkage are erased. A protected irreversible, identity-free fingerprint is retained permanently only to prevent reissue; it cannot be used to recover the code or identify the former Affiliate.

A verified Affiliate erasure request closes the enrollment, removes the active code and share link, stops new attribution and future earnings, and erases data not needed for the approved retention purpose. It does not affect referred customers or extinguish already available credit. Required financial, accounting, settlement and audit evidence is retained under restricted access for the remainder of the seven-year period and excluded from marketing and ordinary product use. The response identifies the retained categories, purpose and deadline. At deadline the data is deleted or irreversibly minimized; a documented legal hold extends only its scoped records.

## 8. Construction and acceptance order

1. Approve the processing/event registry, consent policy, retention schedule and analytics provider boundary.
2. Implement consent receipts and enforcement plus first-party analytics ingestion locally.
3. Implement the Affiliate enrollment, code, attribution, rule and commission-ledger backend with generated HTTP contracts.
4. Add landing, checkout and onboarding event instrumentation with consent-denied parity.
5. Add referral entry/review and immutable Checkout/Subscription attribution.
6. Add webhook-driven recurring commission earning, reversal and reconciliation.
7. Build the consent center, Affiliate enrollment/dashboard and commercial statements in Vue.
8. Complete GDPR rights, erasure/retention, vendor/transfer, disclosure, abuse and settlement acceptance.

Release requires automated proof that:

- optional analytics emits nothing before consent and stops after withdrawal;
- refusing analytics does not alter signup, checkout, onboarding, referral or application outcomes;
- every accepted event conforms to the registry and contains no prohibited data;
- affiliate attribution survives browser loss and analytics withdrawal;
- Checkout and invoice webhook replay cannot duplicate commission;
- failed/refunded/disputed policy outcomes cannot leave unearned settled value;
- Affiliates cannot see referred-customer identity or content;
- Affiliate disclosures and terms are presented before enrollment; and
- analytics and Affiliate data participate in the documented GDPR lifecycle.

Local acceptance checkpoint (2026-08-25): the service, PostgreSQL lifecycle and exact-build browser suites now automate the consent-independent referral, self-referral denial, suspended-code, invoice replay, recurring-cycle ledger, Refund/final-lost-dispute reversal and referred-customer-concealment cases. The browser matrix exercises both active and suspended Affiliate states across all ten standard profiles, retains the deliberate code after a self-referral denial with analytics rejected, and emits no optional event. The combined exact-artifact result is 331 applicable browser passes with eleven intentional compact-menu skips; all Go packages pass. This closes the local synthetic/adversarial implementation evidence for those cases, but it does not approve the candidate economics, settlement mode, Affiliate launch flags, legal/vendor/transfer review, hosted Stripe journey, real-device use or assistive-technology certification.

## 9. Authoritative external guidance

- [GDPR Article 3 and territorial scope](https://eur-lex.europa.eu/eli/reg/2016/679/art_3/oj)
- [EDPB consent guidance](https://www.edpb.europa.eu/our-work-tools/our-documents/guidelines/guidelines-052020-consent-under-regulation-2016679_en)
- [EDPB cookie-banner taskforce report](https://www.edpb.europa.eu/documents/task-force-report/report-of-the-work-undertaken-by-the-cookie-banner-taskforce_en)
- [Stripe metadata use cases, including affiliate attribution](https://docs.stripe.com/metadata/use-cases)
- [Stripe subscription Checkout and recurring `invoice.paid` events](https://docs.stripe.com/payments/checkout/build-subscriptions)
- [FTC affiliate and endorsement disclosure guidance](https://www.ftc.gov/business-guidance/resources/ftcs-endorsement-guides-what-people-are-asking)
- [European Commission Influencer Legal Hub](https://commission.europa.eu/topics/consumers/consumer-rights-and-complaints/influencer-legal-hub_en)
- [IRS Form W-8BEN foreign-payee guidance](https://www.irs.gov/forms-pubs/about-form-w-8-ben)
- [IRS business-record retention guidance](https://www.irs.gov/businesses/small-businesses-self-employed/how-long-should-i-keep-records)
- [GDPR storage-limitation principle](https://eur-lex.europa.eu/legal-content/EN/TXT/PDF/?uri=CONSIL%3APE_31_2018_INIT)

## Customer rights-status presentation

The authenticated Privacy workspace reads only the customer-safe rights-request projection. Customers can refresh that projection without a full application reload, see the last recorded update and receive distinct explanations for `submitted`, `in_review`, `completed`, `partially_completed`, `declined` and `canceled`. If refresh fails, the last known history remains visible and is not presented as current.

Operator evidence UUIDs and digests, staff identity, operational reasons and fulfillment artifacts never enter the browser contract. A terminal status communicates the recorded review outcome; it is not itself evidence that an export, correction, restriction, objection or erasure was performed. That evidence remains governed by the fulfillment procedure and execute-only operator boundary.
