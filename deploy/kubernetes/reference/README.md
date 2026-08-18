# Kubernetes reference topology

These manifests encode Phase 2 workload and security defaults for review. They
are intentionally not a deployable environment yet: release automation must
replace `registry.invalid/...:release-placeholder`, inject managed secret
references and provide environment-specific network/database destinations before promotion. The
account-api, app-router, cell app-api, private admission-api, per-cell route-receipt
worker, billing-worker, notification-worker, entitlement-worker, Account lifecycle worker, Work reconciler, Agent dispatch/projection workers, runner controller/broker, and model gateway arguments are executable today. The render includes narrow Job/identity RBAC, internal runner/broker/gateway NetworkPolicies, workload-specific service accounts, disruption budgets, and backlog-oriented HPA contracts. It still fails closed as an applied environment until overlays supply the cluster-specific Kubernetes API and managed-service egress, sandbox RuntimeClass, certificates, database roles, provider policy, metrics adapter, and digest-pinned images.

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
- A custom/external metrics adapter exporting the exact referenced
  `spyglass_agent_dispatch_ready`, `spyglass_agent_projection_ready`, and
  `spyglass_runner_ready` metrics from each worker's no-store `/metrics`
  endpoint, plus request latency, oldest queue age, and
  schedule-to-start signals. Missing external metrics must alert; environments
  may not silently treat CPU as sufficient proof that backlogs are healthy.
- Pod/service monitors scraping each HTTP service's `/metrics` endpoint for
  bounded-cardinality request counts, in-flight work, and duration summaries.
  The application exports registered route templates rather than raw customer
  paths; environment dashboards and relabeling must preserve that constraint.
- Tested NetworkPolicy egress destinations and cluster admission policy. The
  base permits only DNS and the explicit in-namespace runner→broker,
  broker→model-gateway, broker→tool-router, router→cell, and cell→admission
  paths. Each overlay must add exact database endpoints, the cluster API CIDR
  for controller/broker only, OpenAI/provider HTTPS for model-gateway only,
  and approved observability/secret-controller paths. Runners receive no
  general internal or internet egress.
- Pod monitor, alerts, SLO metadata, and a load-tested replica/connection cap.
- An ingress/L7 load-balancer probe that removes an unready router endpoint and
  a pod/node-loss exercise proving the next request reaches another ready
  replica without session affinity or customer-visible state loss.
- `spyglass-global-runtime`, `spyglass-cell-reference-runtime`,
  `spyglass-work-reconciler-restore-checkpoints`,
  `spyglass-account-api-secrets`, `spyglass-app-router-secrets`,
  `spyglass-app-api-secrets`, `spyglass-admission-api-secrets`,
  `spyglass-billing-worker-secrets`, `spyglass-notification-worker-secrets`,
  `spyglass-entitlement-worker-secrets`, `spyglass-account-lifecycle-worker-secrets`, `spyglass-route-receipt-worker-cell-reference-secrets`,
  `spyglass-work-reconciler-secrets`, `spyglass-agent-dispatch-worker-cell-reference-secrets`,
  `spyglass-agent-projection-worker-cell-reference-secrets`,
  `spyglass-runner-controller-cell-reference-secrets`,
  `spyglass-runner-broker-cell-reference-secrets`, and
  `spyglass-model-gateway-secrets` objects from environment configuration
  and secret controllers; they are not committed here. Workload-specific
  Secrets prevent each worker from receiving webhook, Stripe, or SMTP
  credentials it does not use. The entitlement worker receives only a
  constrained global-database credential.
- The Agent dispatcher and projector secrets each carry a distinct constrained
  cell credential and the runtime runner-envelope keyring. The dispatcher can
  read only immutable forced-RLS planning tables and call the dispatch plus
  encrypted-provision functions. The projector has execute-only projection
  authority and no direct customer-table read. Neither receives a provider
  credential, Kubernetes credential, browser signing key, Stripe key, or SMTP
  secret.
- `runner-rbac.yaml` grants the controller only Job `create/get/delete` inside
  the dedicated `spyglass-runners-reference` namespace, and the broker only
  TokenReview creation plus Pod/Job `get` in that namespace; neither can list,
  watch, patch, exec, read Secrets, or impersonate. The `runner` ServiceAccount
  exists only in the runner namespace, has no RBAC, and has no ordinary token
  mount. Each generated Job explicitly projects a ten-minute token whose
  audience is the exact HTTPS broker URL. Namespace-local default denial and a
  single cross-namespace runner→broker path keep a compromised runner away
  from application workloads and unrelated operator Jobs.
- The runner broker secret carries its constrained cell database credential,
  envelope keyring, and tool-context signing key. The model gateway alone
  receives the OpenAI/provider credential. A certificate controller supplies
  `spyglass-runner-broker-cell-reference-workload-tls` and
  `spyglass-model-gateway-workload-tls`; the broker certificate is also a
  client identity accepted by model-gateway. `spyglass-runner-broker-ca`
  exposes only `ca.crt` to ephemeral Jobs. No private key is mounted into a
  runner.
- Environments must define the `spyglass-sandboxed` RuntimeClass using their
  selected isolation technology (for example gVisor or Kata) and verify its
  node/runtime configuration. The reference does not manufacture a generic
  RuntimeClass whose handler might not exist.
- The global and per-cell runtime ConfigMaps pin
  `SPYGLASS_ERASURE_CHECKPOINT_SEQUENCE` and
  `SPYGLASS_ERASURE_CHECKPOINT_ROOT`. Sequence `0` uses 64 hexadecimal zeroes.
  After an erasure, release automation publishes the newest externally
  archived sequence/root without removing earlier database ledger entries. The
  Work reconciler checkpoint ConfigMap carries both
  `SPYGLASS_GLOBAL_ERASURE_CHECKPOINT_*` and
  `SPYGLASS_CELL_ERASURE_CHECKPOINT_*`. Serving roles receive `SELECT` on only
  their restore-ledger table. A missing checkpoint prevents startup and makes
  non-liveness endpoints unavailable; workers terminate if the checkpoint
  later disappears.
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
- The account API secret supplies independent `SPYGLASS_NETWORK_ACTOR_KEY`,
  `SPYGLASS_PASSKEY_ENCRYPTION_KEYS`, and
  `SPYGLASS_PASSKEY_ENCRYPTION_ACTIVE_VERSION` values. The environment ConfigMap supplies
  `SPYGLASS_PASSKEY_RP_ID` plus only the exact ingress/load-balancer CIDRs through
  `SPYGLASS_TRUSTED_PROXY_CIDRS`. Leaving the CIDR list empty safely ignores
  forwarding headers.

Run `kubectl kustomize deploy/kubernetes/reference` as a structural render
check. Do not apply the output to a cluster.

Catalog administration is intentionally not a standing Deployment. Environments
run `spyglass catalog-admin <action>` as a short-lived, human-authorized Job with
its own restricted database credential, operator identity, reason, and reviewed
input. It must not inherit any serving, webhook, Stripe secret, or SMTP secret.

Passkey key rotation is also absent as a standing Deployment. Environments run
`spyglass passkey-admin inspect|reencrypt` as a short-lived Job with the reviewed
active-plus-retained keyring, exact environment confirmation, and a database
role limited to encrypted passkey columns and immutable aggregate operator
events. See [passkey-key-rotation.md](../../../docs/production/passkey-key-rotation.md).

Work release administration is also intentionally absent as a standing
Deployment. Environments run `spyglass work-release-admin inspect|requeue` as a
short-lived, human-authorized Job with the target cell's execute-only operator
credential, operator identity/reason, and exact environment confirmation. It
must not inherit the reconciler's global credential or any serving secret. See
[work-release-operations.md](../../../docs/production/work-release-operations.md).

Agent queue administration is likewise absent as a standing Deployment.
Environments run `spyglass agent-queue-admin inspect|requeue` as a short-lived,
human-authorized Job with one target cell's execute-only operator credential,
an exact `dispatch` or `projection` queue, and exact environment confirmation.
It must not inherit envelope keys, provider credentials, worker credentials, or
serving secrets. See
[agent-queue-operations.md](../../../docs/production/agent-queue-operations.md).

Account erasure administration is intentionally absent as a standing
Deployment. Environments run `spyglass account-erasure-admin
prepare|inspect|approve|cancel|execute` as short-lived, human-authorized Jobs with
separate execute-only global and target-cell credentials, operator evidence,
and exact environment confirmation. The preparation Job also repeats the
Account UUID, policy/export evidence, and backup-expiry deadline. Leased
`execute` uses distinct execute-only global and cell roles; no standing
workload receives those authorities. Restored environments use a distinct
`restore-replay` Job with replay-only global/cell roles, an externally archived
signed directive file, a separate verification key, and exact Account/cell/
environment confirmation. It is never added to a Deployment and never selects
a fallback cell. See
[account-erasure.md](../../../docs/production/account-erasure.md).

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

The Account lifecycle worker is also shared and horizontally scalable. Its
PostgreSQL `SKIP LOCKED` leases ensure one replica evaluates each due closure
attempt. It receives only a constrained global-database credential; it does not
receive Stripe API credentials because billing eligibility is evaluated from
the verified local subscription projection. Account access freezes at request
time, owners retain a global cancellation path during cooling-off, and this
worker performs logical closure only. Physical erasure after the retention
deadline remains a separate operator-governed workflow.
