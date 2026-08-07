# ADR-0022: Store typed, versioned agent configuration

Status: Proposed
Date: 2026-08-07

## Context

Onboarding supplies useful default personas, but owners need to adapt each specialist without editing database rows or relying on prompt-only conventions. The application must preserve provider neutrality, prevent unsafe or malformed settings, and ensure that edits made during an active boardroom round do not change that round underneath Temporal.

## Decision

Give tenant owners and administrators an agent-management interface. Store the following configuration on each persona:

- Identity, role, owner-facing purpose, system instructions, active state, boardroom, and turn position
- Provider selection, optional model override, reasoning effort, and provider-neutral sampling values
- Context, output, timeout, and tool-call ceilings plus an estimated-cost quota reservation
- Response style, citation behavior, and external-action behavior
- Explicit capability grants with optional server-interpreted conditions

Validate every value in the Go domain service and constrain the columns in PostgreSQL. A persona may be deactivated but is not destructively deleted. Its boardroom identity remains stable; owners duplicate an agent when they need a separate identity elsewhere. Keep the boardroom turn cap synchronized to its active-agent count and reject an edit that would leave no active agent, so an active specialist is never silently omitted.

When a run is prepared, snapshot the full runtime configuration, prompt, grants, and output schema into an immutable persona version. The turn uses that version even if an owner edits the live persona before the Activity executes. Reuse a prior version only when its complete content hash matches.

Provider adapters receive the normalized configuration through the provider-neutral invocation contract. The Codex adapter currently applies model and reasoning overrides; temperature and top-p remain stored for adapters that support them. The application, rather than the model, enforces timeout, context and output reservations, tool-call count, spend reservation, capability conditions, citation provenance, and action suppression.

## Consequences

Owners can tune specialists without weakening orchestration or historical reproducibility. Provider-specific support can arrive incrementally without another persona schema redesign. Configuration creates a larger validation surface, and some behavior controls remain best-effort provider instructions while hard limits remain application-enforced.

## Alternatives considered

- Store one untyped JSON settings object: rejected because migrations, validation, indexing, and operational inspection would become fragile.
- Put all customization in the system prompt: rejected because prompts cannot enforce resource, capability, approval, or cost boundaries.
- Mutate configuration in place for active runs: rejected because retries could execute different work from the original Temporal event.
- Create a new persona record after every edit: rejected because it would clutter the boardroom and make stable ownership difficult; immutable versions provide the required history.

## Revisit when

Claude Code, OpenRouter, or direct model APIs require provider-specific configuration extensions; plans require reusable organization-wide agent templates; or owners need an explicit cross-boardroom move workflow with history and authorization semantics.
