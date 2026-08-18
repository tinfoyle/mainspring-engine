# ADR-0003: Pooled cell runtime

- Status: Accepted
- Date: 2026-08-18
- Owners: Infinite Ocean platform and security engineering

## Context

The prototype provisions an always-on containerized application boundary per customer. That model gives intuitive physical separation but scales infrastructure count, idle cost, connections, secrets, deployments, migrations, backups, and operational toil with customer count. A single unrestricted shared data plane would reduce that cost but create an unacceptable fleet-wide failure and data-isolation boundary.

## Decision

Spyglass uses **shared stateless deployments grouped by workload class** and **multi-Account data cells**.

- The horizontal scaling unit is a workload class: website, Account API, application router, cell application API, billing projection, workflow/activity worker, ingestion/indexing, connector worker, Agent dispatch/projection, model gateway, or runner controller.
- The data placement and failure-containment unit is a cell. One cell serves many Accounts through a cell-scoped database, workers, object/index partitions, and bounded connection pools.
- The customer boundary remains the Spyglass Account. Every customer-owned row, object, job, workflow, cache entry, capability, and audit event carries an immutable `account_id`.
- A global control plane owns Users, Accounts, Memberships, Catalog, Billing projections, Entitlements, and the Account Directory. Ordinary customer business records remain in the assigned cell.
- A trusted router resolves `account_id` to `cell_id`, signs short-lived Account and placement-generation context, and forwards to the cell. Clients never choose a cell.
- Cell requests set transaction-local Account context. PostgreSQL RLS, explicit repository predicates, and Account-scoped keys/foreign keys provide defense in depth.
- Stateless APIs scale on request concurrency, latency, and resource pressure. Workers scale on durable queue depth, oldest-ready age, and schedule-to-start latency. Replica maxima respect database, provider, and downstream capacity.
- Weighted fairness, per-Account/package admission, bounded concurrency, and global safety ceilings prevent a hot Account from monopolizing shared replicas.
- Sandboxed runner Jobs may be ephemeral per bounded invocation because their purpose is isolation of one task, not an always-on customer stack. They receive no customer database or Kubernetes authority.

Ordinary Account provisioning is logical and retry-safe: create records, choose a healthy cell, publish an initial entitlement snapshot, initialize required Account-scoped records/prefixes, and emit `account.ready`. It creates no Deployment, Pod, Service, namespace, database, load balancer, DNS record, or secret set.

Dedicated enterprise isolation is a placement policy, not a separate product architecture. A high-isolation Account may be assigned to a dedicated cell or database while using the same API contracts, domain code, images, and placement workflow.

## Consequences

- Infrastructure and idle cost scale with aggregate workload rather than raw Account count.
- API and worker workload classes can scale independently in Kubernetes.
- A cell bounds data size, connection count, rollout cohort, restore scope, and most data-plane failures.
- Strong multi-Account isolation becomes a non-negotiable application, database, queue, object, cache, and operations requirement.
- The platform needs capacity-aware placement, cell health, stale-generation rejection, fair scheduling, Account movement, and cell-cohort deployment tooling.
- Cells should be added before hard resource limits; autoscaling replicas cannot compensate for a saturated database or provider.

## Rejected alternatives

- **One permanent stack per Account.** Operational objects and idle cost grow linearly and make fleet-wide upgrades and recovery expensive.
- **One fleet-wide business database with no cell boundary.** It creates an unbounded blast radius and weakens placement, restore, residency, and cohort-rollout options.
- **Database per Account by default.** It moves the scaling problem into connections, migrations, backups, and managed-database count.
- **Use Kubernetes namespace as customer identity.** Runtime scheduling metadata is not a domain authorization boundary and does not cover queues, databases, objects, or external actions.

## Verification

- Signup tests assert that no customer-specific Kubernetes or database resource is created.
- Cross-Account attack suites cover API calls, SQL/RLS, joins and foreign keys, jobs, workflows, caches, object storage, indexes, exports, and operator paths.
- Many-small-Account and one-hot-Account load tests prove fairness, scale-up behavior, connection safety, and bounded queue age.
- Pod, node, zone, cell database, global control-plane, queue, and full-cell failure exercises demonstrate documented degraded behavior and no duplicate visible effects.
- Account-move rehearsals verify rows, objects, indexes, workflows, entitlements, placement generation, rollback, and source retention.
