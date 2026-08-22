# Architecture Decision Records

This directory records decisions that constrain the Infinite Ocean: Spyglass production rewrite. Accepted decisions are implementation requirements. A later decision may supersede one, but feature work must not silently diverge from them.

| ID | Decision | Status |
|---|---|---|
| [ADR-0001](0001-product-identity-and-web-surfaces.md) | Infinite Ocean company identity, Spyglass product identity, and public/application web surfaces | Accepted |
| [ADR-0002](0002-system-identity-accounts-packages-and-billing.md) | System-wide Users, Spyglass Accounts, package entitlements, and asynchronous Stripe billing | Accepted |
| [ADR-0003](0003-pooled-cell-runtime.md) | Shared workload-class deployments with account-isolated cells instead of per-customer containers | Accepted |
| [ADR-0004](0004-knowledge-document-lifecycle.md) | Account-isolated document objects, immutable revisions, fail-closed processing, and environment storage mapping | Accepted |
| [ADR-0005](0005-frozen-agent-run-plans.md) | Immutable Agent Run plans with bounded forward-only delegation and no model-driven expansion | Accepted |

## Record lifecycle

- **Proposed**: open for review and not yet an implementation constraint.
- **Accepted**: required for new production code and delivery gates.
- **Superseded**: retained for history and linked to its replacement.
- **Rejected**: considered but not selected.

Each record identifies its consequences and verification. Acceptance evidence belongs in the linked delivery item, test suite, and operations document rather than being inferred from code alone.
