# Package Surface and Lifecycle Contract

- Status: accepted Phase 2.5 platform contract
- Scope: every executable package-owned surface in the current repository
- Related controls: [Accounts, packages and billing](accounts-packages-billing.md), [usage admission](usage-admission.md), [Account erasure](account-erasure.md)
- Machine-readable gate: [`deploy/package-surface-inventory.json`](../../deploy/package-surface-inventory.json)

## Canonical mode behavior

| Mode | Read/projection | New mutation or durable admission | Already-admitted work | Export/retention/release |
|---|---|---|---|---|
| `enabled` | Allowed | Allowed after role, capability and governed-limit checks | Runs normally | Allowed by the owning policy |
| `read_only` | Allowed | Denied as `package_read_only` | A bounded durable operation admitted under an earlier entitlement may settle, be explicitly canceled, or expire; it cannot add capabilities or enqueue successor work | Export and retention reads remain available; reconciliation and idempotent capacity release continue |
| `suspended` or absent | Denied as `package_not_entitled` | Denied | Safety/account suspension may cancel; commercial loss otherwise follows the same bounded settle/cancel/expire rule so results and capacity are not orphaned | Package APIs are denied. Account-level lifecycle/export operators remain reachable because they are not package authority |

Package loss never deletes customer data synchronously. Retained data is restored by a later effective `enabled` or `read_only` snapshot while it remains inside the owning retention window. Physical deletion is only the reviewed Account/package erasure workflow.

An operation is “already admitted” only after its durable package resource and entitlement version are committed. A browser request, queued HTTP retry, unsigned message or launcher request is not admission evidence. Agent run plans are bounded to a maximum 24-hour request lifetime and immutable capability list. Their dispatch, result projection, cancellation and usage release may finish after a commercial downgrade; no later step may expand the frozen plan.

## Current executable inventory

| Boundary | Package | Reads under read-only | Mutations denied under read-only/absent | Downgrade and completion evidence |
|---|---|---|---|---|
| Browser application | Work | Work page and retained item views remain available | Controls and client submissions are disabled in read-only; the server remains authoritative | Browser derives navigation and read-only state from the current immutable entitlement snapshot |
| Browser application | Agents schedules | Active and paused Schedule definitions remain visible | Create, revise, pause, resume, delete and trigger-now controls are absent under read-only access | Every command still crosses the routed HTTP authorizer; browser state is explanatory, never authority |
| App Router HTTP | Work | `GET work-items`, summary, item, and children | `POST work-items`, `POST .../transitions`, `PATCH .../assignment` | `routeRequirement` declares package and mutation before routing; signed cell authority carries only that package projection |
| App Router HTTP | Agents | `GET agent-boardrooms`, personas, conversations, messages and runs | Boardroom/persona creation, run creation and run resolution | The same route allowlist and authorizer semantics apply; unpublished methods/paths never reach authorization |
| App Router HTTP | Agents schedules | `GET schedules` and exact Schedule reads | Definition creation/revision, pause/resume/delete and separate trigger-now admission | Signed route authority binds Account, operation UUID and the current Agents package mode before the cell service reauthorizes |
| Cell admission broker | Work | Not a read surface | Work item capacity reservation requires current `enabled` Work access and a request-bound router proof | Reauthorizes against the global snapshot immediately before reservation; a downgrade between router and broker fails closed |
| Tool router | Work | `work.summary.read` is allowed when Work is `enabled` or `read_only` | No mutating tool is published | The runner capability and Account are verified, then current Work authorization is evaluated again; the runner cannot name an ungranted tool |
| Agent run creation | Agents | Existing boardroom/conversation/run reads remain | New run plans and retry resolutions require current `enabled` Agents access and governed concurrent-run admission | The durable plan records the entitlement version, immutable personas/tools and expiry |
| Agent dispatch/runner | Agents plus each frozen tool package | Projection/capability calls required by an already-admitted bounded run may settle | No new run can enter through dispatch; the immutable request cannot gain a tool after admission | Dispatch consumes only durable plans; broker request encryption, one-use runner identity, expiry, cancellation and result projection preserve exact Account/invocation binding |
| Work and Agent reconciliation workers | Owning package | May inspect and settle retained durable state | May not create customer work | Release remains idempotent after downgrade; dead-letter replay is an audited operator action, not package admission |
| Schedule execution worker | Agents plus each referenced context package | Not a customer read surface | Cannot admit new work when Agents is read-only/absent or a referenced package is unavailable | Identifier-only leases lead to current global authorization and exact-cell mTLS execution; recurring and trigger queues cannot expose definitions |
| Account lifecycle and erasure operators | Account-level, not a commercial package | Restricted inspection/export evidence and restore checkpoints | No customer-facing package mutation | These controls must remain operable after every package is absent so retention, export evidence, erasure and restore fencing cannot be bypassed by billing state |

## Surfaces intentionally not present in Phase 2.5

The current repository has no production MCP server, connector runtime, search/vector store, analytics export store, or customer-facing package export endpoint. “Not present” is a closed state: no route, tool, worker credential or network grant exists to bypass the matrix. Phase 3 must register each new surface here before implementation and must add enabled/read-only/suspended tests at its outermost transport and durable admission boundary. Customer-created Agent schedules are now executable and therefore intentionally absent from this closed list.

Catalog packages `Knowledge`, `Finance`, `Marketing` and `Integrations` are publishable product definitions but have no executable package-owned application surface yet. Only Work and Agents are executable. Public Catalog publication exposes stable descriptions, package dependencies, capabilities, offer presentation and governed limit definitions; it never exposes Stripe Product/Price IDs, provider credentials, internal support overrides, Account grants or unpublished drafts.

## Export, retention and restoration rule

Customer export is an Account-level lifecycle capability, not a paid-package permission. [ADR-0009](decisions/0009-account-portability-export.md), the executable builder, durable request/expiry state, private exact-version artifact adapter, private staging/pipeline kernel, global/cell repeatable-read coordinator, complete global/cell database projections and exact Knowledge/Marketing source factory now fix the deterministic archive, bounded snapshot, source-integrity, lease recovery, movement fencing, fail-closed schema/column registries and create-if-absent publication/deletion contracts. Phase 3 must still compose the worker, add short-lived download authorization and publish the customer transport before launch. Until then, `customer-export-api` remains correctly absent and the only end-to-end executable export field is restricted evidence supplied to Account-erasure preparation; it is not represented as customer self-service.

Every future package-owned store must declare, before its migration is accepted:

1. Account identity and cross-Account isolation key;
2. read-only behavior and the point where new durable admission stops;
3. treatment of already-admitted work, including maximum lifetime;
4. export format, chunking/checkpoint and artifact expiry;
5. retention start, duration and re-entitlement restoration behavior;
6. movement copy/capture/reconciliation handler;
7. erasure directive, acknowledgement and content-free attestation;
8. backup expiry and signed restore-replay behavior; and
9. schema/inventory tests that fail if the store appears without those handlers.

No product-level physical-erasure claim is permitted while an enabled external store lacks a successful attestation. An environment with zero enabled external Account stores records that empty inventory explicitly; it does not fabricate acknowledgements for absent systems.

## Change gate

A new route, MCP tool, schedule, worker, runner capability, connector or store cannot ship merely because the UI hides it. Its change must update this inventory, declare its Catalog owner, reuse the stable denial vocabulary, test all three modes, and extend Account movement/export/erasure coverage where it persists Account data. CI and deployment policy should treat an unregistered surface as a release failure.
