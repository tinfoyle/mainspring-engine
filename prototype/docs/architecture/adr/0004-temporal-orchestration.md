# ADR-0004: Use Temporal for durable orchestration

Status: Proposed  
Date: 2026-08-06

## Context

Boardrooms perform multi-step work that may span agent invocations, tool calls, scheduled waits, retries, and human approvals. The application must select turns and preserve progress through worker or host restarts. A message broker can deliver jobs but does not itself model the durable state and transitions of a boardroom.

## Decision

Use Temporal as the durable orchestration engine for:

- Boardroom runs and application-controlled turn selection
- Recurring schedules and manual runs
- Provisioning and tenant lifecycle workflows
- Human approval waits and signals
- Retries, timeouts, cancellation, and compensation
- Long-running integration operations

Use Temporal's Go SDK. Boardroom workflow code remains deterministic; model calls, database writes, container operations, web requests, and integration calls run as Activities.

The application database remains the source of truth for customer-visible state. Temporal payloads contain small identifiers and immutable configuration references rather than full documents, transcripts, credentials, or large model responses.

Workflow and activity identifiers include immutable tenant and run identifiers. Long histories use `Continue-As-New`. Workflow changes follow an explicit compatibility and versioning strategy, and container images use immutable versions.

The MVP self-hosts Temporal with PostgreSQL persistence. Search infrastructure such as Elasticsearch or OpenSearch is not required initially. Temporal UI is private administrative infrastructure.

## Consequences

- Boardroom and provisioning work can resume after worker restarts.
- Schedules, timers, overlap policies, and approval waits share one execution model.
- Engineers must follow Temporal determinism and workflow-versioning rules.
- Temporal persistence must be backed up and monitored; it is durable only while its persistence is recoverable.
- Activities that cause external side effects require careful idempotency and reconciliation.

## Alternatives considered

- **RabbitMQ:** a capable durable broker, but would require a separate workflow state machine, scheduler, retry model, and approval-resumption mechanism.
- **PostgreSQL-backed job queue:** operationally smaller, but would require substantial custom durable workflow behavior that is central to the product.
- **Agent-controlled orchestration:** rejected because it weakens determinism, permissions, cost control, and auditability.

## Revisit when

- Measured Temporal resource usage is unsuitable for the MVP VPS.
- Operational complexity exceeds the value of its workflow guarantees.
- Deployment moves to infrastructure with a different durable-workflow platform requirement.

