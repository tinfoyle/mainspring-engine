# ADR-0007: Marketing owns governed campaign releases, not delivery providers

- Status: Accepted
- Date: 2026-08-23
- Owners: Marketing, Attention, Integrations, platform security

## Context

Phase 3 requires a final Marketing package rather than a preview shell. The prototype has no coherent Marketing domain to migrate. Spyglass does have Agent execution, immutable Knowledge provenance, human Attention approvals and planned email/web connector boundaries. Putting campaign state into an email adapter would make provider state authoritative; letting an Agent publish directly would bypass the existing consequential-action policy; and treating the public Infinite Ocean site as a customer content-management system would merge unrelated products.

## Decision

Marketing is an Account-owned planning and governance system for campaigns, immutable creative revisions and exact release snapshots.

1. A campaign declares a bounded objective, audience and supported channel intents. The first launch vocabulary is `email` and `web`; a channel is only intent and never implies that a connector is configured or authorized.
2. Creative content is stored outside the aggregate. Marketing freezes an immutable revision identity, media type, byte count, SHA-256, opaque content reference and required image alternative text. Replacing content creates a new revision.
3. A release plan binds one exact campaign version, a sorted unique set of asset revisions and the exact channel set. It cannot silently follow later edits.
4. Agents may create drafts only with an exact Run and invocation provenance. They cannot submit, approve, activate, pause, complete or archive a campaign and cannot send or publish content.
5. A User may submit a release for review. Only an Owner or Administrator may approve it, and approval binds an exact Attention consequential-approval identity. Activating a campaign requires that approved release to match the same Account, campaign version and channels.
6. Connector execution belongs to Integrations. It rechecks current package capability, scoped credentials, provider health and the still-valid approval before each external effect. Unknown delivery outcomes are reconciled; they are never reported as success by Marketing.
7. Marketing persistence will use forced RLS, optimistic versions, immutable content-redacted events, movement fencing and exact erasure/restore accounting. Operational telemetry contains identifiers, state, counts, timing and error classes—not objectives, audiences, creative text or provider payloads.
8. The Marketing package remains non-executable in the deployment inventory until persistence, Catalog lifecycle, HTTP/MCP/UI surfaces, worker/tool boundaries, observability, retention and acceptance tests are complete.

## Consequences

- Provider replacement or revocation cannot rewrite campaign history.
- Human approval binds the exact material that can later be delivered.
- Agent assistance remains useful without granting a workload publishing authority.
- Email/web connectors can fail or remain absent without corrupting Marketing state.
- Additional channel types require an explicit product, connector and policy change rather than accepting arbitrary strings.

## Verification

- Pure kernel tests cover channel policy, Agent provenance, immutable ordered asset revisions, stale versions, exact release binding and human-manager approval.
- PostgreSQL tests must prove forced RLS, cross-Account denial, immutable revisions/events, optimistic replay, movement fencing and exact erasure/restore counts.
- Transport tests must prove enabled/read-only/suspended package behavior and identical HTTP/MCP outcomes.
- Stage certification must approve an exact synthetic release, inject connector retry/unknown outcomes, reconcile them, revoke the credential and erase the complete fixture.
