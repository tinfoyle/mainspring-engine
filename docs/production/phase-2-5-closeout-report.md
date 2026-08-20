# Phase 2.5 closeout report

- Audit date: 2026-08-20
- Audited revision: `b195fae07b264ea4a609e424d6664f0b776eb6a4`
- Purpose: finish Phase 2 without confusing this bridge with backlog item P2.5 (the entitlement engine)
- Verdict: **the codebase is ready to enter connected staging, but Phase 2 is not complete**

## Executive finding

Phase 2's core architecture is implemented: system-wide identity, Account and Membership boundaries, Catalog publication, local entitlement snapshots, Stripe projection, forced-RLS cell storage, signed global-to-cell routing, pooled workload references, release-image automation, and strong repository tests all exist.

The remaining work falls into two classes:

1. **Missing product behavior:** durable Account movement, complete downgrade/data-lifecycle behavior, compromised-credential operations, billing mismatch explanations, and external-store erasure.
2. **Missing target-environment evidence:** live Stripe and SMTP, real WebAuthn devices, applied Kubernetes and managed PostgreSQL isolation, load/failure/restore exercises, observability paging, accessibility certification, and signed-artifact rollback.

Local contracts that advance a placement generation between two test cells are not a durable Account-move implementation. Review-only Kubernetes manifests and a green local suite are not deployment evidence.

## Evidence reviewed

- The Phase 2 handoff and the ordered backlog in [delivery-plan.md](delivery-plan.md).
- The launch gates in [production-readiness-audit.md](production-readiness-audit.md).
- Production code, migrations, the 62-operation OpenAPI document, CI workflows, release image, Kubernetes reference, and operating runbooks.
- `ubunturojo` WSL verification: `go test ./...`, `go vet ./...`, `go run ./cmd/apicontract -check`, and `deploy/kubernetes/reference/verify.sh` passed.
- The recorded hosted website/CI result at the audited revision. The local WSL Node runtime is 18, below the site's declared Node 22.13+ requirement, so the website suite was not rerun locally.

The pre-existing `website/` worktree changes are line-ending-only and are outside this report.

## Closeout inventory

| Area | Implemented baseline | Work required for Phase 2 closure |
|---|---|---|
| P2.1 Website | Public routes, Catalog proxy, GET-only signup handoff, DOM/axe regression gates, anonymous staging probe | Consent/event taxonomy; deployed-origin proof; private-route browser axe; contrast, zoom/reflow, keyboard, screen-reader and supported-device evidence; legal/privacy/support/status ownership |
| P2.2 Identity | Password/passkey auth, recovery codes, session controls, owner enrollment, verified contact change, rotation tooling | Compromised-credential response; passkey rename; cleanup/retention jobs and metrics; live SMTP; real-device WebAuthn; reviewed factor-loss and attestation policies |
| P2.3 Accounts | Membership lifecycle, ownership transfer, closure, four-eyes database erasure and signed restore replay | Account export; signed external-store erasure directives, acknowledgements and attestations; post-retention physical deletion rehearsal |
| P2.4 Catalog | Immutable publication, governed offers and Stripe mappings, contract generation | Finish package/surface inventory; decide publication policy; publish a sanitized OpenAPI artifact; certify the staged Catalog |
| P2.5 Entitlements | Deterministic snapshots, modes, limits, shared admission boundary | Complete and test a package-by-transport downgrade matrix, including schedules, workers, runners, exports, retention, restoration and Agent tools |
| P2.6 Billing | Checkout/Portal boundaries, signed webhook inbox, projection, replay, refresh and reconciliation | Real Stripe test-mode journey; human-readable mismatch explanations; approved failure, grace, cancellation, refund/dispute and recovery policy |
| P2.7 Global/cell | Signed routing, generation fencing, forced RLS, constrained roles, two-cell contracts | Managed database application; ingress and sustained admission failure injection; route-key and CA rollover in staging; load/fairness proof |
| P2.8 Kubernetes | Security-reviewed shared workload reference, HPAs/PDBs/policies, release image workflow | Deployable staging and production overlays; exact egress; managed secrets/certificates; RuntimeClass; metrics adapter/monitors/alerts; connection caps; applied-cluster tests |
| P2.9 Placement | Directory states, generation rejection, test-only generation switch | Durable copy/change-capture/reconcile/switch/rollback/retire workflow with operator controls and recovery evidence |

## Required work order

### Gate 1 — create connected staging

Implement the staging path in [stage-production-deployment-report.md](stage-production-deployment-report.md). This is first because it unlocks live-provider, managed-database, accessibility, observability, load, failure and restore evidence.

Exit evidence:

- One exact signed image digest is deployed through a checked-in overlay.
- One global and two cell databases use migrated, non-owner, non-`BYPASSRLS` runtime roles.
- TLS SMTP, Stripe test mode, workload certificates, route keys, ingress, exact egress, monitoring and paging are connected.
- Website -> verification -> free Account -> owner enrollment -> Checkout -> signed projection -> paid access succeeds.
- Wrong origin, wrong Account, stale placement, duplicate delivery, restart and provider outage cases are repeatable.

### Gate 2 — implement durable Account movement

Add a Placement application boundary and persistence model; the current `internal/modules/placement/model.go` and test generation switch are insufficient.

Required implementation:

1. A globally durable move record with source/destination cell, source/target generation, phase, lease, attempts, checkpoints, rollback deadline, actor/reason and immutable events.
2. Capacity/health validation and a drain/freeze policy that explicitly defines allowed reads and writes.
3. Account-scoped initial copy adapters for rows and every enabled external store.
4. Change capture with a durable high-water mark; no in-memory delta buffer.
5. Reconciliation manifests for row counts/digests, objects, search state, workflows, entitlements and usage reservations.
6. A compare-and-swap placement-generation switch committed with audit evidence.
7. Destination resume and a retained, fenced source through a declared rollback window.
8. Rollback as a new generation switch, followed by verified source retirement only after policy permits it.
9. A resumable worker plus bounded inspect/pause/resume/rollback operator commands using signed, exact-scope authorization.
10. Integration tests for every crash boundary, stale workers, duplicate commands, source/destination outage, global outage, database failover and cross-Account attacks.

Restore, export, erasure, cell evacuation and dedicated enterprise placement must reuse the same Account identity and placement-generation boundary.

### Gate 3 — close package downgrade and data lifecycle

Create a version-controlled matrix whose rows are every published package/capability and whose columns are HTTP, private UI, MCP, schedules, workers, runners and Agent tools. For each cell define:

- mutation cutoff and read-only behavior;
- in-flight/background behavior;
- export availability;
- retention start/duration;
- restoration after re-entitlement;
- failed-payment, grace, cancellation and safety-suspension behavior.

Turn the matrix into table-driven application, transport and asynchronous-boundary tests. Then complete external-store export/erasure directives, acknowledgement retries, attestations and a restore-then-delete rehearsal.

### Gate 4 — complete live customer and provider journeys

- Add compromised-credential response, passkey rename, durable cleanup and metrics.
- Exercise registration, verification, recovery, invitations and contact change through real TLS SMTP.
- Exercise passkey enrollment, login, step-up, recovery and replacement on the supported device/browser matrix.
- Exercise the full Stripe sequence in [staging-certification.md](staging-certification.md), including mismatch explanation, replay and refresh.
- Exercise multi-Account switching, ownership transfer, closure, export and restoration.
- Complete public/private accessibility and responsive certification against the exact artifact.

### Gate 5 — prove pooled operations and release recovery

- Run many-small-Account, one-hot-Account and deterministic Work write/replay certification.
- Inject queue backlog, pod/node/zone loss, database failover, provider degradation, saturation and stale-cache faults.
- Rehearse Work/Agent dead-letter recovery using constrained staging roles.
- Connect the Collector, authenticated scrapes, dashboards, remaining billing/isolation/database/restore signals and paging.
- Complete a burn-in, one game day, clean-environment release, restored-environment release, and rollback between two retained signed digests.

## Scope control

Phase 3 foundations do not have to block Phase 2 closure. Incomplete Work, Agents, Knowledge, Finance or Marketing capabilities must remain unpublished, preview-only or unavailable in entitlements and public claims. A production launch may expose only capabilities that pass the exact-artifact gates; hiding navigation alone is not sufficient.

## Phase 2 completion rule

Phase 2 is complete only when:

- every P2.1-P2.9 acceptance criterion is implemented or explicitly removed from launch scope;
- all P0 launch blockers in [production-readiness-audit.md](production-readiness-audit.md) have current evidence from the exact staged release artifact and target environment;
- Account movement and rollback have been exercised, not merely simulated by changing a generation;
- package downgrade/data-lifecycle rules are enforced at every synchronous and asynchronous boundary; and
- the retained release can be promoted or rolled back without rebuilding it.
