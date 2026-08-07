# ADR-0018: Execute agent tools through a capability broker

Status: Proposed
Date: 2026-08-07

## Context

Agents need tenant documents and integrations, but prompts and provider sandboxes are not authorization boundaries. Tool results can also overflow context or produce unverifiable citations.

## Decision

Providers emit structured tool requests. The Go harness executes at most five requests per turn through a broker that verifies a signed, short-lived invocation token and the immutable persona grants. The first tool is `documents.search`, which validates input, limits documents and results, and returns stable `doc:{id}:chunk:{n}` citations. Tool outputs are recorded as invocation events and supplied only to the next bounded provider call. Final citations must match returned records.

## Consequences

Provider credentials never imply tool authority, denials are auditable, and retrieved evidence is bounded and traceable. Each new tool needs a server-side schema, handler, limits, and grant mapping.

## Alternatives considered

- Provider-direct database or RAG access: rejected because it bypasses tenant and grant checks.
- Put all documents in every prompt: rejected for cost, relevance, and disclosure risk.

## Revisit when

Parallel tool execution or provider-native tool streaming materially improves latency.
