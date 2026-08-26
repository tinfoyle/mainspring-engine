# Package and Usage Admission

Spyglass has one application boundary for starting package-owned work: `usageadmission.Service`. HTTP handlers, MCP tools, schedules, workflow dispatchers, background jobs, and agent tools must call the owning feature use case, which calls this service before creating the durable resource or dispatching work. No surface interprets plan names, navigation state, Stripe state, or raw Catalog JSON as authorization.

The complete inventory and post-admission downgrade rules are maintained in [Package surface and lifecycle](package-surface-lifecycle.md). Surfaces that do not yet exist are recorded as absent rather than treated as implicitly enforced.

## Governed limit definitions

Every new Catalog draft explicitly defines each package default limit:

```json
{
  "code": "concurrent_runs",
  "package_code": "agents",
  "name": "Concurrent agent runs",
  "unit": "run",
  "kind": "capacity",
  "combine": "maximum",
  "reservation_ttl_seconds": 3600
}
```

- `code` is stable within its owning `package_code`.
- `unit` gives operators and clients a presentation-safe unit; it is not used for arithmetic conversion.
- `kind` is currently `capacity`. Periodic consumption and monetary/model budgets require separate reviewed semantics before they can be published.
- `combine` is one of `replace`, `add`, `maximum`, or `minimum`. Entitlement evaluation applies that rule to active grants in deterministic priority and grant-ID order.
- `reservation_ttl_seconds` is optional. A positive value recovers abandoned transient capacity, such as a crashed agent run. Long-lived resources such as documents release capacity when their owning resource is deleted or leaves the counted state.

Launch document decision (2026-08-26): the `documents` capacity is 1,000 logical Documents per team. Revisions do not consume separate slots. A Document releases its slot only after guarded physical deletion finishes; requested, queued, quarantined, processing and retained deletion states remain counted. Admission of a 1,001st Document must fail before object creation. A reviewed versioned Support override may raise the Account limit without creating a price or feature tier.

Launch Work decision (2026-08-26): the `active_items` capacity remains 100 per team. Every active child counts separately. Completed and cancelled items preserve history but release capacity when the terminal transition commits. Admission of a 101st item fails atomically. A reviewed versioned Support override may raise the Account limit without creating a price or feature tier.

Launch Agent decision (2026-08-26): the `concurrent_runs` capacity remains two per team. Queued Runs do not consume a slot until admitted. Admitted nonterminal Runs hold one reservation; terminal result, cancellation or bounded TTL reclamation releases it exactly once. A third concurrent admission cannot begin provider execution. A reviewed versioned Support override may raise the Account limit without creating a price or feature tier.

Launch AI-token decision (2026-08-26): customer usage is denominated in Infinite Ocean AI Tokens rather than vendor token counts or provider cost. Admission freezes an immutable rate-card version and atomically reserves the operation's maximum AI Tokens before any external model call. Settlement uses trusted normalized input/cached/output/tool usage, debits the exact customer amount, releases unused reservation once and records provider cost separately for margin analysis. A failed or unstarted provider operation cannot consume an estimated maximum; replay cannot double-debit. Published rate revisions apply prospectively only and model/provider substitution cannot mutate prior ledger entries.

AI Token commerce amendment (2026-08-26): renewal grants, top-up bundles, promotional grants, complexity-to-model mappings and customer usage rates are separate immutable versioned records with prospective effective windows. The construction seed grants 10,000 tokens on a successful renewal and offers 10,000 for $10, but neither number is a permanent exchange rate or release promise. Later publications may change quantities and prices independently. Promotion issuance appends an idempotent labeled grant with a bounded campaign/version; it never edits an earned, purchased or previously consumed balance. Agent complexity selects one reviewed rate class, while provider/model selection remains internal and is frozen at Run admission.

AI Token balance amendment (2026-08-26): included renewal grants expire without carryover when the next successful renewal grant commits. Purchased grants remain non-expiring while the Account is active. Promotional grants require an explicit published expiry. Admission reserves earliest-expiring cohorts first, using creation time and grant ID for deterministic ties and preserving non-expiring purchased cohorts until last. Exact settlement and release retain cohort identity; a release cannot resurrect a cohort that expired during execution.

AI Token restricted-access amendment (2026-08-26): a restricted Account cannot admit or reserve new AI work even when its ledger has an available balance. Remaining grants may be reported through the necessary billing/privacy/export surfaces. Recovery before Account deletion restores the surviving purchased cohorts, while included and promotional cohorts continue to expire normally. Account deletion extinguishes all remaining grants, with no restoration or migration to a replacement Account and no cash value or refund except where law requires.

AI Token ownership amendment (2026-08-26): grants and reservations are scoped to the receiving team Account, not a User. Membership or ownership changes cannot move them, and neither application nor Support authority may transfer a cohort between Accounts. Corrections append same-Account reversal or replacement ledger entries. Account merger, split or replacement handling remains a reviewed commercial procedure with no automated balance migration.

AI Token refund amendment (2026-08-26): an authorized owner or billing administrator with recent passkey confirmation may ask Support to refund a purchased top-up within 14 calendar days only if no token from that exact grant has been consumed or remains reserved. Any consumption rejects full and partial discretionary refunds. Review must wait for outstanding reservations to settle or release. A confirmed Stripe refund appends an idempotent full-grant reversal; included and promotional grants are not refundable, while duplicate, fraud, technical-error and legally required remedies follow their separate exception paths.

AI Token issuance amendment (2026-08-26): a signed Stripe projection grants the configured full included quantity exactly once for each successfully paid positive initial or recurring service-period invoice, independent of discount and tax. Proration, retry, commissioning and top-up lines cannot duplicate that grant. A top-up projects its frozen Catalog quantity exactly once. Subscription Refund/final-lost-dispute handling removes only the unused included remainder and cannot create negative usage or mutate settled entries.

AI Token authority/admission amendment (2026-08-26): active members may inspect safe shared totals, but consumption still requires the package, role, object and action authorization that would apply without metering. Only an owner or billing administrator with recent passkey evidence may manually purchase a top-up or initiate refund review; launch performs no automatic replenishment. Insufficient balance rejects the maximum reservation before queue/provider contact with `ai_tokens_insufficient`, preserves caller input and exposes a purchase-or-renewal recovery. No customer debt is created.

AI Token rate/promotion amendment (2026-08-26): comparable same-complexity rate increases receive 30 days' notice before prospective effect, except an urgent security, legal, provider-withdrawal or severe-reliability replacement may take effect immediately with notice as soon as practicable. Existing Runs stay frozen; existing quantities do not preserve an old rate card. Promotion records freeze audience, window, expiry, redemption limits, issuance cap, price/quantity effect, disclosure and stacking, redeem exactly once and never mutate history. Token top-ups and promotional grants are Affiliate-ineligible.

AI Token adversity amendment (2026-08-26): an open top-up dispute freezes the grant's unused cohorts. A win restores what survives; a successful Refund, reversed payment or final lost dispute appends an idempotent reversal of the remainder. Settled consumption remains immutable and cannot drive balance negative. Fraud and repeated-adversity response remains reviewed Account/security procedure.

Agent AI Token admission implementation checkpoint (2026-08-26): the global admission service now owns the private five-level execution-policy overlay and the Account AI Token reserve/close boundary. A cell dispatch worker resolves the current public rate by frozen Persona complexity, reserves before runner provisioning and atomically stores the reservation, rate and private execution target on the Account-owned invocation. Projection closes that reservation with normalized input, cached-input, output and actual tool-call usage before publishing a successful result; failure and terminal queue paths release it. The original reservation and rate remain authoritative across retries and later Catalog changes. Local workload identities restrict the private reserve/close endpoints to dispatch and projection workers, and global portability includes customer-visible grants and ledger entries while excluding operational reservations and internal correlation keys.

The public Catalog endpoint exposes these product definitions but never provider mappings. New governed drafts fail if a package default has no definition, the package owner is unknown, the rule or kind is unsupported, or a reservation TTL exceeds 30 days. Immutable Catalog versions created before limit definitions remain rollback-compatible with conservative `capacity`/`replace` semantics.

## Decision sequence

1. Authenticate the User or workload actor through its supported authorization adapter. The current persistent composition supplies the Membership-backed User adapter; future signed route/workload claims implement the same interface and must not impersonate a User.
2. Resolve and verify the Account, active Membership or workload authority, Account state, and role.
3. Load the latest immutable Account entitlement snapshot.
4. Return `package_not_entitled` when the package is absent or suspended.
5. Return `package_read_only` when a mutation is requested under read-only access.
6. Resolve the numeric maximum and its immutable limit policy from the same snapshot; return `limit_not_defined` if either is absent or inconsistent.
7. Atomically reserve capacity using the Account, package, limit, amount, entitlement version, and caller's UUID request ID.
8. Apply the feature module's object, capability, and approval rules before committing its domain mutation.

The authorized `AccountContext` carries the exact entitlement version and package projection used in this decision. Stripe is never called in this path.

## Durable reservation semantics

`entitlement_usage_counters` owns the current reserved amount for one Account/package/limit. `entitlement_usage_reservations` is the immutable operation identity and lifecycle record.

- The caller supplies one UUID request ID per logical operation. Repeating the same Account/request/amount returns the original reservation without consuming capacity again.
- Reusing that key for another package, limit, or amount returns a conflict.
- A response distinguishes a newly inserted reservation from an idempotent replay for saga compensation decisions. This flag is operational metadata, not a persisted billing fact.
- Released or expired request IDs are final. A later reserve returns `reservation_closed`; it never treats a closed record as active admission.
- The transaction locks the Account entitlement version and the counter. A version change returns `ErrEntitlementChanged`; the service reloads authorization once and never admits against a stale maximum.
- Concurrent reservations serialize at the counter boundary and cannot exceed the effective maximum. A denial returns `limit_exceeded` with safe current and maximum values.
- Release is idempotent and remains available after package downgrade so retained resources and canceled work can return capacity.
- Expired active reservations are closed and reclaimed in the next transaction for that counter. A finalized request ID is never resurrected.
- Negative counters, over-release, or inconsistent reservation state fail as corruption; application code does not repair those records with ad hoc SQL.

Capacity reservation and the eventual feature-domain mutation cannot always share a database transaction because cell workloads and the global control plane are separate. Feature use cases therefore use a durable operation ID, release the reservation after a failed creation, and reconcile orphaned domain objects/reservations. A reservation receipt is evidence of capacity admission, not evidence that the business operation completed.

## Private admission broker

Cell workloads do not receive a global database connection. `spyglass admission-api` exposes a private, Work-scoped broker that accepts only a still-valid app-router proof bound to an allowlisted mutation and signed UUID operation ID. It re-verifies the exact cell audience and route, then reauthorizes current Membership, Account state, package mode, limit, and entitlement version against the global database before calling `usageadmission.Service`.

The broker role can select Accounts, Memberships, and entitlement snapshots and can select/insert/update only usage counters and reservations. It executes `spyglass_lock_account_entitlement_version(uuid)`, a public-execute-revoked security-definer function, to take the required row lock without gaining Account-update authority. It cannot read Users, sessions, Billing, or any cell Work table. App-api can reach the broker through NetworkPolicy but holds only its cell database credential; TLS 1.3 additionally requires its exact configured SPIFFE workload identity before the broker handler runs.

Work compensates only a definitive cell rollback. A newly reserved operation that loses a serialization conflict may be released. An existing reservation plus a payload conflict is never released because it may back an already-created item. A transport or commit error retains capacity and returns an unknown-outcome response; an exact retry with the same body and operation ID resolves the ambiguity idempotently.

## Stable denial vocabulary

| Code | Meaning | Typical response |
|---|---|---|
| `package_not_entitled` | Package absent or suspended | Offer an upgrade or explain suspension |
| `package_read_only` | Reads allowed; new mutation denied | Preserve export/retention paths |
| `limit_not_defined` | Snapshot lacks a valid governed policy | Fail closed and alert operators |
| `limit_exceeded` | Reservation would exceed effective capacity | Show usage, limit, and a safe recovery/upgrade path |

Transport adapters map these codes into their native error envelope without renaming them. Feature modules may add narrower object or approval denials after package admission; they may not translate a package denial into “not found” for convenience unless non-disclosure policy explicitly requires it.

## Operations

Monitor reservation denial rate, active and expired counts, oldest expiring reservation, counter saturation, entitlement-race retries, conflicts, and corruption failures by safe Account hash/package/limit. Alert on counters above their current snapshot maximum, reservations remaining active beyond their TTL, or any corruption error.

Changing a limit requires a reviewed Catalog draft and publication. The entitlement rollout updates existing Account snapshots. Operators never increase a customer's counter or maximum directly. During incident diagnosis, compare the Account's latest snapshot, `entitlement_usage_counters`, active reservations, and the feature module's counted resources using read-only queries; repair is an explicit reconciler with an audit record.

Before enabling the first capacity-enforced feature in an upgraded environment, publish a current-equivalent governed Catalog containing explicit limit definitions and wait for its entitlement rollout to complete. Pre-definition snapshots intentionally return `limit_not_defined`; deployments do not rewrite immutable snapshots in place or silently invent policy at request time.
