# ADR-0005: Use a provider-neutral agent harness

Status: Proposed  
Date: 2026-08-06

## Context

The MVP will invoke Codex CLI because it is immediately available. Later versions are expected to support Claude Code, OpenRouter, and other model or agent runtimes. Provider-specific session, workspace, tool, and output concepts must not become Mainspring domain concepts.

The application must retain responsibility for deciding which persona runs, what context it receives, which tools are available, and when the boardroom advances or completes.

## Decision

Define a provider-neutral harness with a narrow interface resembling:

```go
type AgentProvider interface {
    Invoke(context.Context, Invocation) (Result, error)
    Cancel(context.Context, InvocationID) error
    Capabilities() Capabilities
}
```

Normalized invocation input includes:

- Tenant, boardroom, run, turn, and persona identifiers
- System instructions and message context
- Allowed tool grants
- Output schema
- Timeout, token, cost, and turn limits
- References to approved workspace artifacts

Normalized results include:

- Final structured output
- Provider and model metadata
- Usage information when available
- Requested tool calls and their results
- Provider thread/session reference when available
- Terminal status and normalized failure category

The Codex adapter is the first implementation. Provider session identifiers and raw events remain adapter metadata. The domain never assumes that all providers expose a shell, repository, resumable thread, or identical tool-calling behavior.

The boardroom orchestrator invokes one bounded persona turn at a time. An agent may recommend consulting another persona, but only application workflow code can enqueue that turn.

## Consequences

- Providers can be added or replaced without rewriting boardroom state.
- Some provider-specific features remain optional capabilities rather than lowest-common-denominator domain features.
- Contract tests and recorded fixtures are required for every adapter.
- Provider output must be validated before it can advance workflow state or invoke a tool.
- Model credentials, rate limits, costs, and cancellation semantics require provider-specific implementation behind the interface.

## Alternatives considered

- **Call Codex CLI directly throughout the domain:** rejected because CLI concepts would leak into persistence and orchestration.
- **Adopt a third-party multi-agent framework as the orchestrator:** rejected because Mainspring requires application-owned boardroom semantics and permissions.
- **Standardize immediately on one model API:** rejected because provider diversity is an explicit product direction.

## Revisit when

- Two or more implemented providers reveal an abstraction that is too broad or too narrow.
- A provider cannot support the required structured output or tool contract.
- Mainspring introduces provider routing based on cost, latency, or task type.

