# ADR-0013: Model business variants as tenant templates

Status: Proposed
Date: 2026-08-06

## Context

Mainspring began with a trade-business back office, but the boardroom, durable orchestration, document retrieval, schedules, integrations, authentication, and capability boundaries also apply to software product companies and managed service providers. Forking the application per industry would duplicate security-sensitive infrastructure and make improvements drift between variants.

At the same time, a generic onboarding and persona catalog would force customers to translate unfamiliar vocabulary. A SaaS founder thinks in activation, churn, roadmap delivery, reliability, subscription revenue, and customer success. An MSP owner also thinks in service queues, SLAs, client agreements, projects, recurring services, monitoring, and technical account plans.

## Decision

Each tenant has a business-template identifier. The runtime currently accepts `trades` and `software`, defaulting to `trades` for backward compatibility. The prior `saas` value maps to `software` so existing tenant configuration remains valid.

The template selects:

- onboarding labels, choices, prompts, and validation language;
- the initial boardroom name and description;
- the available persona blueprints, default core team, and priority recommendations;
- tenant-facing examples for conversations and recurring schedules; and
- business-context wording added to persona instructions.

Templates do not select a different executable, database schema, orchestration workflow, provider adapter, integration implementation, or authorization model. Capabilities remain enforced by the shared Go harness. The template is immutable tenant provisioning metadata for the MVP; a future migration workflow may allow it to change safely.

## Consequences

- A new industry can be developed as a focused presentation and policy package without copying the application.
- Software tenants receive relevant product, engineering, customer success, revenue, growth, website, service delivery, technical account, cloud operations, reliability, and security roles.
- Shared infrastructure and security fixes apply to every business variant.
- Template-aware copy and catalogs require explicit regression tests so one industry's language does not leak into another.
- Fields in the current onboarding persistence model have stable storage names but template-specific meanings. A later schema revision may replace them with a versioned, typed template document if variants diverge substantially.

## Alternatives considered

### Separate SaaS fork

Rejected because workflows, integrations, security boundaries, and most UI behavior would be duplicated and would drift.

### One fully generic experience

Rejected because it makes onboarding less clear for the owner and produces vague personas with weak operating context.

### User-authored personas only

Deferred as an advanced option. Curated templates give a first-time owner a useful, bounded starting team while still allowing names and team membership to be edited.

## Revisit when

- customers need to change templates after launch;
- an industry needs materially different domain records or workflow semantics;
- the catalog is distributed or independently versioned; or
- tenant-specific custom templates become a product feature.
