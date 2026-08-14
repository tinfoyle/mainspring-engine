# ADR-0017: Persist immutable agent turns and structured results

Status: Proposed
Date: 2026-08-07

## Context

Boardroom retries must not duplicate messages or silently change which persona, instructions, grants, or schema participated. Provider text alone is also too ambiguous for tools, approvals, usage, and future provider adapters.

## Decision

At run preparation, snapshot the ordered team as immutable, content-addressed persona versions. Represent each `(run, turn)` as one uniquely constrained invocation row and one Temporal Activity. Build context deterministically within a token budget and persist its manifest. Require a provider-neutral structured result containing a contribution, findings, recommendations, questions, citations, proposed actions, tool requests, confidence, provider metadata, and usage. Atomically commit the successful invocation and its single boardroom message.

## Consequences

Retries reuse one invocation identity, historical runs remain reproducible, and new providers implement one bounded contract. Schema changes require a new snapshotted version. Large conversation context must be deliberately selected rather than appended without limit.

## Alternatives considered

- Persist only messages: rejected because attempts, usage, failures, and immutable inputs disappear.
- Let a model orchestrate the room: rejected because ordering and retry behavior would not be deterministic.

## Revisit when

Provider-native resumable threads can be adopted without weakening application-owned state.
