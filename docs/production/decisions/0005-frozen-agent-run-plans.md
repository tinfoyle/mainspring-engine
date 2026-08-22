# ADR-0005: Agent delegations cannot expand a frozen Run plan

- Status: Accepted
- Date: 2026-08-22
- Owners: Agents, platform security

## Context

Spyglass Run creation freezes an ordered set of immutable Persona versions, a Boardroom policy version, context bytes, provider policy, operation identities and a plan digest. Sequential execution makes only the next frozen invocation dispatchable. A `manager_led` Run additionally freezes its synthesis manager as the final turn. Failed-turn recovery copies the unexecuted frozen suffix rather than reading newer Persona or context state.

Agent results contain structured delegation requests. The implemented policy permits a Persona to address a later Persona already present in that frozen plan, and the application inserts the request as labeled untrusted task content when that target dispatches. An earlier backlog item proposed allowing model output to add a Persona that was not in the plan.

Adding a new Persona after execution starts would require at least one unsafe compromise: read a mutable latest Persona version; rewrite the plan digest and turn count; replace or renumber preallocated invocation/tool/model identities; move a frozen synthesis manager; or keep an unaudited second execution order outside the Run plan. Exact retry after a crash could then depend on when expansion occurred. It would also let model output select new workload identities instead of making delegation a bounded routing hint within application-owned policy.

## Decision

An Agent Run plan is immutable after creation and model output cannot expand it.

Delegation has the following final contract:

1. The caller chooses the specialist set. For `manager_led`, the application appends the exact current manager version as the final turn before the Run is committed.
2. Every turn, provider target and tool/model operation identity is allocated before dispatch and is covered by the immutable plan/context boundary.
3. Result policy exposes only later Personas in that exact plan as delegation targets. A result may target each eligible Persona at most once.
4. A validated request is content, not control flow. The application routes it only to the matching frozen later Persona and labels it as untrusted prior-Persona task content.
5. Unknown, current, earlier, duplicate or out-of-plan Persona identities fail result publication atomically.
6. Failed-turn retry copies the frozen failed/successor suffix and its exact Persona versions. It does not interpret delegation output to construct a new plan.
7. Additional expertise requires a new user-, schedule- or application-owned Run command with ordinary authorization, entitlement and capacity checks. An approved consequential action may propose that command, but the model cannot execute it by emitting a delegation.

## Consequences

- Manager synthesis remains final and retry-safe.
- Persona publication after Run creation cannot change that Run.
- Invocation and operation identities retain one derivation and one audit history.
- Delegation remains useful for ordered Boardroom collaboration without becoming an implicit workflow engine.
- Schedules and approved application workflows may create follow-on Runs through the canonical StartRun service when dynamic fan-out is genuinely required.
- “Dynamic delegation expansion” is removed from the Phase 3 construction backlog; bounded frozen-plan delegation is the completed product behavior.

## Verification

- Pure result-policy tests reject invented, duplicate, current and non-forward targets.
- Projection tests prove a denied delegation commits neither Message nor Run state.
- Dispatch tests route a validated request only to its exact frozen target and label it untrusted.
- Manager-led PostgreSQL tests prove the manager's exact version and final position do not change after later Persona publication.
- Retry tests prove the failed suffix reuses frozen Persona/context bytes and cannot add a target from result output.
- Full local and connected-environment certification continues to cover crash replay, Account isolation and least-privilege projection.
