# Architecture Decision Records

This directory records decisions that constrain the Infinite Ocean: Spyglass production rewrite. Accepted decisions are implementation requirements. A later decision may supersede one, but feature work must not silently diverge from them.

| ID | Decision | Status |
|---|---|---|
| [ADR-0001](0001-product-identity-and-web-surfaces.md) | Infinite Ocean company identity, Spyglass product identity, and public/application web surfaces | Accepted |
| [ADR-0002](0002-system-identity-accounts-packages-and-billing.md) | System-wide Users, Spyglass Accounts, package entitlements, and asynchronous Stripe billing | Accepted |
| [ADR-0003](0003-pooled-cell-runtime.md) | Shared workload-class deployments with account-isolated cells instead of per-customer containers | Accepted |
| [ADR-0004](0004-knowledge-document-lifecycle.md) | Account-isolated document objects, immutable revisions, fail-closed processing, and environment storage mapping | Accepted |
| [ADR-0005](0005-frozen-agent-run-plans.md) | Immutable Agent Run plans with bounded forward-only delegation and no model-driven expansion | Accepted |
| [ADR-0006](0006-finance-operational-ledger.md) | Finance as an Account-owned operational ledger rather than billing or payment execution | Accepted |
| [ADR-0007](0007-marketing-governed-release.md) | Marketing owns governed release snapshots while Integrations owns provider delivery | Accepted |
| [ADR-0008](0008-integration-connector-execution.md) | Versioned connector scopes, external credential custody, and reconciliation-first delivery | Accepted |
| [ADR-0009](0009-account-portability-export.md) | Deterministic Account portability artifacts from reviewed projections and exact versioned objects | Accepted |

## Record lifecycle

- **Proposed**: open for review and not yet an implementation constraint.
- **Accepted**: required for new production code and delivery gates.
- **Superseded**: retained for history and linked to its replacement.
- **Rejected**: considered but not selected.

Each record identifies its consequences and verification. Acceptance evidence belongs in the linked delivery item, test suite, and operations document rather than being inferred from code alone.
