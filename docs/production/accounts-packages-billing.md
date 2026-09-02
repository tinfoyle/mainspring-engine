# Website, Accounts, Packages, and Billing Architecture

- Status: Accepted by [ADR-0001](decisions/0001-product-identity-and-web-surfaces.md) and [ADR-0002](decisions/0002-system-identity-accounts-packages-and-billing.md)
- Product: Infinite Ocean: Spyglass
- Public domain: `infiniteocean.net`
- Parent: [Production plan](README.md)

## 1. Product surfaces

Infinite Ocean owns the public relationship with visitors and customers. Spyglass is the product a customer enters after creating an identity and account.

| Surface | Recommended origin | Audience | Responsibility |
|---|---|---|---|
| Infinite Ocean website | `https://infiniteocean.net` and `https://www.infiniteocean.net` | Anonymous visitors and signed-in users | Company/product information, package descriptions, pricing, signup, login entry, legal pages, support and billing entry |
| Spyglass application | `https://app.infiniteocean.net` | Authenticated account members | Operational product packages such as Work, Agents, Finance, and Marketing |
| Public/account API | `https://api.infiniteocean.net` | Website and Spyglass clients | Identity, accounts, memberships, catalog, checkout, portal, application APIs |
| Stripe webhook | `https://api.infiniteocean.net/webhooks/stripe` | Stripe only | Signed event ingestion; no browser session or CSRF semantics |
| Documentation/status | `https://docs.infiniteocean.net`, `https://status.infiniteocean.net` | Customers and operators | Product documentation and independent service status |

These origins may be served through one edge and one frontend deployment initially. They remain distinct security and caching surfaces:

- Public pages can be CDN-cached and contain no account data.
- Authentication, account, checkout, and application pages are dynamic and private.
- Cookies are host-scoped wherever possible; do not give the marketing site broad application-session cookies.
- Redirect destinations are allowlisted exact Infinite Ocean origins.

## 2. Terminology

The word â€œaccountâ€ is often overloaded. Production code uses these explicit concepts:

### User

A system-wide human login identity. A User has one authentication identity, profile, verified contact methods, sessions, and security settings across Infinite Ocean.

### Spyglass Account

The customer/business boundary for Spyglass. It owns memberships, business data, package entitlements, usage, billing relationship, and lifecycle. It replaces the prototype's use of â€œtenantâ€ in customer-facing language.

### Membership

A relationship between a User and a Spyglass Account with a role and state. A User can belong to more than one account without creating another login.

### Billing Profile

Optional account-level billing metadata and a Stripe Customer reference. A free account can exist indefinitely without a Billing Profile or payment method.

### Subscription

The local projection of a Stripe subscription attached to one Spyglass Account. Stripe is authoritative for billed subscription/payment state; Spyglass is authoritative for application access policy derived from that state.

### Feature Package

A versioned commercial/product capability group such as Work, Agents, Finance, or Marketing. This is distinct from a Go code package.

### Entitlement

An effective account-level right to use a Feature Package, optionally subject to mode, time window, quota, or conditions.

## 3. Signup and entry flow

### Anonymous visitor

1. Visitor lands on `infiniteocean.net`.
2. Public content explains Infinite Ocean, Spyglass outcomes, individual packages, examples, security posture, pricing, and limitations.
3. Visitor can explore a non-sensitive guided product preview without creating customer-specific infrastructure or running real agents.
4. Visitor selects **Create account** or a package/plan call to action.

### Free account creation

1. User registers a system-wide identity and verifies the required contact method.
2. Spyglass creates a new Account and an owner Membership in one transaction.
3. The Catalog/Entitlements service attaches the current free-plan grants.
4. The account is assigned to a production cell and its logical data namespace is initialized.
5. The user enters `app.infiniteocean.net` with only entitled navigation and actions visible.
6. No Stripe Customer or payment method is required.

### Paid conversion

1. An account owner selects an offered plan or package combination.
2. The server validates the offer from the local catalog; the browser cannot submit arbitrary Stripe price IDs.
3. Billing creates or reuses the account's Stripe Customer and a Checkout Session with an idempotency key and internal account reference in metadata.
4. The browser redirects to Stripe-hosted Checkout.
5. The return page displays payment/checkout progress but does not grant access based only on the redirect.
6. Signed Stripe events are ingested and asynchronously projected into local Billing and Entitlement records.
7. When access policy considers the subscription effective, a new entitlement snapshot is published and Spyglass refreshes the account session/navigation.

### Billing management

An authenticated account billing administrator requests a short-lived Stripe Customer Portal session and is redirected to it. Subscription and payment changes return through webhooks; the browser redirect is not authoritative.

### Affiliate attribution and recurring commission

A normal Infinite Ocean identity may enroll in the Affiliate program and receive one active public code. With recent passkey confirmation and the exact enrollment version, the Affiliate can replace that code for future referrals; the old code is permanently retired and cannot be reused, while existing locked attributions and their ledger remain unchanged. A referred customer may enter the current code during the authenticated checkout review. The server validates it, displays the attribution before redirect, and locks at most one immutable Affiliate attribution to the projected subscription. Analytics consent does not create, change or erase this commercial state.

Stripe metadata carries only an opaque local attribution ID for reconciliation. Each qualifying projected `invoice.paid` renewal appends at most one entry to the local immutable commission ledger under the versioned rule frozen at attribution. Failed or ineligible invoices earn nothing; refunds, disputes and chargebacks append reversals according to the approved rule. Affiliate views expose the count of locked referred subscriptions, aggregate earnings and UTC monthly statements without referred-customer identity.

The proposed launch example is $10 USD for each qualifying successfully paid $50 USD monthly renewal. Initial-invoice treatment, discounts/proration, reversal window and whether settlement becomes Affiliate Account billing credit or withdrawable cash require explicit commercial approval before the UI describes earned value as available. The complete boundary is in [Privacy, analytics and affiliate architecture](privacy-analytics-affiliates.md).

## 4. Identity and account model

```text
User
  id
  primary_email
  email_verified_at
  display_name
  state
  security_version

Account
  id
  slug
  display_name
  type                 free | paid | internal | partner
  state                active | restricted | suspended | closing | closed
  cell_id
  entitlement_version
  created_by_user_id

Membership
  account_id
  user_id
  role                 owner | administrator | billing_admin | member | viewer
  state                invited | active | suspended | removed
  version

Invitation
  account_id
  email
  intended_role
  token_hash
  expires_at

BillingProfile
  account_id
  stripe_customer_id
  billing_email
  tax/location status references
  version
```

Rules:

- Email is a login/contact attribute, never the durable identity key.
- One user may own or join multiple accounts.
- One account has exactly one active owner. Ownership transfer requires recent strong identity confirmation, explicit current versions for both Memberships, a bounded reason, and one atomic role swap that promotes the successor while demoting the previous owner to Administrator.
- Exercising any Owner Membership requires one completed system-wide security path: either a passkey plus at least one unused recovery code, a verified SMS-code method, or a verified email-code method. Passkeys are recommended, SMS is the simpler fallback, and email is presented as the least-safe option. The shared authorizer fails closed for both reads and mutations; the Account list exposes a safe enrollment-required flag while global identity/security endpoints remain reachable. Non-owner Memberships are not subject to this owner-only gate.
- Sensitive Account mutations accept a passkey, SMS code, or email code confirmed on the acting User's current session within the bounded strong-auth window. Password confirmation and Google sign-in alone do not satisfy that boundary. Recovery-code rotation and lost-passkey replacement remain passkey/recovery-specific operations.
- Ownership transfer may promote an owner whose factors are not ready, preserving one-owner atomicity. That successor cannot exercise owner authority until enrollment is complete; factor state is never inherited from the prior owner or persisted as Account-local state.
- The same ownership transaction appends its immutable event and inserts separate encrypted notices for the previous and new owner. Delivery is asynchronous and independently retryable, so SMTP failure cannot roll authority back after commit or prevent the other recipient from being notified.
- Owners may change any non-owner Membership among Administrator, Billing Admin, Member, and Viewer. Directly assigning the Owner role is forbidden; ownership moves only through the transfer use case.
- Owners may remove any non-owner Membership. Administrators may remove Billing Admin, Member, and Viewer Memberships, but cannot remove an Owner or another Administrator.
- Owners may suspend/reactivate any non-owner Membership. Administrators may suspend/reactivate Billing Admin, Member, and Viewer Memberships, but cannot manage an Owner or peer Administrator. Suspension preserves the assigned role and manager visibility while immediately failing active-Membership authorization.
- Any active non-owner may leave an Account after passkey confirmation. Owners must transfer ownership first. Leave and removal transition to the terminal `removed` state; restoration requires a new invitation and Membership.
- Role, lifecycle, removal, and transfer mutations use optimistic Membership versions, recheck the actor and target inside the persistence transaction, and append immutable Account Membership events with role/state before and after values.
- Suspended and removed Memberships disappear from Account selection and fail current authorization checks; no session is duplicated or globally revoked because the same User may still belong to other Accounts.
- Billing roles do not automatically grant access to business content.
- Account state and package entitlement are evaluated separately.
- Closing an account is a durable workflow covering subscription, exports, connectors, runs, data retention, and deletion.

## 5. Catalog model

The Catalog module defines what Infinite Ocean sells. Stripe IDs are mappings, not domain identities.

```text
FeaturePackageDefinition
  code                 work | agents | finance | marketing | ...
  version
  name
  description
  lifecycle            draft | active | retired
  dependencies[]
  capabilities[]
  default_limits

PlanDefinition
  code                 free | team | operating | ...
  version
  included_package_grants[]
  availability

CatalogOffer
  code
  plan_version
  currency
  billing_interval
  stripe_product_id
  stripe_price_id
  effective_from/to
  availability rules
```

Package examples are intentionally configurable:

| Package | Example scope | Possible dependencies |
|---|---|---|
| Work | Work queue, tickets, assignment, comments, review | Account foundation |
| Agents | Boardrooms, agent configuration, runs, governed tools | Work and Knowledge foundations |
| Finance | Ledgers, accounts, journal entries, finance reports | Account foundation; optional Work links |
| Marketing | Campaign planning, content calendar, brand knowledge, research workflows | Knowledge; optionally Agents and Work |
| Knowledge | Documents, facts, evidence, citations, revisions | Account foundation |
| Integrations | Email, Drive, and future connectors | Package-specific capability grants |

Dependencies are explicit and acyclic. A commercial plan may bundle packages, but entitlement evaluation occurs package by package.

Changing a price creates a new Stripe Price mapping; it does not change the stable package or plan identity. Existing subscriptions remain explainable through the catalog version recorded locally.

## 6. Entitlement model

Entitlements make package availability configurable without deploying account-specific code or containers.

```text
EntitlementGrant
  id
  account_id
  package_code
  package_version
  mode                 enabled | read_only | suspended
  source               free_plan | subscription | trial | promotion | support_override | grandfathered
  source_reference
  limits
  starts_at
  ends_at
  priority
  reason

EntitlementSnapshot
  account_id
  version
  evaluated_at
  catalog_version
  effective_packages[]
  source_hash
```

Evaluation order:

1. Validate active User session and Membership.
2. Validate Account state.
3. Load the account's current immutable EntitlementSnapshot.
4. Confirm the requested Feature Package is present and dependencies are satisfied.
5. Confirm package mode permits the requested command.
6. Enforce package limits and account usage reservation.
7. Apply role, object, capability, and approval authorization.

Enforcement exists at every relevant boundary:

- Website/application navigation and upgrade messaging.
- HTTP and MCP route/use-case guard.
- Background dispatcher and schedule activation.
- Agent tool-definition visibility.
- Usage admission and connector operations.

UI hiding alone is never enforcement. A non-entitled call returns a stable `package_not_entitled` problem including a safe package code and upgrade path. A read-only downgrade returns `package_read_only` for mutations while allowing retention/export policy reads.

### Sources and precedence

- A safety or account suspension overrides all positive grants.
- Explicit time-bounded support overrides outrank plan grants and require reason, actor, and audit.
- Subscription and promotion grants combine according to catalog policy.
- Limits combine explicitly by rule (`replace`, `add`, `maximum`, or `minimum`); there is no implicit map merge.
- Equal-priority grants use immutable grant ID as a deterministic tie-breaker; database row order can never change the winner.
- Missing or suspended dependencies suspend the dependent package; a read-only dependency propagates read-only mode so a dependent mutation cannot bypass the downgrade.
- Capacity is reserved through one Account/package/limit counter using a UUID operation key, entitlement-version fencing, idempotent release, and optional crash-recovery expiry.
- Each evaluation produces an immutable snapshot so requests and runs can record which entitlement version authorized them.

### Downgrade behavior

The normative boundary-by-boundary rules and current executable inventory are in [Package surface and lifecycle](package-surface-lifecycle.md).

Package loss is not immediate data deletion.

1. At the effective downgrade time, new mutations and new background work stop.
2. Active runs follow a documented cancel-or-complete policy based on package and action risk.
3. Data enters read-only/exportable retention where appropriate.
4. Scheduled jobs and connector writes are disabled.
5. A later retention workflow archives or deletes package data only under published policy.
6. Re-upgrade during retention restores access without an ad hoc data recovery process.

## 7. Billing domain

```text
Subscription
  id
  account_id
  provider             stripe
  provider_customer_id
  provider_subscription_id
  status
  current_period
  cancel_at
  collection_state
  catalog_offer_code/version
  provider_object_version
  last_synced_at

BillingEventInbox
  provider_event_id
  event_type
  provider_created_at
  object_id
  signature_verified_at
  payload_reference/hash
  processing_state
  attempt_count

BillingProjection
  account_id
  subscription state
  paid-through/grace state
  package grant inputs
  version
```

Stripe does not become the account database. Spyglass never uses a Stripe billing email as login identity, and a Stripe Customer does not imply an active Spyglass Account.

### Webhook ingestion

1. Read the unmodified raw request body.
2. Verify the `Stripe-Signature` with the configured endpoint secret and timestamp tolerance.
3. Reject unsupported live/test mode or account context.
4. Insert the event ID, metadata, and protected payload reference into an inbox with a unique constraint.
5. Return `2xx` promptly after durable acceptance.
6. Process asynchronously.
7. Handle duplicates by event ID and, when required, provider object ID plus event type.
8. Do not assume delivery order. Retrieve the current Stripe object when an event cannot be safely projected from local/provider version information.
9. Update the local subscription projection idempotently.
10. Recalculate entitlement grants and publish a new EntitlementSnapshot transactionally.

Webhook secrets rotate independently by environment. Test/sandbox and live events cannot share projections.

### Billing versus access state

Subscription state is input to access policy, not a direct boolean. The accepted initial policy is deterministic:

- `active` and `trialing` grant the mapped plan modes and limits.
- On the first unresolved renewal failure, `past_due` enters immediate read-only remediation for seven calendar days: existing-data reads, export, billing, security and privacy continue, while new mutations, schedules and runs stop.
- Stripe `pause_collection` on an otherwise active/trialing subscription uses the same read-only remediation policy. A subscription whose actual status is `paused` grants no paid packages.
- `incomplete`, `incomplete_expired`, `unpaid`, `paused` and `canceled` grant no paid packages; independent free, promotion or support grants still evaluate normally.
- Voluntary cancellation scheduled at period end retains full paid access through that period and may be withdrawn before termination. When the paid period ends, access contracts to billing, security, privacy, export and sign-out and a 30-day deletion clock begins. Resubscription projected before deletion restores access and cancels erasure.
- A successful payment during the 30-day nonpayment-retention window cancels the scheduled closure/erasure and restores the paid entitlement after signed-webhook projection. At seven days after the first unresolved failure, or when Stripe marks the subscription `unpaid`/`canceled` sooner, access contracts to billing, security, privacy, export and sign-out. At 30 calendar days after that first failure, the team Account and live customer data are due for guarded erasure. Required financial, Affiliate and legal-hold evidence follows its separate restricted-retention policy.
- Upgrade, downgrade and proration are provider billing operations. Projection resolves the current Price to one immutable published Offer and atomically replaces that subscription's grants; Spyglass does not calculate money or prorations.
- Invoice, refund and dispute webhooks are invalidation signals, never direct entitlement commands. Current subscription state remains authoritative; a future dispute-specific safety suspension requires its own reviewed grant source and tests.
- Tax calculation, invoice presentation, credits and manual collection never independently grant access.

Access checks read the local EntitlementSnapshot, never call Stripe synchronously. A reconciler periodically compares Stripe Customer/subscription state with local projections and produces operator-visible mismatches.

## 8. Stripe product mapping decision

Spyglass v1 uses stable Stripe Customer, Product, Price, Checkout, Subscription, Invoice, and Customer Portal APIs behind a Billing provider adapter. The Stripe API version is pinned and upgraded deliberately.

Stripe Product tax classification is part of each environment's provider mapping, even though the provider Product identifier and tax code do not become Catalog domain identities. The team subscription and AI Token top-up use Stripe's business-use SaaS code `txcd_10103001`; the optional human commissioning service uses the technical-support-services code `txcd_20060017`. A release preflight must retrieve every mapped Price and require its Product to carry the expected code before checkout is opened. Stripe Managed Payments accepts the SaaS lines but does not accept the correctly classified commissioning service. Combined subscription-and-commissioning checkout therefore remains closed until the environment has a reviewed non-Managed-Payments Stripe Tax configuration, including a valid head-office address and automatic tax, rather than misclassifying the service as a digital product.

Stripe Billing Entitlements may mirror paid package features, but it is not the sole Spyglass authorization source because free plans, trials, promotions, internal accounts, grandfathering, emergency suspension, and support overrides also affect access. The local Entitlements module always produces the effective snapshot used by the application.

Launch correction (2026-08-26): the release Catalog has no Free plan. The checked-in `free-v1` offer, permanent-free acquisition copy and free entitlement remain current implementation/history only and must not be published or presented as launch behavior. The local Entitlements architecture may continue to represent historical, trial, promotional, internal and support-granted access without turning any of those sources into a public Free plan.

Approved registration/payment boundary (2026-08-26): registration first creates and verifies the User identity and an inactive team shell. Before payment projection, that owner may access only identity security, privacy, billing/checkout and sign-out; the shell grants no Spyglass feature or package entitlement. An authenticated authorized owner selects the subscription and optional commissioning package and enters Stripe-hosted Checkout. A browser redirect never activates the team. Only the locally verified signed Stripe webhook projection can establish the paid subscription and entitlement snapshot. Cancelled, abandoned, pending and failed Checkout attempts leave the shell inactive and safely retryable through the same idempotent purchase boundary.

Approved commissioning purchase boundary (2026-08-26): the authorized team owner may add the optional $250 onboarding/commissioning package to the initial subscription Checkout or purchase it later from Billing. The standard package is self-service-purchasable only once per team. A verified local purchase projection suppresses every later self-service offer and blocks duplicate purchase attempts independently of browser state or repeated Stripe events. An additional engagement is arranged through Support rather than another product purchase. Initial combined Checkout uses the recurring subscription Price plus the eligible one-time commissioning Price; a later purchase uses a separate one-time Checkout and cannot alter subscription or Affiliate state.

Approved trial boundary (2026-08-26): launch has no free trial. The ordinary customer path requires the first $50 subscription invoice to reach fully paid state before signed-webhook projection activates product entitlements. Defensive trial and zero-value states may remain representable for replay safety, historical compatibility and future versioning, but no launch Catalog offer, Stripe configuration or customer copy may create or promise free trial access.

Approved launch entitlement (2026-08-26): the single $50 monthly team subscription enables every completed launch package—Knowledge, Work, Agents, Finance, Marketing and Integrations—and every reviewed feature within them. The checked-in Free, Team and Operating tier split is obsolete launch configuration. Capacity, concurrency and safety limits remain enforceable operational controls but may not hide completed features behind another paid tier or add-on. The $250 commissioning purchase is a service engagement and grants no additional software package, mode, limit or authority.

Approved team-member capacity (2026-08-26): the flat subscription includes up to 25 active Members with no per-seat charge. Each still-pending invitation reserves one member slot until accepted, cancelled or expired so parallel invitations cannot bypass admission. The 26th combined active-member/pending-invitation position fails atomically with a visible capacity result and leaves the invitation unchanged. Support may issue a reviewed, versioned Account capacity override for a larger team without changing price, packages, Affiliate earnings or feature tier; no client or Stripe metadata may self-assert that override.

Approved document capacity (2026-08-26): the paid team includes 1,000 logical Knowledge Documents. Immutable revisions of one Document share that Document's single slot. A Document continues consuming capacity throughout processing and retention/deletion preparation; capacity releases only after its guarded physical deletion completes so concurrent uploads cannot oversubscribe. The 1,001st admission fails atomically without creating source objects or partial metadata. Support may issue a reviewed, versioned Account override without changing price, packages or feature tier. The checked-in default of 25 is prototype configuration and must not be published for launch.

Approved Work capacity (2026-08-26): the existing default of 100 active Work items per team remains the launch limit. Each active child item counts independently. Completed and cancelled items remain available as governed history but release active capacity only after their terminal transition commits. Concurrent admissions must reject a 101st active item atomically without partial Work or capacity state. Support may issue a reviewed, versioned Account override without changing price, packages or feature tier; clients cannot request or self-assert the override.

Approved Agent concurrency (2026-08-26): the existing default of two concurrently admitted Agent Runs per team remains the launch limit. Queued work consumes no concurrency until admission commits. Each admitted nonterminal Run owns one reservation; terminal projection, explicit cancellation or bounded reservation expiry releases it exactly once. Parallel admission of a third Run fails or remains queued without starting provider work. Support may issue a reviewed, versioned Account override without changing price, packages or feature tier; clients and runner/provider metadata cannot self-assert it.

Approved AI usage unit (2026-08-26): Infinite Ocean AI Tokens are customer-facing, provider-neutral usage credits, not raw provider tokens, money, cryptocurrency or a promise of access to one named model. Each admitted model operation freezes an immutable customer rate-card version that converts its complexity class, internal model/provider, input, cached input, output and billable tool use into AI Tokens. The system reserves a bounded maximum before provider work, settles exact trusted usage afterward, releases unused reservation once, and cannot produce a negative balance or retroactively reprice completed use. New rate cards affect future admissions only. This abstraction permits reviewed Kimi, Z.ai or later model substitutions while preserving Infinite Ocean margin.

Approved configurable token commerce (2026-08-26): subscription grants, top-up bundles and promotion grants are independently versioned Catalog records with arbitrary positive integer AI Token quantities, USD prices, effective windows, purchase limits and disclosure versions. No permanent dollars-to-AI-Tokens exchange rate is promised. Publishing a new grant or bundle changes only later renewals/purchases; it cannot mutate a completed Stripe purchase or existing immutable balance entry. Promotions append bounded labeled grants rather than editing balances or rate history. The construction seed is 10,000 AI Tokens per successful $50 renewal plus an optional 10,000-token/$10 top-up, but both values are configurable and must be reaffirmed for release. Future reviewed publications may, for example, offer much larger quantities at lower prices when upstream economics permit.

Approved AI Token balance lifecycle (2026-08-26): every successful renewal appends a distinct included-token grant; any unused prior included grant expires atomically when that next successful renewal grant commits and does not roll over. Purchased top-up grants have no arbitrary expiry while the team Account remains active. Every promotional definition must publish an explicit expiration timestamp, and its grant expires at that time. Reservation consumes available grant cohorts by earliest expiration first, with non-expiring purchased grants last; ties use grant creation time and immutable grant ID. Settlement debits the exact reserved cohorts, and release returns unused Tokens to those same cohorts unless they expired while reserved. Included, purchased and promotional origins are never blended or rewritten.

Approved AI Token cancellation/deletion lifecycle (2026-08-26): when nonpayment or voluntary cancellation restricts product access, all remaining grants stay visible for billing/privacy/export purposes but cannot admit new AI work. Recovery or resubscription projected before guarded Account deletion restores use of the remaining purchased-token grants. Included and promotional grants receive no special extension and may expire under their ordinary rules while access is restricted. Guarded Account deletion extinguishes every remaining included, purchased and promotional grant without cash value or refund except where applicable law requires otherwise. Extinguished grants cannot be recovered by or transferred to a later Account.

Approved AI Token ownership/transfer policy (2026-08-26): every grant belongs to the team Account that received or purchased it, never to an individual member. Membership and Account-owner changes leave its immutable ledger in place. No self-service, API or Support operation may transfer grants between Accounts, including Accounts controlled by the same person. Billing corrections must append auditable reversal or replacement entries within the original Account. Mergers, splits and commercial replacements are handled case by case without an automated token-migration path.

Approved AI Token top-up refund policy (2026-08-26): an authorized team owner or billing administrator with recent passkey confirmation may ask Support to refund a top-up within 14 calendar days of purchase only when none of that exact grant has been consumed or remains reserved. Any consumption makes the grant ineligible for both full and partial discretionary refunds. Included and promotional grants are not refundable. Duplicate charges, fraud, technical billing errors and legally required remedies remain separate exceptions. Review waits for every grant-backed reservation to settle or release. A confirmed Stripe refund appends one idempotent full-grant reversal and makes the grant unavailable without rewriting its purchase or usage history.

Approved AI Token issuance policy (2026-08-26): only a signed Stripe projection of one successfully paid positive initial or recurring subscription invoice for a new service period appends that period's configured included grant. The full versioned quantity is granted independent of percentage discount and tax. Prorations, payment retries, commissioning lines and top-up purchases do not create another included grant. Each fully paid top-up appends exactly the quantity frozen by its purchased Catalog bundle. A subscription Refund or final lost dispute makes the unused remainder of its included grant unavailable without creating negative usage or rewriting consumption; ordinary access and nonpayment rules determine the Account consequence.

Approved AI Token authority and insufficient-balance policy (2026-08-26): the balance is a shared team resource. Active members may see customer-safe available, reserved and origin totals, while an otherwise authorized package action may consume Tokens only through ordinary role/object policy. Only an Account owner or billing administrator with recent passkey confirmation may manually buy a top-up or initiate its refund request. Launch has no automatic replenishment or surprise overage charge. An insufficient maximum reservation fails before queue admission or provider contact, preserves the customer's draft and returns a stable purchase-or-renewal recovery path; the ledger never incurs debt or a negative balance.

Approved AI Token rate-change policy (2026-08-26): a materially higher customer debit for comparable work at the same complexity class receives at least 30 calendar days' in-product and email notice before its prospective effective time. Security, legal, provider-withdrawal or severe-reliability emergencies may require an immediate mapping or rate replacement, but the owner must receive notice as soon as practicable and a clear lower-complexity or stop-work option. An admitted Run always retains its frozen rate. Existing token quantities do not grandfather an Account into an old rate card and are never represented as fixed USD purchasing power.

Approved AI Token promotion policy (2026-08-26): every campaign freezes its eligible audience, effective window, explicit token expiry, per-Account redemption limit, total issuance cap, price/quantity effect, disclosure version and stacking rule. Redemption is exact-once and appends a labeled grant or immutable discounted purchase rather than editing history. Promotions are not retroactive and do not stack unless their published definitions explicitly permit it. An Affiliate code remains attribution, not a token promotion. Neither top-up purchases nor promotional token grants earn Affiliate commission; only the approved eligible subscription basis does.

AI Token commerce and commissioning implementation checkpoint (2026-08-26): the Account API now exposes recent-passkey owner/billing-admin operations for a Catalog-selected one-time purchase and bounded promotion redemption. A local Checkout attempt freezes bundle or commissioning identity, version, Catalog version, USD amount and token quantity before any hosted redirect. Signed paid projection—not browser return state—fulfills that snapshot once. A final full-charge Refund reverses only the surviving unused purchased balance. The optional commissioning line may accompany initial subscription Checkout or use a later one-time session; its durable Account purchase record grants no entitlement and blocks a second self-service purchase. Promotion campaigns freeze quantity, effective window, expiration interval, per-Account and global caps, disclosure and no-stacking behavior; their operational cap rows are nonportable while the customer-safe grant and ledger history remain in the Account export.

Approved AI Token payment-adversity policy (2026-08-26): an open top-up dispute freezes that purchased grant's unused balance and blocks new reservations from it. A won dispute restores the surviving balance; a successful Refund, reversed payment or final lost dispute appends an idempotent reversal for the remaining grant. Tokens already settled remain immutable and never create a negative balance, while suspected fraud, repeated adversity and any Account restriction follow reviewed Support/security procedure rather than an automatic cross-Account penalty.

Approved model presentation correction (2026-08-26): customers remain insulated from provider/model names but may set each Agent's desired complexity on a five-stop control: `simple`, `efficient`, `balanced`, `thorough`, and `advanced`. `balanced` is the default for a new Agent. Each class maps through the effective reviewed rate card to an internal certified model/provider and a distinct AI Token consumption schedule. The control exposes the exact text label and current estimated token range, supports keyboard/assistive-technology value changes and never relies on slider position alone. A change affects future Runs only. Every admitted Run freezes its complexity, provider, model, adapter, policy and rate versions so substitution or later Agent customization cannot alter retries, reconciliation, evidence or customer debits.

Approved failed-renewal lifecycle (2026-08-26): the first unresolved renewal failure fixes one nonpayment clock. Days 0–7 are read-only remediation while Stripe retries. After day 7, or an earlier terminal `unpaid`/`canceled` projection, only billing, security, privacy, export and sign-out remain. Successful payment projected before day 30 atomically cancels pending deletion and restores paid access. Otherwise the system must prepare export, closure and guarded Account erasure so the Account and live customer data are deleted at day 30. Browser traffic never holds erasure authority; the existing tombstone, cross-store, backup-expiry and reviewed-execution safeguards remain mandatory. Separately retained financial/Affiliate evidence is detached and restricted rather than kept in the customer Account. After Account erasure, a separate atomic check deletes the live User identity only if it has no Membership in another Account and no active Affiliate relationship; otherwise the identity and unrelated authority remain intact.

Approved nonpayment notices (2026-08-26): durable email and in-application notices are due immediately after the first failure, at day 7 when read-only remediation ends, at day 23, and at day 29. Every notice identifies the exact team and absolute UTC deletion time, links directly to authenticated payment recovery and Account export, and explains whether the User identity will also be deleted if it becomes orphaned. Stripe dunning messages do not substitute for these Spyglass lifecycle notices. Delivery is idempotent per Account, clock and notice milestone; a projected recovery cancels all unsent deletion notices.

Approved voluntary-cancellation lifecycle (2026-08-26): scheduling cancellation preserves full product access through the already-paid period. Withdrawal before period end leaves the subscription uninterrupted. At actual term end, product access contracts to billing, security, privacy, export and sign-out and the same guarded 30-day Account-erasure clock begins. A projected resubscription before deletion cancels the clock and restores access. Email and in-app notices are due when cancellation is scheduled, at term end, at deletion-clock day 23 and at day 29; each carries the exact team, term/deletion times, resubscription and export links, and conditional orphaned-identity consequence.

## 9. Public website content architecture

The website content model supports:

- Infinite Ocean company story and contact information.
- Spyglass overview and core operating loop.
- Package landing pages for Agents, Work, Finance, Marketing, and future packages.
- Cross-package workflows and business outcomes.
- Pricing/plan comparison sourced from the public Catalog API.
- Guided screenshots/demo data that cannot expose a live customer account.
- Security, privacy, acceptable-use, terms, subprocessors, and status links.
- Signup, login, email verification, account recovery, and invite acceptance.
- Authenticated account switcher, profile/security, membership, package, usage, and billing entry.

Marketing content and application releases can share a design system but have independent caching and deployment rollbacks. Public catalog rendering fails closed to a last-known published catalog rather than displaying unpublished Stripe identifiers or offers.

## 10. API surface

```text
/api/v1/identity/*
/api/v1/users/me
/api/v1/accounts
GET    /api/v1/accounts/{account_id}/memberships
PATCH  /api/v1/accounts/{account_id}/memberships/{membership_id}
DELETE /api/v1/accounts/{account_id}/memberships/{membership_id}
POST   /api/v1/accounts/{account_id}/memberships/{membership_id}/suspensions
DELETE /api/v1/accounts/{account_id}/memberships/{membership_id}/suspensions
DELETE /api/v1/accounts/{account_id}/membership
POST   /api/v1/accounts/{account_id}/ownership-transfers
/api/v1/accounts/{account_id}/entitlements
/api/v1/catalog/public
/api/v1/accounts/{account_id}/checkout-sessions
/api/v1/accounts/{account_id}/billing-portal-sessions
/api/v1/accounts/{account_id}/billing
/webhooks/stripe
```

Account-scoped application routes use the same explicit account ID context. The server verifies that route, membership, token, cell assignment, data scope, and resource account all agree.

## 11. Audit and operations

Audit events include:

- User registration, verification, authentication, recovery, and session changes.
- Account creation, state, ownership, membership, and cell assignment.
- Immutable Membership role-change, suspension, reactivation, leave, removal, and ownership-transfer events with actor, target, before/after role/state, reason, and time.
- Catalog publication and Stripe mapping changes.
- Checkout and portal session creation without sensitive payment details.
- Webhook verification failure, duplicate, projection, and reconciliation.
- Subscription and billing state transitions.
- Entitlement grant, override, snapshot, downgrade, and package denial.

Operator tooling supports replaying a stored Stripe event, refreshing a provider object, recomputing an entitlement snapshot, comparing local/provider state, expiring an override, and explaining exactly why an account has or lacks a package.

## 12. Required tests

- Signup without billing creates an inactive zero-entitlement team shell that retains only the reviewed identity, privacy, security and billing recovery surfaces.
- One User can switch between accounts without data or entitlement leakage.
- Only authorized roles can start Checkout or Customer Portal sessions.
- Browser-supplied prices/offers cannot bypass the local catalog.
- Duplicate and out-of-order Stripe events converge correctly.
- Invalid signatures, stale signatures, wrong mode, and replay attempts are rejected.
- Cancellation, failed payment, grace, reactivation, upgrade, downgrade, and trial transitions produce expected snapshots.
- Free, subscription, promotion, grandfathered, and override grants combine deterministically.
- Package loss disables APIs, MCP tools, schedules, background jobs, and agent tools consistently.
- Downgraded package data follows read-only, export, retention, and restoration policy.
- Stripe outage does not break normal entitlement checks against a current local snapshot.

## 13. Stripe implementation references

- [Stripe subscription webhooks](https://docs.stripe.com/billing/subscriptions/webhooks)
- [Stripe Checkout subscriptions](https://docs.stripe.com/payments/checkout/build-subscriptions)
- [Stripe Customer Portal integration](https://docs.stripe.com/customer-management/integrate-customer-portal)
- [Stripe webhook security and delivery behavior](https://docs.stripe.com/webhooks)
- [Stripe Billing Entitlements](https://docs.stripe.com/billing/entitlements)
