# OpenTelemetry tracing and observability operations

Status: executable content-safe OTLP/HTTP trace export; collector deployment, dashboards, alerts, burn-in, and game-day evidence remain environment gates

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

## Current limit

This implementation instruments HTTP serving and the principal routed/provider HTTP clients. PostgreSQL calls, worker lease loops, Kubernetes controller calls, runner-invocation exchange, SMTP, and detailed queue phase spans are not yet instrumented. No checked-in collector deployment, backend, dashboard, alert rule, staging burn-in, or game-day result exists. Consequently this closes the runtime export gap but does not close the production observability P0 by itself.
