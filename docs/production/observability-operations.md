# OpenTelemetry tracing and observability operations

Status: executable content-safe OTLP/HTTP trace export plus a binary-validated collector gateway, Prometheus rules, and Grafana overview reference; applied backend, scrape, paging, burn-in, and game-day evidence remain environment gates

## Runtime contract

Every long-running HTTP/worker mode and reviewed one-shot control mode can opt into one process-owned OpenTelemetry trace provider. HTTP servers continue valid W3C `traceparent` context, create server spans around registered `ServeMux` routes, and flush a bounded batch processor during graceful shutdown. Principal clients create child spans. Trace context propagates across Infinite Ocean-owned router-to-cell, cell-to-admission, broker-to-tool/model, and route-canary calls; Stripe/OpenAI calls are timed without disclosing trace context to those providers. The sandboxed `runner-invocation` mode rejects trace-export configuration because it deliberately receives neither a general workload certificate nor observability egress.

Tracing is absent when `SPYGLASS_OTEL_TRACES_ENDPOINT` is unset. Once that variable is present, configuration is fail-closed: an invalid release revision, endpoint, sample ratio, correlation key, or workload certificate prevents process startup. Export uses OTLP/HTTP protobuf with gzip to one exact HTTPS `/v1/traces` endpoint over the ordinary rotating Spyglass mTLS workload identity. Redirects, ambient OTLP headers, plain HTTP, endpoint credentials, alternate paths, queries, and fragments are refused.

The batch processor is deliberately bounded: 2,048 queued spans, 256 spans per export batch, two-second batching, five-second export timeout, one-MiB serialized request maximum, and at most ten seconds of exporter retry. Application requests do not wait for export and do not fail because a collector is unavailable. The collector and telemetry pipeline must alert on rejected or missing data; silently treating exporter loss as healthy is prohibited.

## Attribute and propagation policy

Allowed server-span attributes are:

| Attribute | Source |
|---|---|
| `service.name` | Static process mode |
| `service.version` | Linker-bound 40-character Git revision |
| `deployment.environment.name` | Reviewed environment label |
| `spyglass.cell.id` | Static cell deployment identity, or `global` |
| `http.request.method` | Standard bounded method or `OTHER` |
| `http.route` | Registered route template or `unmatched` |
| `http.response.status_code` | Numeric response status |
| `spyglass.account.ref` | First 128 bits of domain-separated HMAC-SHA-256 over the validated Account UUID with the environment trace-correlation key |

Client spans contain only bounded method and response status. They do not contain destination URLs, hosts, paths, query strings, request/response bodies, headers, provider identifiers, or errors. Failures use fixed status descriptions. Server spans never contain raw Account/User/Work/invocation UUIDs, cookies, email addresses, IP addresses, prompts, results, or panic/error strings.

Spyglass accepts only the W3C `traceparent` identity/parent/sample fields at owned service boundaries and discards inbound `tracestate`. It does not extract or inject baggage, and every outbound transport removes inherited baggage from its cloned request without mutating the caller request. External-provider transports additionally remove `traceparent` and `tracestate`. The configured ratio sampler is applied to remote roots regardless of the incoming sampled flag, so an Internet client cannot force trace volume by supplying a sampled `traceparent`. Local child spans preserve their parent decision; services using the same ratio make a deterministic decision from the shared trace ID.

The Account correlation key is a telemetry secret, not an encryption key or authorization credential. Use one random 32-byte value per environment, distribute it only to traced workloads, rotate it deliberately at a release boundary, and record the rotation because references before and after rotation cannot be joined. Never reuse `SPYGLASS_NETWORK_ACTOR_KEY`, route-signing keys, envelope keys, or provider credentials.

## Required environment values

| Variable | Requirement |
|---|---|
| `SPYGLASS_OTEL_TRACES_ENDPOINT` | Exact `https://host[:port]/v1/traces`; absence disables tracing |
| `SPYGLASS_OTEL_TRACE_SAMPLE_RATIO` | Explicit finite number greater than `0` and at most `1` |
| `SPYGLASS_TRACE_ACCOUNT_HASH_KEY` | Standard Base64 encoding of exactly 32 random bytes |
| `SPYGLASS_ENVIRONMENT` | Reviewed bounded environment label |
| `SPYGLASS_CELL_ID` | Static bounded cell label for cell workloads; omitted for global workloads |
| `SPYGLASS_WORKLOAD_CERT_FILE` | Rotating client certificate accepted by the collector |
| `SPYGLASS_WORKLOAD_KEY_FILE` | Private key readable only by the workload |
| `SPYGLASS_WORKLOAD_CA_FILE` | Trust bundle containing the collector server issuer |

The running binary must have a linker-bound 40-character revision. A local `go run` binary reports `unknown` and therefore cannot export release evidence accidentally.

## Environment topology

The production overlay must provide an authenticated collector or gateway inside an approved observability trust boundary. NetworkPolicy grants each traced workload egress only to that endpoint and DNS; the collector must not become a general proxy. Its certificate DNS identity must match the configured endpoint and its client trust must admit only the reviewed workload CA/SPIFFE population. Collector receivers, processors, exporters, storage, and query access need named owners and least-privilege credentials.

Do not send customer telemetry directly from application Pods to a third-party backend. Terminate workload mTLS at the environment collector, enforce the attribute allowlist there again, then export under a separate collector-owned credential. Configure retention, regional processing, deletion, access logging, and subprocessor treatment before customer traffic.

The checked-in reference now includes two disruption-protected `otel-collector` gateway replicas using the official OpenTelemetry Collector Contrib `0.159.0` multi-platform image pinned to digest `sha256:1f2c54a30e713fac6b3ae77a1ec84010c2007e29ced8ec666214fc2f6739c1cc`. Its configuration is validated by that exact binary in hosted CI. It accepts only one-MiB OTLP/HTTP requests over reloadable TLS 1.3 with a required, reloadable workload-client CA, drops spans containing links, validates bounded resource/span values, removes every non-allowlisted attribute, canonicalizes scope/span/event text, exposes only internal health metrics, and exports through a separate bounded 2,048-item queue over TLS 1.3. It has no Kubernetes API credential, writable root filesystem, debug/content exporter, or embedded backend credential.

The base deliberately cannot export in an applied cluster. An environment overlay must provide all of the following:

- `spyglass-otel-collector-ingress-tls` with the Service-DNS server certificate and private key;
- `spyglass-workload-client-ca` containing only the reviewed workload client issuer as `ca.crt`;
- `spyglass-observability-runtime.environment` as the bounded environment label and `backendEndpoint` as the reviewed HTTPS OTLP backend base URL;
- `spyglass-otel-collector-backend.authorization` and `ca.crt` as the collector-owned backend credential and trust bundle;
- exact collector-to-backend egress, while keeping the base default denial in force;
- `SPYGLASS_OTEL_TRACES_ENDPOINT=https://otel-collector.spyglass-reference.svc.cluster.local:4318/v1/traces` and the managed tracing values on traced workloads;
- a Prometheus Operator-compatible rule selector, authenticated scrape configuration for application metrics, paging receiver/routing policy, and import/provisioning of `deploy/observability/grafana/spyglass-overview.json`;
- retention, region, access-control, deletion, and subprocessor review for the chosen backend.

The `PrometheusRule` and dashboard are version-controlled product artifacts, not proof that an environment is monitoring anything. Promotion must record their content digests and prove the target Prometheus loaded every rule, the dashboard queries return the exact release series, and a synthetic firing alert reaches the named on-call receiver.

## Release acceptance

For the exact release artifact and target environment:

1. Deploy the collector and prove mTLS rejects no certificate, an expired certificate, an untrusted issuer, and the wrong workload identity.
2. Start each long-running mode with tracing enabled and prove its resource identity matches the release manifest, environment, and cell.
3. Execute anonymous, authenticated Account, routed Work, Stripe test, Agent runner, provider-failure, and wrong-Account journeys. Confirm their server/client topology joins on one trace ID where the boundary is instrumented.
4. Query exported data for raw synthetic UUIDs, email addresses, route values, query values, cookies, provider payloads, prompts, and deliberately injected baggage. Any match blocks promotion.
5. Disconnect and throttle the collector. Confirm requests continue, queues remain bounded, exporter loss alerts fire, and recovery does not create application latency or an unbounded retry storm.
6. Terminate Pods normally and under the configured grace period. Confirm shutdown flushes bounded pending spans and does not delay rollout beyond the budget.
7. Archive content-free trace IDs, query/dashboard revisions, collector configuration digest, alert results, release digest, and timestamps in the restricted release record.

## Dashboards and paging

Before staging burn-in, provide version-controlled dashboards for request rate/error/latency by service and route template; router-to-cell and cell-to-admission latency; Stripe/provider latency and failures; queue age/projection lag; database saturation; collector accepted/rejected spans; export failures; and trace absence by expected workload. Join controlled Account references only in restricted incident views.

Page on sustained customer SLO burn, isolation denials, billing projection lag, oldest runnable/dispatch/projection work, collector-wide trace loss, and restore-gate failures. Ticket lower urgency capacity trends, individual provider retries, and sampling gaps that do not affect the whole environment. Every alert needs an owner, severity, evaluation window, runbook link, and explicit recovery/verification step.

The reference `spyglass-production` rule group supplies recording rules for five-minute request rate, 5xx ratio, and p95 latency plus paging/ticket alerts for the currently executable HTTP, worker, and collector signals. The dashboard intentionally has no Account-reference variable or panel. Billing projection lag, isolation denial, database saturation, restore-gate, and expected-workload trace-absence signals still require dedicated runtime/environment metrics before their corresponding launch pages can be considered implemented.

## Alert response procedures

These are minimum response contracts. Environment runbooks may add provider-specific commands, but may not replace content-free evidence with customer payload inspection or direct queue/database mutation.

### HTTP error budget burn

1. Acknowledge the page and identify the static `service` label, affected release digest, first firing time, and whether all replicas/routes are affected.
2. Compare bounded route-template 5xx rates with deployment, dependency, database saturation, and collector timestamps. Do not query raw paths or bodies.
3. Stop the active rollout. If the failure began with the candidate and rollback safety gates remain satisfied, roll back to the last retained verified digest.
4. Verify the five-minute ratio remains below five percent for at least two evaluation windows and that no isolation or duplicate-effect signal appeared before resolving.

### HTTP tail latency

1. Confirm the p95 series is backed by current histogram buckets and meaningful request rate; a stale or absent scrape is a telemetry incident instead.
2. Compare replica CPU/memory, in-flight requests, database connections, queue age, and bounded provider latency. Preserve Account fairness and connection caps while scaling.
3. Stop or roll back a correlated release; otherwise mitigate the saturated dependency or add replicas within the reviewed limit.
4. Resolve only after p95 stays below one second for two evaluation windows and queued work is not growing.

### Runnable queue stall

1. Identify the static worker and field, confirm at least one ready replica, and compare ready, leased, retrying, dead-letter, and oldest-age gauges.
2. Stop rollouts and new discretionary load if age continues to increase. Do not bypass Account-fair claims or capacity fencing.
3. Use the queue-specific inspected operator command and immutable audit path when a poisoned record is suspected; never update queue rows directly.
4. Verify oldest age returns below the threshold, ready backlog drains, and no duplicate external effect or capacity leak occurred.

### Queue dead letter

1. Treat every sustained non-zero dead-letter gauge as a failed product operation, not routine backlog.
2. Use `agent-queue-admin` or `work-release-admin` with exact environment, queue, target, actor, and reason confirmation. Preserve the original immutable evidence.
3. Requeue only after the deterministic cause is corrected and reviewed. If the operation may have crossed an external-effect boundary, reconcile before retry.
4. Verify the target reaches its terminal state, the gauge returns to zero, and the audit batch is archived.

### Collector export loss

1. Determine whether enqueue failure, send failure, or queue utilization fired and whether both collector replicas are affected.
2. Check backend reachability, authorization expiry/rotation, CA validity, quota, and throttling through content-free collector health only.
3. Preserve the bounded queue and retry limits; do not remove the memory limiter, enable an unbounded queue, or add a debug exporter.
4. Verify queue utilization returns below 50 percent, failure counters stop increasing, accepted spans resume export, and application latency was unaffected.

### Collector refusal

1. Compare receiver versus processor refusal. Receiver refusal indicates payload/resource pressure; processor refusal normally means a workload violated the strict telemetry shape.
2. Identify the static workload certificate and release identity without recording customer payloads. Quarantine a workload that emits unexpected attributes, links, identifiers, or malformed values.
3. Correct instrumentation or capacity; never widen the allowlist during incident response.
4. Replay only synthetic validation traffic, then verify refusal counters stop increasing and the content scan remains clean.

### Missing telemetry

1. Distinguish a failed workload/collector from failed service discovery, network policy, certificate, or Prometheus ingestion.
2. Check target health and rule evaluation timestamps from the monitoring plane. Absence is not evidence of health.
3. Restore scraping/export or fail over the monitoring plane; stop promotion while critical service, worker, or collector signals are missing.
4. Verify at least two fresh scrapes, successful rule evaluation, and one synthetic alert delivery before resolving.

## Current limit

This implementation instruments HTTP serving and the principal routed/provider HTTP clients. PostgreSQL calls, worker lease loops, Kubernetes controller calls, runner-invocation exchange, SMTP, and detailed queue phase spans are not yet instrumented. A checked-in collector gateway, dashboard, and initial alert rules now exist and are syntax/component validated, but no overlay has supplied the backend, secrets, authenticated application scrape, remaining signals, paging route, applied policy evidence, staging burn-in, or game-day result. Consequently the repository deployment contract is substantially stronger, but the production observability P0 remains open.
