# ADR-0002: System identity, Accounts, packages, and billing

- Status: Accepted
- Date: 2026-08-18
- Owners: Infinite Ocean product, identity, billing, and platform engineering

## Context

Spyglass needs a no-payment-required product preview, optional paid subscriptions, Users who may participate in more than one business, and independently configurable product areas such as Agents, Work, Finance, and Marketing. Treating a login, customer, subscription, and feature flag as the same concept would make authorization ambiguous and put Stripe in the availability path of the application.

## Decision

Production uses the following separate concepts:

- A **User** is one system-wide human identity, authentication profile, session owner, and security posture across Infinite Ocean.
- A **Spyglass Account** is the customer/business, data ownership, lifecycle, placement, usage, and billing boundary.
- A **Membership** gives one User a role in one Account. Authentication alone grants no Account authority.
- A **Billing Profile** is optional Account metadata and may reference one Stripe Customer. A free Account does not need one.
- A **Feature Package** is a versioned commercial and authorization unit such as Work, Agents, Finance, Marketing, Knowledge, or Integrations.
- An **Entitlement Grant** records why an Account may use a package: free plan, paid subscription, trial, promotion, grandfathering, support override, or internal policy.
- An immutable **Entitlement Snapshot** is the effective, explainable package mode and limit projection used at runtime.
- A **Subscription** is Spyglass's local projection of provider state for one Account; it is not the Account or the authorization record.

Free signup is one retry-safe business workflow:

1. Register and verify a User identity.
2. Create a Spyglass Account and owner Membership transactionally.
3. Assign the Account to a healthy data cell.
4. Attach current free grants and publish its initial Entitlement Snapshot.
5. Enter Spyglass with only entitled packages visible and callable.

This workflow creates no Stripe Customer, payment method, subscription, container, namespace, database, or credential set.

Paid conversion is separate. The server accepts only a published local Offer, creates or reuses the Account's Stripe Customer, and creates a Stripe-hosted Checkout Session. Browser return is informational. Only verified, durable, asynchronously projected Stripe events can change paid grants and publish a new Entitlement Snapshot.

Every package-owned operation enforces entitlement in the application use case. The same decision is applied to HTTP, MCP/tool routes, schedules, workflows, background dispatch, agent tool visibility, connector use, and usage admission. UI navigation is an explanation of access, not the security boundary. Runtime access checks read local snapshots and never call Stripe synchronously.

Package access supports at least `enabled`, `read_only`, and `suspended` modes; time windows; dependencies; and account-specific limits. Downgrade stops new mutation or background work according to package policy but does not immediately delete customer data.

## Consequences

- One User can belong to and switch among multiple Accounts without another login.
- An Account can remain free indefinitely and continues to function during a Stripe outage within its current entitlements.
- Packages can be sold in plans yet authorized independently, enabling different Account configurations without code forks or deployments.
- Stripe remains authoritative for provider payment/subscription facts while Spyglass remains authoritative for customer identity, Account membership, and effective application access.
- Entitlement changes are versioned, auditable, explainable, testable, and safe under duplicated or out-of-order provider events.
- Package code must not branch on plan names, raw Stripe Price IDs, or frontend navigation state.

## Rejected alternatives

- **One login per customer.** This prevents a system-wide identity and makes multi-Account membership and security management brittle.
- **Require billing during signup.** This blocks the free preview, adds provider availability to Account creation, and stores billing state earlier than necessary.
- **Use Stripe subscription state directly as authorization.** This cannot represent free grants, trials, overrides, grandfathering, dependency modes, or local safety suspension reliably.
- **Implement packages as deployment-time feature flags.** Account configuration would require code or infrastructure changes and would not provide auditable commercial history.

## Verification

- Free signup completes when Stripe is unavailable and creates no Billing Profile unless billing begins.
- Multi-Account tests prove session reuse without Membership, cache, entitlement, or data leakage.
- Contract tests exercise every package-owned entry point with enabled, absent, read-only, suspended, expired, and dependency-denied snapshots.
- Duplicate, delayed, and out-of-order Stripe event tests converge on the same Subscription and Entitlement Snapshot.
- Browser-supplied Offer or Stripe identifiers cannot grant access or begin unapproved Checkout.
