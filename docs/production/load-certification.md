# Account load and fairness certification

Status: executable read-only evidence tool; connected Kubernetes evidence remains a release gate

## Purpose

`cmd/load-cert` drives one bounded, reviewed, synthetic GET workload against an exact deployed Spyglass origin. It measures latency and errors by stable actor and route labels, including a deliberately hot Account beside many small Accounts, and writes an immutable content-free report.

This tool answers a narrow question: did every planned read complete at the requested rate, and did each actor/route group remain within the declared p95 and error-rate budgets? It does not by itself prove Kubernetes autoscaling, database headroom, asynchronous queue fairness, write idempotency, failover, or a 24-hour soak. Those require coincident platform evidence and separate exercises described below.

## Safety boundary

The command is deliberately less flexible than a general load generator:

- It sends only `GET`; it cannot create, change, or delete customer state.
- The target must be one exact HTTPS origin. User information, paths, queries, or fragments are forbidden in `origin`, and redirects are not followed.
- Plans have strict JSON decoding and reject unknown fields or trailing values.
- Rates, duration, concurrency, actor/route counts, weights, response size, and request timeout are bounded.
- Session cookies and synthetic Account UUIDs come only from narrowly named process environment variables. They are never accepted as command arguments or included in the plan.
- Account routes may contain the one supported `{account_id}` placeholder. The value is validated as a UUID and substituted immediately before the request.
- Every actor in an Account-scoped plan must resolve to a distinct Account UUID. This prevents a supposed many-Account run from silently exercising one Account repeatedly.
- Reports contain no origin, route path, query, cookie, Account UUID, response body, or raw transport error. Route and actor names must be pre-reviewed machine-safe labels.
- Response bodies are read only to enforce a 1 MiB ceiling and then discarded.
- Output files are created with mode `0600` and `O_EXCL`; an existing evidence file is never overwritten.

Use only dedicated synthetic Accounts containing generated non-customer data. Never point this command at an Account that contains production customer content, even though the report is content-free.

## Plan contract

| Field | Meaning and bound |
|---|---|
| `schema_version` | Exactly `1` |
| `environment` | Stable evidence label such as `staging-us-east`; 2-48 lowercase letters, digits, or hyphens |
| `revision` | Exact 40-character lowercase Git revision asserted by the reviewed deployment record |
| `image_digest` | Exact `sha256:` registry digest asserted by the reviewed deployment record |
| `origin` | Exact HTTPS application origin, with no path, credentials, query, or fragment |
| `warmup_seconds` | `0-60`; discarded samples used to establish connections and caches |
| `duration_seconds` | `10-900`; measured window |
| `requests_per_second` | `1-1000`; aggregate target rate, not a per-actor rate |
| `concurrency` | `1-500`; maximum simultaneous client workers |
| `max_p95_ms` | `1-60000`; applied independently to every actor/route group |
| `max_error_rate` | `0-0.25`; applied independently to every actor/route group |
| `actors` | `1-500` weighted synthetic identities |
| `routes` | `1-20` weighted read routes |

Actor and route weights are integers from 1 through 100. Actor and route weight sums are each limited to 1,000, their product is limited to 10,000, and the measured request count must cover at least one complete weighted actor-by-route cycle. The schedule is deterministic: a weight of 20 for `hot-account` and 1 for each small Account gives the hot Account twenty times the request share of each small Account.

`cookie_env` must match `SPYGLASS_LOAD_COOKIE_*`. `account_id_env` must match `SPYGLASS_LOAD_ACCOUNT_ID_*`. An Account-scoped route requires a unique `account_id_env` on every actor; a global/public route plan must not declare unused Account identifiers.

Allowed expected response media types are `application/json`, `application/problem+json`, and `text/html`. A mismatched status, media type, oversized body, body read failure, or transport failure is a bounded error category, never a copied response or error string.

## Example reviewed plan

This valid smoke plan demonstrates one hot and two small Accounts. A release profile should enumerate the approved representative Account population rather than inserting ellipses or generating actors at execution time.

```json
{
  "schema_version": 1,
  "environment": "staging-us-east",
  "revision": "0123456789abcdef0123456789abcdef01234567",
  "image_digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "origin": "https://app.staging.infiniteocean.net",
  "warmup_seconds": 30,
  "duration_seconds": 300,
  "requests_per_second": 200,
  "concurrency": 100,
  "max_p95_ms": 300,
  "max_error_rate": 0.005,
  "actors": [
    {
      "name": "hot-account",
      "weight": 20,
      "cookie_env": "SPYGLASS_LOAD_COOKIE_HOT",
      "account_id_env": "SPYGLASS_LOAD_ACCOUNT_ID_HOT"
    },
    {
      "name": "small-account-001",
      "weight": 1,
      "cookie_env": "SPYGLASS_LOAD_COOKIE_SMALL_001",
      "account_id_env": "SPYGLASS_LOAD_ACCOUNT_ID_SMALL_001"
    },
    {
      "name": "small-account-002",
      "weight": 1,
      "cookie_env": "SPYGLASS_LOAD_COOKIE_SMALL_002",
      "account_id_env": "SPYGLASS_LOAD_ACCOUNT_ID_SMALL_002"
    }
  ],
  "routes": [
    {
      "name": "account-context",
      "path": "/api/v1/accounts/{account_id}/context",
      "weight": 2,
      "expected_status": 200,
      "expected_content_type": "application/json"
    },
    {
      "name": "work-summary",
      "path": "/api/v1/accounts/{account_id}/work-items/summary",
      "weight": 1,
      "expected_status": 200,
      "expected_content_type": "application/json"
    }
  ]
}
```

Keep the reviewed plan in the restricted release record. The emitted report contains only its SHA-256 digest. This binds the result to the reviewed origin, routes, labels, weights, thresholds, revision, and image assertion without copying those operational details into the portable evidence.

## Preparation

1. Deploy one signed digest through the normal cohort process. Record the Git revision, multi-architecture manifest digest, deployment/overlay revision, environment, and cell cohort.
2. Verify the running workloads use that digest through the deployment system and admission evidence. `load-cert` records the asserted identity; it does not trust an application response to discover or override deployment identity.
3. Provision synthetic Accounts in the intended cells with representative generated row cardinality and package modes. Include small, medium, high-cardinality, and one deliberately hot Account. Normal signup must create no Account-specific Pod, Deployment, Service, namespace, database, credential, or load balancer.
4. Create least-privilege synthetic Users and sessions. A User may be a member of multiple synthetic Accounts, but every load actor must reference a distinct Account UUID.
5. Inject cookies and Account IDs into the load-runner process from the environment's secret manager. Do not print them, place them in the JSON plan, save them in shell history, or persist them in CI output.
6. Confirm monitoring retention and clock synchronization for the runner, routers, APIs, workers, database, Kubernetes control plane, and metrics backend.
7. Freeze unrelated deployments, migrations, reconciliations, and synthetic-data changes for the evidence window.
8. Record the approved latency, error, saturation, fairness, and scaling budgets before starting. A result is not certified against thresholds selected after observation.

## Execution

Build the command from the same reviewed source revision, or run it directly from that checkout:

```powershell
wsl.exe -e sh -lc 'go run ./cmd/load-cert -plan ./restricted/load-plan.json -output ./restricted/load-evidence.json'
```

Exit status is:

- `0` when all planned requests were scheduled and completed, every actor/route group was observed, and every group met both thresholds;
- `1` when valid execution produced failing evidence; or
- `2` for invalid input, credentials, or evidence-write failure.

The measured scheduler emits one request immediately and paces the remainder at the aggregate rate. If the scheduler cannot place the full workload before the bounded window, or the parent context is canceled, `scheduled_requests` is lower than `planned_requests` and the report fails. Workers drain scheduled requests under the HTTP timeout; `completed_requests` must also equal the planned count.

Run one profile at a time from a runner with enough CPU, network, file descriptors, and clock stability that the client is not the bottleneck. Use a new evidence filename for every attempt. A failed attempt remains evidence and must not be overwritten; fix the cause and create a separately named rerun.

## Required profiles

Use the same exact artifact for all release-candidate profiles:

1. **Baseline:** representative small Accounts at normal read mix and expected steady rate. Establish routing, TLS, session, database, and telemetry correctness.
2. **Hot Account plus many small Accounts:** one Account receives a substantially larger share while many low-weight Accounts continue. Compare each small Account's error and latency result and the server-side admission/throttling metrics; aggregate success can hide a noisy neighbor.
3. **Capacity step:** increase aggregate rate in reviewed steps and hold each step long enough to observe HPA stabilization, Pod readiness, connection-pool behavior, and cache effects. Each step gets its own plan and evidence file.
4. **Replica disruption:** repeat the representative profile while removing a router/API endpoint through the approved failure-injection procedure. Prove bounded same-Service recovery and no guessed cross-cell routing.
5. **Database/dependency degradation:** use the environment runbook to exercise managed failover and sustained admission-service degradation. This command supplies only the safe read traffic; the failure controller and recovery evidence are separate.
6. **Soak:** orchestrate successive immutable windows for at least 24 hours, with distinct output names and a manifest of report hashes. The command intentionally caps one window at 15 minutes so interruption and evidence loss are bounded.

Write and asynchronous runner fairness require separate content-free harnesses. Do not weaken this command into a mutation tool; write certification must own idempotency keys, canonical payloads, cleanup, ambiguous outcomes, and durable-state assertions explicitly.

## Reading the report

Top-level evidence includes the environment label, asserted Git revision and image digest, plan digest, UTC timestamps, load controls, planned/scheduled/completed counts, thresholds, overall success, and results.

Each `(actor, route)` result includes:

- completed sample and error counts;
- error rate;
- p50, p95, p99, and maximum whole-millisecond latency;
- bounded HTTP status counts;
- bounded error-category counts; and
- whether the group passed both configured thresholds.

Whole-millisecond observations can be zero for a fast response. Percentiles use the nearest-rank method. Groups are sorted by actor and route name for deterministic review. A missing group, partial request count, threshold miss, unexpected status/media type, or any transport/body error makes overall `success` false.

The report intentionally cannot explain a transport failure beyond `transport`, or identify a request beyond reviewed actor/route labels. Investigate with content-free request IDs and server telemetry captured for the same window; do not expand the evidence format to contain raw errors, paths, or bodies.

## Coincident cluster evidence

Archive the report beside time-aligned exports from the real staging environment. At minimum capture:

| Boundary | Required evidence |
|---|---|
| Edge/router/API | request rate, latency histogram, bounded error class, in-flight requests, replica count, readiness, restarts, CPU/memory throttling, and endpoint changes |
| Account fairness | admitted/denied work by controlled Account label, hot-Account concentration, per-Account concurrency ceilings, and small-Account latency/error comparison |
| PostgreSQL | active/max connections by workload role and cell, pool wait, transaction/query latency, locks, CPU, memory, IOPS, storage latency, and replica/failover state |
| Kubernetes | HPA desired/current replicas, source metric, scaling conditions/events, pending/unschedulable Pods, node/topology distribution, disruption-budget state, and rollout identity |
| Async workloads | queue depth/age, Temporal schedule-to-start where present, dispatch/projection/reconciliation backlog, dead letters, runner capacity, and provider ceiling |
| Release | Git revision, image index/platform digests, signature/provenance/SBOM verification, deployment overlay revision, cell cohort, and start/end change record |

Prefer bounded labels and controlled synthetic Account labels in metrics. Never export raw UUIDs into a broad metrics system. Record the query or dashboard revision and export timestamps so a later reviewer can reproduce the interpretation.

## Certification decision

A profile passes only when all of the following are true:

- the report is valid, immutable, bound to the reviewed plan digest, and has `success: true`;
- the deployment record proves the asserted signed image digest was serving the entire window;
- no isolation, wrong-cell, stale-placement, or cross-Account correctness signal fired;
- small-Account budgets remained satisfied during the hot-Account profile;
- HPA response stayed within the approved scaling-time budget without oscillation or unavailable capacity;
- database, connection-pool, provider, queue, and node headroom stayed within their predeclared ceilings;
- no Account-specific infrastructure was provisioned;
- disruption/failover behavior matched its runbook and other cells remained healthy; and
- the evidence bundle is reviewed and signed off by the release and service owners.

A passing JSON report with missing cluster telemetry is an incomplete certification, not a pass. Conversely, operator observation cannot override `success: false`; retain the failed run and execute a new reviewed plan after remediation.

## Evidence retention and cleanup

Hash the report immediately and add its filename, SHA-256, plan SHA-256, release identity, profile, monitoring export references, and reviewer decision to the restricted release manifest. Store the report and platform exports under the release evidence retention policy. Keep credential values and synthetic Account UUID mappings only in the environment secret store and restricted synthetic-data inventory, never in the portable report bundle.

After the last profile, revoke synthetic sessions, remove temporary runner secrets, end failure injection, restore ordinary scaling bounds, and confirm the cluster returned to steady state. Retain synthetic Accounts only when the test-data policy requires stable representative fixtures; otherwise erase them through the ordinary audited Account lifecycle.

## Current limit

The executable command provides the safe read driver and content-free client evidence. Spyglass is not load-certified until it has been run against an applied multi-replica staging cluster and the coincident metrics above have been archived. Pending companion evidence includes authenticated write/idempotency load, asynchronous runner fairness, managed database and admission degradation, sustained autoscaling, and the 24-hour soak.
