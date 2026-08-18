# Fair Runner Control Plane

- Status: Kubernetes launch and terminal reconciliation executable; invocation broker, cancellation, manifests, and applied evidence pending
- Product: Infinite Ocean: Spyglass
- Parent: [Pooled Kubernetes and Cell Architecture](kubernetes-topology.md)

## Decision

Spyglass runs bounded asynchronous invocations in a shared cell fleet. It does not keep an application container alive for each customer. An admitted invocation becomes one durable, identifier-only queue record; a cell runner controller fairly selects it and eventually creates one hardened, ephemeral Kubernetes Job.

The scheduling policy is independent of Kubernetes. PostgreSQL owns durable admission, Account fairness, concurrency, launch leases, retries, and terminal state. A launcher adapter owns idempotent interaction with the cluster. This keeps business policy testable without a cluster and permits another execution substrate without rewriting admission semantics.

The `spyglass runner-controller` process now performs durable fair claims, idempotent Kubernetes Job creation, due-time terminal inspection, and exact capacity release. It intentionally has no reference Deployment yet: the production invocation broker, runner workload identity, environment-specific Kubernetes API egress, cancellation path, and verified runner image are not complete, so promoting a manifest would create an attractive but incomplete execution surface.

## Control record and payload boundary

`spyglass.runner_invocation_queue` contains only:

- invocation UUID;
- Account UUID;
- fixed execution-profile code;
- processing state and attempt count;
- controller lease UUID and expiry;
- deterministic Kubernetes Job name after launch;
- bounded machine error code and lifecycle timestamps.

It must never contain a prompt, tool input, model output, connector secret, browser credential, provider token, customer file, arbitrary image, arbitrary command, or customer-selected environment variable. Invocation payloads belong behind a separate authenticated broker/object boundary and are resolved by the runner using only its invocation identity.

The runner pod receives no cell/global database credential and no Kubernetes API token. Its eventual broker credential is workload-scoped, short-lived, invocation-bound, and unable to select another Account or invocation.

## State machine

```mermaid
stateDiagram-v2
    [*] --> queued
    queued --> launching: fair claim + lease
    failed --> launching: retry becomes due
    launching --> launching: expired controller lease reclaimed
    launching --> launched: idempotent Job confirmed
    launching --> failed: launch failed below retry ceiling
    launching --> dead_letter: launch contract invalid or retry ceiling reached
    launched --> completed: Job succeeded
    launched --> execution_failed: Job failed
    launched --> canceled: authorized cancellation
```

`failed` means the controller could not establish a Kubernetes Job and may retry. `execution_failed` means the launched workload reached a terminal unsuccessful outcome. Those meanings are intentionally distinct.

Every `launching` row has a lease and no Job name. Every `launched` or execution-terminal row has a Job name and no controller lease. Retryable rows have a due time. A launched row has a durable next-inspection time; controller replicas claim bounded due pages with `FOR UPDATE SKIP LOCKED` and advance that time before calling Kubernetes. This prevents a page of long-running Jobs from starving newer terminal Jobs and avoids every replica polling every Job. Execution-terminal rows have a completion time and no next inspection. Database constraints reject partial combinations.

## Fair scheduling

Each Account has one `runner_account_scheduling` row:

```text
weight
concurrency_limit
active_count
virtual_finish
last_dispatched_at
```

For a fresh claim, one transaction:

1. Selects an Account that has due work and available concurrency.
2. Orders eligible Accounts by virtual finish, then oldest queued work, then immutable Account ID.
3. Locks one Account with `FOR UPDATE SKIP LOCKED` so controller replicas can claim other Accounts concurrently.
4. Locks that Account's oldest due invocation.
5. Moves it to `launching`, assigns a random lease, and increments its attempt.
6. Increments Account active count and advances virtual finish by `1 / weight`.

An Account at its concurrency limit is not eligible even if it owns the oldest global invocation. This prevents one noisy Account from occupying every slot. Weight changes relative long-run share; concurrency remains the hard per-Account ceiling. A policy cannot be lowered below current active usage because the database invariant rejects it; operators must drain first.

Expired `launching` records are reclaimed before fresh work. Reclaim changes the lease and attempt but does not increment Account active count or virtual finish again. The launcher must use a deterministic Job name and treat an already-matching Job as success, which closes the crash window after Kubernetes accepted a Job but before PostgreSQL recorded `launched`.

## Capacity lifecycle

Active count is incremented exactly once on the first fresh claim. It is decremented in the same transaction that records:

- a retryable or dead-letter launch failure; or
- `completed`, `execution_failed`, or `canceled` after launch.

Completion binds both invocation UUID and Job name. A repeated identical completion is idempotent. A stale lease, different Job name, or conflicting terminal outcome fails closed. The PostgreSQL contract proves expired-lease reclaim does not double-count capacity and that a stale controller cannot fail or launch the reclaimed invocation.

The Kubernetes reconciler treats missing or ambiguous Jobs as nonterminal and keeps inspecting them; it never silently releases capacity. Successful and failed Job conditions become `completed` and `execution_failed` respectively. Inspection verifies the immutable invocation, profile, and launch-contract digest before trusting a Job condition.

## Database authority

The queue is a technical cross-Account control plane and is intentionally not protected by Account RLS. Its dedicated controller role receives only:

```sql
GRANT USAGE ON SCHEMA spyglass TO spyglass_runner_controller;
GRANT SELECT, UPDATE ON spyglass.runner_account_scheduling TO spyglass_runner_controller;
GRANT SELECT, UPDATE ON spyglass.runner_invocation_queue TO spyglass_runner_controller;
```

It receives no `INSERT` authority and therefore cannot manufacture runnable work. It also receives no access to Account namespaces, Work, Agents, prompts, files, entitlements, Users, sessions, Billing, or provider credentials. The integration contract runs claim/lifecycle operations through this non-owner role and proves direct reads of Account namespaces and Work, and direct queue insertion, fail.

The producer role has no table grants. It can execute only `spyglass_configure_runner_account` and `spyglass_enqueue_runner_invocation`. Those security-definer functions validate bounded input, set the exact forced-RLS Account context, require an existing Account namespace, reject enqueue unless its state is `active`, and preserve invocation identity idempotency. The application must still invoke enqueue only after current Membership, package entitlement, capability, budget, and operation-id checks. Serving APIs receive neither controller table authority nor a general cross-Account read surface.

## Account erasure and restore

Runner control is part of cell Account erasure policy from its first migration:

- readiness and execution reject queued, launching, launched, retryable-failed, or dead-letter invocations;
- completed, execution-failed, and canceled records may be erased;
- invocation and scheduling rows are removed before the Account namespace;
- content-free row counts are merged into the immutable tombstone before its restore-ledger root is computed;
- restore replay removes restored runner-control rows before recreating the same checkpoint.

The original erasure functions remain under internal names because applied migrations are immutable. Production grants assign both the public wrapper and internal implementation to the same `NOLOGIN NOBYPASSRLS` erasure function role; only the public wrapper is executable by the short-lived operator role.

## Kubernetes adapter contract

The executable adapter enforces these rules:

- Job name is derived only from invocation UUID.
- Digest-pinned image, command, profile-to-resources mapping, sandbox RuntimeClass, service account, deadline, and retention are operator configuration, never invocation input.
- An HTTP `409 AlreadyExists` is success only after the existing Job's immutable invocation identity matches.
- Pods run as UID/GID 65532 with a read-only root filesystem, RuntimeDefault seccomp, no privilege escalation, all Linux capabilities dropped, bounded CPU/memory/ephemeral-storage/time, an operator-selected sandbox RuntimeClass, and no host namespaces or volumes. The RuntimeClass/admission policy must supply the tested PID and stronger sandbox boundary.
- Runner service accounts have `automountServiceAccountToken: false` and no RBAC.
- The implemented API client needs only `create` and `get` on Jobs in its cell namespace. Cancellation will add exact `delete`; it must never gain Secret reads, pod exec, or wildcard RBAC.
- NetworkPolicy denies all by default and allows only DNS, the invocation broker, approved model/tool egress gateways, and telemetry as required by the fixed profile.
- The controller observes Job conditions and records one terminal outcome; missing or ambiguous Jobs remain visible and reconcilable rather than silently releasing capacity.

The controller process is horizontally safe. PostgreSQL `SKIP LOCKED`, launch leases, durable inspection due-times, deterministic Job names, launch-contract hashes, and idempotent terminal transitions—not leader-local memory—coordinate replicas.

## Scaling and observability

The existing stats contract exposes bounded counts for ready, launching, launched, retryable-failed, and dead-letter invocations plus oldest-ready age. Production metrics must add launch latency, run duration, terminal outcome, reclaim count, admission denial reason, and Account concurrency saturation. Account identity must be omitted or represented by a controlled low-cardinality hash.

Event-driven scaling should use ready count and oldest-ready age. Controller replicas scale only within database connection and Kubernetes API budgets. Cluster autoscaling reacts to pending runner Jobs whose fixed profiles have valid resource requests. Queue depth alone never overrides Account concurrency, package limits, global safety ceilings, or provider budgets.

## Acceptance gates

- PostgreSQL 17 migration replay and checksum drift checks pass.
- Non-owner role cannot read customer tables.
- Same invocation identity is idempotent; a conflicting Account/profile is rejected.
- One Account at concurrency limit cannot block another eligible Account.
- Weighted share converges under sustained multi-Account backlog.
- Parallel controllers never exceed Account concurrency.
- Controller crash before and after Kubernetes acceptance converges to one Job.
- Launch retry/dead-letter and every execution-terminal path release capacity exactly once.
- Account erasure blocks unfinished runs and removes all terminal control records.
- Applied-cluster tests prove RBAC, NetworkPolicy, pod hardening, cancellation, node loss, API timeout, controller rollout, queue-driven scale-up, and cleanup.
- A many-small-Accounts plus one-hot-Account load test meets the admitted-start SLO without database or Kubernetes API saturation.
