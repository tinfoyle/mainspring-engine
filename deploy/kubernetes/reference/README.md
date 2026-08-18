# Kubernetes reference topology

These manifests encode Phase 2 workload and security defaults for review. They
are intentionally not a deployable environment yet: release automation must
replace `registry.invalid/...:release-placeholder`, inject managed secret
references and provide environment-specific network/database destinations before promotion. The
account-api, app-router, cell app-api, private admission-api, per-cell route-receipt
worker, billing-worker, notification-worker, entitlement-worker, and Work reconciler arguments are executable today.

The reference proves the intended unit of scaling: shared workload classes in
a cell. Nothing here creates a Deployment, Service, namespace, database, or
credential per Spyglass Account.

The app-router reference keeps at least two replicas, requires at least two
eligible node domains with a hard max-skew policy, permits no unavailable pod
during a rolling update, waits for a new
pod to remain ready, and retains one ready replica during voluntary disruption.
Its startup probe allows dependency initialization before liveness enforcement.
Environment overlays must provide enough nodes and capacity to satisfy these
constraints; weakening them is an explicit availability-policy change.

Before an environment overlay may use these resources it must add:

- A pinned image digest produced by the verified release workflow.
- External Secrets or certificate controller integration; never literal Secret values.
- Cell-specific database, object-store, Temporal, and queue references.
- Ingress/WAF, certificate, DNS, and trusted route-signing configuration. The
  edge must send the more-specific
  `/api/v1/accounts/{accountID}/work-items...` path family to `app-router` and
  private HTML/global control routes to `account-api`; it must never route a
  browser directly to cell `app-api`.
- HPA custom metrics for request latency, queue age, and schedule-to-start.
- Tested NetworkPolicy egress destinations and cluster admission policy.
- Pod monitor, alerts, SLO metadata, and a load-tested replica/connection cap.
- An ingress/L7 load-balancer probe that removes an unready router endpoint and
  a pod/node-loss exercise proving the next request reaches another ready
  replica without session affinity or customer-visible state loss.
- `spyglass-global-runtime`, `spyglass-cell-reference-runtime`,
  `spyglass-account-api-secrets`, `spyglass-app-router-secrets`,
  `spyglass-app-api-secrets`, `spyglass-admission-api-secrets`,
  `spyglass-billing-worker-secrets`, `spyglass-notification-worker-secrets`,
  `spyglass-entitlement-worker-secrets`, `spyglass-route-receipt-worker-cell-reference-secrets`, and
  `spyglass-work-reconciler-secrets` objects from environment configuration
  and secret controllers; they are not committed here. Workload-specific
  Secrets prevent each worker from receiving webhook, Stripe, or SMTP
  credentials it does not use. The entitlement worker receives only a
  constrained global-database credential.
- The route-receipt worker secret supplies one narrow cell credential. It can
  lease the identifier-only cleanup queue and use `SELECT/UPDATE/DELETE` on the
  forced-RLS receipt table after setting transaction-local Account scope. It
  cannot read Work or global data and has no `BYPASSRLS`. Replicas coordinate
  with expiring leases and schedule versions; scale them against ready count and
  oldest-due age within the cell connection budget.
- The Work reconciler secret supplies distinct `SPYGLASS_CELL_DATABASE_URL` and
  `SPYGLASS_GLOBAL_DATABASE_URL` credentials. The cell credential can lease the
  identifier-only release outbox, prune expired completed jobs, and enter Account-scoped Work transactions;
  the global credential can only read Account existence and release usage
  reservations/counters. Neither credential is suitable for app-api.
- The admission-api secret supplies a narrow global credential plus the route
  verification keyring. It can read the Account access projection, execute the
  entitlement-version lock function, and mutate only usage counters and
  reservations. App-api reaches it with a signed routed-operation proof and
  retains only its cell credential. TLS 1.3 and an exact per-cell app-api SPIFFE
  identity protect the broker before route-proof verification.
- The app-router secret supplies one active route-signing key and the cell API
  secret supplies the active plus retained verification keys during rotation.
  The router receives only the global database credential; the cell API
  receives only its cell database credential. Operators populate each cell's
  exact internal HTTPS `route_origin` in the global registry before Account
  placement; route endpoints are no longer copied into every router pod.
- A certificate controller or secret synchronizer supplies
  `spyglass-app-router-workload-tls`, the cell-specific
  `spyglass-app-api-cell-reference-workload-tls`, and
  `spyglass-admission-api-workload-tls`, each with `tls.crt`, `tls.key`, and
  `ca.crt`. Server certificates contain the exact Service DNS SAN; client
  certificates contain the configured SPIFFE URI and appropriate extended key
  usage. Files rotate in place and are reloaded on new connections. No private
  key or certificate payload is committed in this reference.
- The account API secret supplies `SPYGLASS_NETWORK_ACTOR_KEY`; the environment
  ConfigMap supplies only the exact ingress/load-balancer CIDRs through
  `SPYGLASS_TRUSTED_PROXY_CIDRS`. Leaving the CIDR list empty safely ignores
  forwarding headers.

Run `kubectl kustomize deploy/kubernetes/reference` as a structural render
check. Do not apply the output to a cluster.

Catalog administration is intentionally not a standing Deployment. Environments
run `spyglass catalog-admin <action>` as a short-lived, human-authorized Job with
its own restricted database credential, operator identity, reason, and reviewed
input. It must not inherit any serving, webhook, Stripe secret, or SMTP secret.

Work release administration is also intentionally absent as a standing
Deployment. Environments run `spyglass work-release-admin inspect|requeue` as a
short-lived, human-authorized Job with the target cell's execute-only operator
credential, operator identity/reason, and exact environment confirmation. It
must not inherit the reconciler's global credential or any serving secret. See
[work-release-operations.md](../../../docs/production/work-release-operations.md).

Route rotation canaries are intentionally absent as a standing Deployment.
Environments run `spyglass route-canary` as a short-lived reviewed Job using a
dedicated internal canary Account, a candidate signing-key secret, and either a
candidate app-router certificate for a cell target or candidate app-api
certificate for admission. The Job receives no database credential and records
no customer identifier or secret. See
[route-rotation-operations.md](../../../docs/production/route-rotation-operations.md).

The entitlement worker is a shared control-plane workload, not one pod or
container per customer. Replicas coordinate bounded rollout seeding and Account
claims through PostgreSQL leases. Scale it against oldest queue age and backlog,
while keeping the replica count multiplied by `SPYGLASS_MAX_DATABASE_CONNS`
inside the database connection budget.
