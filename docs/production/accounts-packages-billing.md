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
- One account has exactly one active owner. Ownership transfer requires recent user-verified passkey proof, explicit current versions for both Memberships, a bounded reason, and one atomic role swap that promotes the successor while demoting the previous owner to Administrator.
- Exercising any Owner Membership requires the system-wide User to have at least one passkey and at least one unused recovery code. The shared authorizer fails closed for both reads and mutations; the Account list exposes a safe enrollment-required flag while global identity/security endpoints remain reachable. Non-owner Memberships are not subject to this owner-only gate.
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
- `past_due` enters immediate read-only remediation: reads, export and release/reconciliation continue, while new mutations, schedules and runs stop.
- Stripe `pause_collection` on an otherwise active/trialing subscription uses the same read-only remediation policy. A subscription whose actual status is `paused` grants no paid packages.
- `incomplete`, `incomplete_expired`, `unpaid`, `paused` and `canceled` grant no paid packages; independent free, promotion or support grants still evaluate normally.
- Cancellation scheduled at period end retains the current grant until Stripe changes the current subscription state. Recovery to `active` or `trialing` restores the mapped grant on refresh.
- Upgrade, downgrade and proration are provider billing operations. Projection resolves the current Price to one immutable published Offer and atomically replaces that subscription's grants; Spyglass does not calculate money or prorations.
- Invoice, refund and dispute webhooks are invalidation signals, never direct entitlement commands. Current subscription state remains authoritative; a future dispute-specific safety suspension requires its own reviewed grant source and tests.
- Tax calculation, invoice presentation, credits and manual collection never independently grant access.

Access checks read the local EntitlementSnapshot, never call Stripe synchronously. A reconciler periodically compares Stripe Customer/subscription state with local projections and produces operator-visible mismatches.

## 8. Stripe product mapping decision

Spyglass v1 uses stable Stripe Customer, Product, Price, Checkout, Subscription, Invoice, and Customer Portal APIs behind a Billing provider adapter. The Stripe API version is pinned and upgraded deliberately.

Stripe Billing Entitlements may mirror paid package features, but it is not the sole Spyglass authorization source because free plans, trials, promotions, internal accounts, grandfathering, emergency suspension, and support overrides also affect access. The local Entitlements module always produces the effective snapshot used by the application.

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

- Signup without billing creates a usable free account.
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
