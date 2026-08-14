# ADR-0025: Coordinate owner input through shared business knowledge

Status: Accepted
Date: 2026-08-12

## Context

Autonomous ticket agents correctly pause when private business knowledge is unavailable, but independent ticket conversations produce repeated questions and force the owner through a long queue of unrelated forms. Onboarding already captures useful business facts, yet those facts were scoped to one baseline assessment and ticket answers remained isolated JSON responses. Agents could not reliably reuse an answer supplied elsewhere.

## Decision

Maintain a tenant-local, versioned business knowledge registry. Onboarding answers, direct ticket answers, and the owner-input coordinator all write current facts with provenance, confidence, scope, sensitivity, confirmation time, and immutable version history.

Represent each human-input question as a link to a stable fact key. Explicit agent-supplied keys take precedence; deterministic classification supplies backward-compatible keys for existing string-only questions. Before presenting a question, resolve it from an active known fact when possible.

Present pending owner input as a conversation with Mia. Mia groups questions by fact key, ranks topics by the number of distinct tickets unblocked, asks one consolidated question, shows its ticket impact, accepts document evidence, and applies the answer to every matching pending request. A request closes only after all of its linked questions are answered. Closing the owner child item allows the existing agent-work dispatcher to resume the parent ticket. Original requests and historical answers remain available as an audit trail.

Every ticket invocation receives the active shared fact registry before it may request more owner input. The application, not the model, owns fact reuse, request completion, and ticket resumption.

## Consequences

- Owners answer common business questions once rather than once per agent.
- Onboarding becomes the initial contribution to an accumulating business knowledge base.
- Agents resume independently as soon as their particular information requirements are satisfied.
- Fact provenance and version history make corrections auditable.
- Heuristic keys remain conservative and can be replaced by explicit structured keys as agent output evolves.
- Highly sensitive facts remain tenant-local and are shown only to authenticated tenant users and authorized internal agent runs.

## Alternatives considered

- Keep one form per ticket: rejected because it preserves repetition and hides cross-ticket leverage.
- Let a language model decide which tickets to resume: rejected because retries and model variation would make workflow state nondeterministic.
- Append all answers only to prompts: rejected because facts would lack provenance, correction history, and queryable reuse.

## Revisit when

- facts require product-, location-, or legal-entity-specific access policies;
- semantic clustering can use a governed embedding service without weakening deterministic resolution;
- stale-fact review and explicit contradiction reconciliation become a dedicated owner workflow.
