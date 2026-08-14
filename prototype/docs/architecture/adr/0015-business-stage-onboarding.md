# ADR-0015: Branch onboarding by business stage

Status: Proposed
Date: 2026-08-06

## Context

The original onboarding assumes that a customer already operates a business with customers, staff, systems, workflows, invoices, and recurring exceptions. A person starting a business from scratch cannot answer those questions honestly. Requiring them to invent current-state answers would create misleading persona context and encourage agents to treat plans as facts.

Business stage is separate from industry. A new plumbing company and a new SaaS product both need validation, launch planning, basic financial modeling, and operating setup, while still needing industry-specific language and specialists.

## Decision

At the beginning of onboarding, the owner chooses either `operating` or `starting`. This business-stage choice layers on top of the tenant's `trades` or `software` template rather than introducing another runtime or industry template.

The operating path retains the current-state workflow. The starting path:

- collects intended customers, offers, constraints, systems, and initial team size;
- asks how the simplest launch process should work in future tense;
- offers validation, pricing, compliance, budget, systems, and go-to-market priorities;
- proposes a lean `Launch Room` with planning-stage roles instead of assuming mature dispatch, support, or customer-success operations;
- labels processes, forecasts, customers, prices, and dates as hypotheses in every generated persona instruction;
- recommends an assumption-testing first boardroom conversation.

Changing the stage during an unfinished onboarding clears dependent playbook, priority, blueprint, and permission drafts so mature-business answers are not silently mixed with startup assumptions. The stage is stored inside the existing versionable business-profile JSON document.

## Consequences

- Founders can give truthful partial answers without pretending the company already operates.
- Industry vocabulary and specialist catalogs remain reusable across both stages.
- Startup personas must distinguish research and planning from professional legal, financial, or regulatory advice.
- A business will eventually need a controlled transition from `starting` to `operating`; the MVP does not automatically replace its launched personas when that happens.
- Stage-specific fields currently reuse stable playbook storage names, so future schema versions may introduce typed startup and operating playbooks.

## Alternatives considered

- **Create a third tenant template named `startup`:** rejected because stage and industry are orthogonal, and it would lose trade/software specialization.
- **Ask the existing questions and allow “not applicable”:** rejected because the resulting context still frames hypothetical workflows as current facts.
- **Use a free-form onboarding interview only:** rejected because structured inputs are easier to validate, review, migrate, and safely convert into persona configuration.

## Revisit when

- launched startups need a guided transition into operating-business personas;
- financing, entity formation, licensing, or market-validation modules become dedicated product areas;
- stage-specific playbooks diverge enough to warrant separate versioned schemas.
