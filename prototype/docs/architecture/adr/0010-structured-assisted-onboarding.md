# ADR-0010: Use structured assisted onboarding

Status: Accepted
Date: 2026-08-06

## Context

Mainspring needs to understand both the customer's business and the back-office roles that should support it. A free-form agent interview can capture nuance but may omit required facts, wander, or make configuration changes that are difficult to review. A conventional wizard provides progress and validation but handles real-world exceptions poorly.

## Decision

Use a six-step guided workflow with an onboarding assistant embedded in the experience. The structured steps collect:

1. Canonical business facts
2. The real operating playbook and its exceptions
3. The owner's first three desired outcomes
4. A generated, editable boardroom blueprint
5. Plain-language capability and approval choices
6. A final review and explicit launch

The onboarding assistant writes only to a tenant-scoped draft. It cannot directly create personas, grant tools, or alter schedules. On launch, the application validates and transactionally applies the business profile, operating playbook, persona instructions, capability grants, and permission plan.

Launch grants read, research, document-comment, draft, and proposal capabilities only. Public-web research and document commenting require explicit owner permission. Email sending, invoice issuing, payment execution, ordering, contract acceptance, and schedule modification remain outside the onboarding grant set. Legal, employment, and safety roles are advisory and instructed to escalate decisions requiring qualified professionals.

The generated blueprint contains a lean three-person operations team and an opt-in specialist catalog covering growth, customers, legal and compliance, people and safety, estimating, and procurement. The application sets the boardroom turn limit to the selected persona count during launch, ensuring every selected role receives its bounded turn without enabling unused specialists by default.

Development runtimes expose an owner-only, CSRF-protected reset that clears the onboarding draft and document corpus, removes document attachments, and restores default personas while preserving the login and conversations. The route is unavailable outside development mode.

## Consequences

- Onboarding remains resumable, measurable, and easy to review.
- Persona instructions receive real business context without asking customers to write prompts.
- Agents cannot silently expand their own authority.
- The initial interview is deterministic; a future model-backed interviewer can improve follow-up questions without changing the persisted contract.
- The product must maintain versioned schemas for the business profile, playbook, blueprint, and permission plan as onboarding evolves.

## Alternatives considered

- **Pure conversational interview:** rejected because completion, validation, and configuration review would be unreliable.
- **Form-only wizard:** rejected because it would miss exceptions and the language owners naturally use to describe their operation.
- **Let an onboarding agent provision the boardroom directly:** rejected because it violates application-owned orchestration and capability boundaries.
