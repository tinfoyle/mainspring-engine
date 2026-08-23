# Account Export and Erasure Workflow

Status: production contract; logical closure, four-eyes preparation, leased cross-store execution, content-free global/cell tombstones, checkpoint quarantine, and signed database restore replay are executable; external-system reconciliation, directive publication, deployment grants/runbooks, and production review remain release gates

Account closure and Account erasure are deliberately different operations. Closure is customer-facing, recoverable during cooling-off, and eventually disables normal access. Erasure destroys live customer data after retention and therefore requires reviewed operator authority, export evidence, cross-store reconciliation, and a durable content-free tombstone.

No HTTP request, account-api replica, or lifecycle-worker attempt may directly erase an Account.

## Safety invariants

1. The Account is already `closed`; `closing`, suspended, restricted, and active Accounts are ineligible.
2. `account_closure_requests.delete_after` has passed according to PostgreSQL time.
3. No active subscription, non-expired Checkout, active usage reservation, moving placement, or unfinished capacity-release job remains.
4. An export artifact built under [ADR-0009](decisions/0009-account-portability-export.md) exists, has a SHA-256 digest, and has a separately governed expiry. An operator may explicitly record a policy-approved `not_applicable` export only with a reason; an empty reference is never interpreted as approval.
5. The requester and approver are distinct externally authenticated operator identities. Both record environment and reason. Neither string is authentication by itself.
6. The target environment and Account UUID are repeated exactly at each destructive invocation.
7. The target cell ID and placement generation are snapshotted at preparation. A later placement change invalidates approval.
8. Cell erasure commits before global erasure. A content-free cell tombstone makes a crash between those steps idempotently recoverable.
9. Global erasure verifies the cell tombstone, deletes Account-scoped live records in one transaction, decrements cell capacity, and converts the request to a content-free global tombstone.
10. User identity, passkeys, sessions, security events, and Memberships in other Accounts are not erased. Identity deletion is a separate User-scoped workflow.
11. The final tombstones retain no raw Account UUID, name, slug, email, Stripe identifier, Work identifier, free-text reason, or export location. They retain a keyed Account fingerprint, request ID, policy version, environment, stage timestamps, row-class counts, export digest, and backup-expiry deadline.
12. Backups are not rewritten in place. The tombstone tracks the latest backup-expiry deadline; restore procedures must replay completed erasures before a restored environment can serve traffic.
13. Every committed cell and global tombstone advances a content-free deterministic hash chain. Deployments pin an externally archived sequence/root checkpoint; retaining every historical checkpoint permits a newer live database while rejecting a restored database that predates the pinned erasure evidence.

## Authority and process boundaries

The workflow uses short-lived, human-authorized jobs rather than a standing high-privilege worker. The binary exposes reviewed preparation, inspection, approval, pre-execution cancellation, leased execution, and signed restore replay:

```text
spyglass account-erasure-admin prepare
spyglass account-erasure-admin inspect
spyglass account-erasure-admin approve
spyglass account-erasure-admin execute
spyglass account-erasure-admin restore-replay
```

`prepare`, `inspect`, `approve`, and global finalization use a narrow global credential. Cell execution uses a credential for exactly the snapshotted cell. Every action first verifies the phishing-resistant exact-scope envelope in [Platform Operator Authorization](operator-authorization.md); break glass additionally requires an incident, two independent approvers, and an exact deployment confirmation. `execute` receives both database URLs, but they remain distinct pools and execute-only database roles. It also receives a 32-byte evidence key used for domain-separated HMAC Account fingerprints and operator-evidence digests; the key and raw Account identifier are absent from completed tombstones and completion logs. `restore-replay` uses separate global and cell replay-only roles, an archived signed directive, and a distinct 32-byte directive-verification key. It never receives the live evidence key. The command receives no browser session, serving credential, Stripe secret, SMTP secret, route-signing key, or arbitrary SQL surface.

Every action requires:

- `SPYGLASS_OPERATOR_ID`
- `SPYGLASS_OPERATOR_REASON`
- `SPYGLASS_ENVIRONMENT`
- matching `SPYGLASS_CONFIRM_ENVIRONMENT`

Customer-erasure and restore-replay actions additionally require:

- `SPYGLASS_ACCOUNT_ID`
- matching `SPYGLASS_CONFIRM_ACCOUNT_ID`
- `SPYGLASS_ACCOUNT_ERASURE_REQUEST_ID`
- the expected workflow version for live `execute`; restore replay instead requires the exact signed directive file

Production access control authenticates the human, authorizes the action, injects the short-lived database credential, and records the change/incident reference outside Spyglass. Environment variables only carry the resulting evidence into the command.

## Durable state machine

```mermaid
stateDiagram-v2
  [*] --> prepared: requester + eligibility + export evidence
  prepared --> approved: distinct approver
  prepared --> canceled: requester or approver
  approved --> cell_erasing: exact execute claim
  cell_erasing --> cell_erased: atomic cell purge + tombstone
  cell_erasing --> approved: retryable failure / expired lease
  cell_erased --> global_erasing: cell tombstone verified
  global_erasing --> completed: atomic global purge + tombstone
  global_erasing --> cell_erased: retryable failure / expired lease
  completed --> [*]
```

Cancellation is allowed only before `cell_erasing`. After any cell data is destroyed, the workflow is forward-only. A failure never restores deleted data and never reports completion while a store is unverified.

The global request carries an optimistic version. Execution claims carry a random lease ID and expiry. An expired claim is reclaimable only from its immediately preceding durable state. Every state transition and operator action is immutable.

## Preparation evidence

Preparation snapshots and verifies:

| Evidence | Rule |
|---|---|
| Closure | Latest current closure is `closed`; `delete_after <= statement_timestamp()` |
| Account | State is `closed`; Account version matches the closure result |
| Placement | Directory is disabled/frozen, not moving; Account and directory cell/generation agree |
| Billing | No locally projected subscription except `canceled` or `incomplete_expired`; no active Checkout |
| Usage | No active reservation and all counters are zero |
| Work release | No pending, processing, failed, or dead-letter release job for the Account in the target cell |
| Export | URI/reference is opaque, bounded, and never logged; SHA-256 is exactly 32 bytes |
| Backup | Deadline is at or after the environment's maximum backup retention |
| Policy | Immutable policy version identifies the exact row classes and external systems covered |

Eligibility is rechecked at approval and immediately before each destructive stage. Preparation is not a future permission to ignore changed state.

## Cell erasure transaction

The cell security-definer function accepts only request ID, Account UUID, expected placement generation, keyed Account fingerprint, policy version, operator evidence, and expected request version. It first checks for an existing tombstone:

- Matching request, fingerprint, generation, and policy returns the stored result as an idempotent success.
- Any mismatch is a conflict and deletes nothing.

It then locks `spyglass.account_namespaces`, verifies the namespace is disabled/frozen at the expected generation, and rejects unfinished release jobs. It deletes in dependency order:

1. `spyglass.route_context_receipts`
2. `spyglass.route_context_receipt_cleanup_queue`
3. `spyglass.work_capacity_release_queue`
4. `spyglass.work_item_events`
5. `spyglass.work_items` from deepest children to roots, or through a deferred-safe set delete
6. `spyglass.work_item_number_counters`
7. `spyglass.account_audit_events`
8. Account-targeted `spyglass.work_capacity_release_operator_events`
9. `spyglass.account_namespaces`

Before commit it inserts `spyglass.account_erasure_tombstones` with aggregate counts only. The function must provide a narrowly scoped bypass for immutable audit triggers; the bypass is usable only inside this security-definer function and never granted to serving or ordinary operator roles.

Future package migrations must register every Account-owned cell table in the erasure policy and its integration fixture. A schema gate fails when a new Account foreign key/RLS table is not covered by the current policy version.

## Global erasure transaction

Global finalization verifies the cell tombstone through the cell read-only attestation function, then locks the erasure request, closure request, Account, and directory snapshot. It rechecks billing, placement, usage, and workflow version.

It deletes or minimizes in dependency order:

1. Account-attributed notification outbox rows
2. entitlement recompute queue rows
3. entitlement usage reservations and counters
4. billing Checkout attempts, subscriptions, and billing profile
5. entitlement snapshots and grants
6. invitation rows
7. Account lifecycle and Membership audit events
8. closure requests
9. Memberships
10. account directory
11. Account

The transaction decrements `cells.assigned_accounts` with an underflow guard and converts the erasure request/operator history into its content-free tombstone representation. Raw operator reason and Account UUID exist only in the restricted active-workflow tables and are removed at completion.

The notification outbox now carries nullable Account attribution outside its encrypted envelope. Verification and recovery remain User-scoped; invitations and ownership-transfer notices are Account-attributed and can be selected for erasure without decrypting unrelated identity messages.

`billing_event_inbox` now records nullable Account attribution without a foreign key, because a provider retry can arrive after the Account row is gone. Stripe ingestion derives it only from a valid Spyglass Account UUID in object metadata, Checkout client reference, or subscription metadata. Unknown and non-Account events remain unattributed. Attributed ingestion acquires the transaction advisory key `hashtextextended('spyglass:account-erasure:' || account_id, 0)` before checking Account existence and inserting. Global finalization must acquire that exact key before deleting attributed provider payloads and the Account. A retry that wins the fence before finalization is deleted by that transaction; one that wins afterward is acknowledged without persisting its payload. Global finalization still needs the erasure-only audit-trigger bypass and content-free tombstone classification.

## External systems

Database completion is not full erasure unless the policy version also reconciles external systems:

- Revoke connectors and delete Account-scoped OAuth credentials.
- Stop schedules, workflows, agent runs, and ephemeral runners.
- Delete object-store versions, search/vector indexes, caches, and analytics exports.
- Delete or detach the Stripe Customer only according to finance/tax retention policy; retain no payment data in Spyglass.
- Expire the customer export according to its disclosed download window.
- Record the maximum backup expiry and ensure restore automation reapplies the tombstone.

Each executor produces a signed or locally verifiable attestation digest. The global request stores stage digests while active; the final tombstone stores only a Merkle/root digest or equivalent policy evidence, not provider object identifiers.

## Failure and recovery

| Failure point | Safe retry behavior |
|---|---|
| Before cell commit | Lease expires; no tombstone means retry the cell transaction |
| After cell commit, before global acknowledgement | Cell tombstone makes cell execution idempotent; global state advances on retry |
| Before global commit | Account still exists closed; retry after re-verification |
| After global commit, before command output | Global tombstone makes completion idempotent |
| Attestation mismatch | Stop; mark `investigation_required`; never select a different cell automatically |
| New billing/usage/work activity | Treat as integrity incident because closed Accounts should reject it; erase nothing |
| Backup deadline unknown | Remain approved but ineligible for execution |

No automatic cross-cell fallback is allowed. A missing namespace is success only when a matching tombstone exists; otherwise it is an investigation state.

## Restore readiness checkpoint

`public.account_erasure_restore_ledger` and each cell's `spyglass.account_erasure_restore_ledger` retain sequence zero plus one immutable entry per tombstone. The chain hashes only stable, content-free policy evidence: request ID, keyed Account fingerprint, policy/request versions, environment, evidence digests, placement generation where applicable, and backup deadline. It excludes Account UUID, User ID, names, email, Stripe identifiers, reasons, export locations, timestamps of execution, and row counts. This makes the same ordered directive stream reproduce the same checkpoint after restore even when deletion counts differ from the original run.

Every serving process receives an externally pinned historical sequence/root. Global processes check the global ledger; cell processes check their cell ledger; Work reconciliation checks both. Startup fails if the checkpoint is absent. Ingress checks it before every non-liveness request through a positive-only five-second cache, and workers terminate within the bounded monitor interval if it disappears. Errors are never cached. A healthy PostgreSQL ping cannot override this gate. Operator and migration commands remain available so a quarantined restored environment can be repaired.

`spyglass account-erasure-admin restore-replay` consumes one strict JSON envelope containing a versioned directive and HMAC-SHA-256 signature. The signature covers the exact request, Account, cell, original placement generation, content-free erasure evidence, timestamps, and previous/expected cell and global checkpoint pairs. Verification happens before database access. Replay then resolves only the restored Account's current placement, requires the configured cell to match, re-erases that cell, and finally re-erases global data. Each security-definer function locks its ledger, requires the exact previous checkpoint, and verifies that the newly derived root equals the directive. Reordered, omitted, forged, cross-environment, cross-Account, cross-cell, and already-divergent streams fail closed. A completed replay is attested exactly and is idempotent.

The external ledger publisher remains a separate production trust boundary. It must durably archive directives in ledger order, protect the signing key outside workload credentials, publish each cell/global checkpoint only after durable archival, and advance deployment pins after completion. The executable command intentionally accepts one reviewed local file and does not fetch a directive or select another cell automatically. Production remains disabled until that publisher/archive and the restricted Job/runbook are deployed and drilled.

## Observability and privacy

Metrics use bounded stage/result labels and no Account identifier. Logs may include request ID, policy version, environment, stage, aggregate row counts, and machine error code. Raw Account UUID and export reference are emitted only by explicit restricted `inspect` output and never by standing health endpoints.

Alerts cover oldest approved age, expired execution lease, attestation mismatch, post-close activity, backup deadline breach, repeated retry, and requests stuck after partial erasure.

## Verification requirements

Disposable global/cell PostgreSQL tests must prove:

- before-retention, active billing, active usage, moving placement, and unfinished Work release all fail without mutation;
- requester cannot approve their own request;
- stale version/environment/Account confirmation fails;
- cell deletion removes every registered Account row while preserving another Account under forced RLS;
- crash after cell commit retries through the matching tombstone;
- a missing namespace without a tombstone fails closed;
- global finalization refuses a missing/mismatched cell attestation;
- global deletion removes every registered Account row, preserves shared User identity and other Accounts, and decrements cell capacity once;
- repeated execution returns the same completed tombstone and never underflows capacity;
- immutable event tables reject ordinary mutation and permit only the erasure function's exact target;
- tombstones contain no raw Account, User, Work, Stripe, email, name, reason, or export-reference value;
- a forged or out-of-order restore directive mutates nothing;
- an original database and a pre-closure restored database converge to identical signed cell/global checkpoints;
- replay removes only the exact Account, preserves another Account, passes pinned readiness, and is idempotent.

The schema-coverage test inventories Account foreign keys, composite Account keys, RLS tables, object namespaces, and attributed outbox/provider rows. A new Feature Package cannot ship Account-owned storage without updating this contract and its deletion proof.

## Delivery sequence

1. Notification/provider payload attribution, restricted active request/operator-event schemas, and content-free cell/global tombstones are executable.
2. Prepare/inspect/cancel/approve services, repeated cell readiness, four-eyes enforcement, and eligibility tests are executable with no deletion authority.
3. The cell security-definer erasure/attestation boundary, forced-RLS exact targeting, content-free tombstone, concurrent idempotency, and multi-Account isolation tests are executable. Its function remains revoked from `PUBLIC` and no command currently invokes it.
4. Idempotent leased cross-database execute orchestration, repeated cell attestation, shared billing-ingestion fencing, and atomic global finalization are executable through split database roles.
5. Integrate connector, object, index, analytics, Stripe-retention, and export-expiry attestations as those stores become executable.
6. Content-free checkpoint ledgers, runtime quarantine, and signed ordered database replay are executable. Add the external directive publisher/archive, restricted Kubernetes Job templates/runbook, alert rules, restore drill, and production security review before enabling an erasure credential.

Database completion is reportable only after the global tombstone commits; a cell tombstone alone is explicitly not completion. Product-level physical-erasure claims remain gated on step 5 for every external store enabled by that environment and on the restore-replay readiness gate in step 6.
