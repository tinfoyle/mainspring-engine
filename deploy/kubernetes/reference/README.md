# Kubernetes reference topology

These manifests encode Phase 2 workload and security defaults for review. They
are intentionally not a deployable environment yet: release automation must
replace `registry.invalid/...:release-placeholder`, inject managed secret
references and provide environment-specific network/database destinations before promotion. The
account-api, app-router, cell app-api, billing-worker, notification-worker, and entitlement-worker arguments are executable today.

The reference proves the intended unit of scaling: shared workload classes in
a cell. Nothing here creates a Deployment, Service, namespace, database, or
credential per Spyglass Account.

Before an environment overlay may use these resources it must add:

- A pinned image digest produced by the verified release workflow.
- External Secrets or workload identity; never literal Secret values.
- Cell-specific database, object-store, Temporal, and queue references.
- Ingress/WAF, certificate, DNS, and trusted route-signing configuration.
- HPA custom metrics for request latency, queue age, and schedule-to-start.
- Tested NetworkPolicy egress destinations and cluster admission policy.
- Pod monitor, alerts, SLO metadata, and a load-tested replica/connection cap.
- `spyglass-global-runtime`, `spyglass-cell-reference-runtime`,
  `spyglass-account-api-secrets`, `spyglass-app-router-secrets`,
  `spyglass-app-api-secrets`,
  `spyglass-billing-worker-secrets`, and
  `spyglass-notification-worker-secrets`, and
  `spyglass-entitlement-worker-secrets` objects from environment configuration
  and secret controllers; they are not committed here. Workload-specific
  Secrets prevent each worker from receiving webhook, Stripe, or SMTP
  credentials it does not use. The entitlement worker receives only a
  constrained global-database credential.
- The app-router secret supplies one active route-signing key and the cell API
  secret supplies the active plus retained verification keys during rotation.
  The router receives only the global database credential; the cell API
  receives only its cell database credential.
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

The entitlement worker is a shared control-plane workload, not one pod or
container per customer. Replicas coordinate bounded rollout seeding and Account
claims through PostgreSQL leases. Scale it against oldest queue age and backlog,
while keeping the replica count multiplied by `SPYGLASS_MAX_DATABASE_CONNS`
inside the database connection budget.
