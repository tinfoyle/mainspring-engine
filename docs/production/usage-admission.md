# Package and Usage Admission

Spyglass has one application boundary for starting package-owned work: `usageadmission.Service`. HTTP handlers, MCP tools, schedules, workflow dispatchers, background jobs, and agent tools must call the owning feature use case, which calls this service before creating the durable resource or dispatching work. No surface interprets plan names, navigation state, Stripe state, or raw Catalog JSON as authorization.

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
- The transaction locks the Account entitlement version and the counter. A version change returns `ErrEntitlementChanged`; the service reloads authorization once and never admits against a stale maximum.
- Concurrent reservations serialize at the counter boundary and cannot exceed the effective maximum. A denial returns `limit_exceeded` with safe current and maximum values.
- Release is idempotent and remains available after package downgrade so retained resources and canceled work can return capacity.
- Expired active reservations are closed and reclaimed in the next transaction for that counter. A finalized request ID is never resurrected.
- Negative counters, over-release, or inconsistent reservation state fail as corruption; application code does not repair those records with ad hoc SQL.

Capacity reservation and the eventual feature-domain mutation cannot always share a database transaction because cell workloads and the global control plane are separate. Feature use cases therefore use a durable operation ID, release the reservation after a failed creation, and reconcile orphaned domain objects/reservations. A reservation receipt is evidence of capacity admission, not evidence that the business operation completed.

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
