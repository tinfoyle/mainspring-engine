# ADR-0006: Enforce tools through capability grants

Status: Proposed  
Date: 2026-08-06

## Context

Personas require specialized tools. A legal persona may search the web and read or annotate documents but must not modify schedules, billing, credentials, or boardroom configuration. Prompt instructions and hidden tool descriptions are not security boundaries, especially when agents process untrusted documents and webpages.

## Decision

All agent tool calls pass through a Mainspring Tool Broker implemented in application code.

The effective grant set is the intersection of:

1. Platform policy
2. Tenant policy
3. Boardroom policy
4. Persona grants
5. Current workflow phase
6. Recorded approval state

An invocation receives a short-lived signed capability token containing its tenant, run, persona, grants, and expiration. Every tool call is authorized again by the broker. The broker verifies that the token tenant matches the target resource tenant and records an audit event.

Tools separate preparation from execution where an action can affect a person, account, schedule, or money:

```text
email.draft        email.send
invoice.prepare    invoice.issue
payment.propose    payment.execute
schedule.propose   schedule.modify
ticket.prepare     ticket.create
```

The broker exposes only granted tools to compatible provider protocols such as MCP, but server-side denial remains authoritative if an agent guesses an unavailable tool name.

## Consequences

- Persona permissions are reviewable, testable, and auditable outside prompts.
- Provider adapters can translate different tool-calling protocols into one authorization path.
- Tool schemas and resource identifiers require stable versioning.
- Approval-aware tools need durable coordination with Temporal and the action ledger.
- Integration credentials remain in the tool service and are not handed to agent processes.

## Alternatives considered

- **Prompt-only tool restrictions:** rejected because they cannot enforce authorization.
- **Give every persona the same tools and rely on approval:** rejected because excessive capability increases accident and prompt-injection impact.
- **Provider-specific tool implementations:** rejected because permission behavior would differ across Codex, Claude Code, and OpenRouter adapters.

## Revisit when

- Policy complexity justifies adopting a dedicated policy engine.
- Tools must be delegated across multiple clusters or third-party runtimes.
- Customers require custom tools or tenant-authored policies.

