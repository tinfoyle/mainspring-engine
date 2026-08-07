# Architecture Decision Records

Architecture decision records capture consequential technical choices and the reasoning behind them. Records begin as `Proposed`; they become `Accepted` after review. A later decision supersedes an earlier record rather than rewriting its history.

## Status values

- `Proposed`: under review
- `Accepted`: approved for implementation
- `Superseded`: replaced by a newer ADR
- `Deprecated`: retained for history but no longer recommended
- `Rejected`: considered and deliberately not selected

## Index

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](0001-go-html-over-the-wire.md) | Use Go and an HTML-over-the-wire frontend | Proposed |
| [0002](0002-control-plane-and-tenant-runtime.md) | Separate the control plane from tenant runtimes | Proposed |
| [0003](0003-tenant-database-isolation.md) | Use a database and role per tenant | Proposed |
| [0004](0004-temporal-orchestration.md) | Use Temporal for durable orchestration | Proposed |
| [0005](0005-provider-neutral-agent-harness.md) | Use a provider-neutral agent harness | Proposed |
| [0006](0006-capability-controlled-tools.md) | Enforce tools through capability grants | Proposed |
| [0007](0007-runtime-provisioning.md) | Abstract runtime provisioning | Proposed |
| [0008](0008-external-side-effects.md) | Record and reconcile external side effects | Proposed |
| [0009](0009-mvp-security-boundary.md) | Adopt a minimum viable security boundary | Proposed |
| [0010](0010-structured-assisted-onboarding.md) | Use structured assisted onboarding | Proposed |
| [0011](0011-rag-document-library.md) | Keep document ingestion behind the tenant RAG service | Proposed |
| [0012](0012-tenant-email-integration.md) | Keep mailbox credentials and delivery inside the tenant runtime | Proposed |
| [0013](0013-tenant-business-templates.md) | Model business variants as tenant templates | Proposed |
| [0014](0014-hybrid-work-queue.md) | Use one tenant work queue for to-dos and tickets | Proposed |
| [0015](0015-business-stage-onboarding.md) | Branch onboarding by business stage | Proposed |
| [0016](0016-onboarding-template-selection.md) | Select the business template inside onboarding | Proposed |

## Template

New records should contain:

```text
# ADR-NNNN: Title

Status: Proposed
Date: YYYY-MM-DD

## Context
## Decision
## Consequences
## Alternatives considered
## Revisit when
```
