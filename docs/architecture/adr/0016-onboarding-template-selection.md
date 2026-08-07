# ADR-0016: Select the business template inside onboarding

Status: Proposed
Date: 2026-08-06

## Context

The first demo used separate `demo.localhost` and `saas.localhost` tenants to show trade and software experiences. That duplicated tenant, RAG, worker, database, and registration services merely to demonstrate configuration. It also hid an important product decision behind the hostname: a new customer should tell Mainspring what kind of business they operate instead of receiving an industry selected by deployment configuration.

Business template and business stage are independent. An owner may operate an established trade, SaaS, or MSP company, or may be starting any of those businesses from scratch.

## Decision

Use one canonical demo tenant at `demo.localhost`. Its first onboarding screen offers trade/field service, SaaS/software, MSP/IT services, and start-from-scratch entry points. The new-business entry asks for the closest industry context before collecting the launch plan.

Store the canonical `trades` or `software` template and the user-facing `trades`, `saas`, or `msp` variant in the tenant onboarding business profile. During onboarding and after launch, tenant UI and blueprint generation resolve the stored choice rather than the runtime's default environment value. The environment value remains a backward-compatible fallback for already-provisioned tenants without a stored selection.

Changing the template during an unfinished onboarding clears dependent playbook, priority, persona, and permission drafts. Template selection does not change the executable, tenant boundary, database, workflow engine, tools, or authorization model.

The local Compose stack runs one tenant runtime, one worker, and one RAG service. `saas.localhost` redirects to `demo.localhost` for old bookmarks.

This decision supersedes the MVP immutability statement in ADR-0013. Template choice remains controlled onboarding data; changing a launched production tenant still requires a future migration workflow.

## Consequences

- One demo account can exercise every supported business experience.
- Local resource use falls because the duplicate software tenant stack is removed.
- Smoke tests must reset and reconfigure the same tenant, so they run serially.
- Post-launch pages must resolve tenant template data rather than relying only on process configuration.
- The stored variant can specialize SaaS and MSP recommendations while both share the software template implementation.

## Alternatives considered

- **Keep one subdomain per example:** rejected because it duplicates infrastructure and makes templates appear to be different products.
- **Create separate executables for trades, SaaS, and MSP:** rejected because orchestration, security, integrations, and tenancy are shared.
- **Make start-from-scratch a fourth industry template:** rejected because stage and industry are orthogonal.

## Revisit when

- production customers need to change templates after launch;
- custom or marketplace templates become tenant-managed objects;
- a demo needs isolated concurrent sessions for multiple evaluators.
