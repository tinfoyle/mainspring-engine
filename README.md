# Infinite Ocean: Spyglass

This repository is transitioning from the validated Mainspring prototype to **Infinite Ocean: Spyglass**, the production business operating platform created by Infinite Ocean.

The public company and product website will be [infiniteocean.net](https://infiniteocean.net). Visitors can learn about Spyglass, compare packages, create a system-wide identity and free Spyglass Account, and optionally purchase a Stripe-backed subscription before entering the application.

## Repository status

- [`prototype/`](prototype/) is the complete working Mainspring prototype preserved as an executable reference implementation.
- [`docs/production/`](docs/production/) is the production rewrite specification, architecture, delivery sequence, and quality plan.
- New production code is being built at the repository root alongside these documents. It must reach behavioral parity through explicit contracts and incremental cutovers; the prototype is not copied wholesale back into the root.

The prototype checkpoint before relocation is Git commit `8024f8d`. Historical prototype naming remains intact so the checkpoint stays reproducible; new production code and documentation use Spyglass.

## Prototype development

Run existing commands from the prototype directory:

```bash
cd prototype
make test-fast
make build
```

The Docker-backed verification suite remains:

```bash
cd prototype
make test-full
```

## Production rewrite

Start with the [production rewrite plan](docs/production/README.md). Its governing rules are:

1. Preserve application-owned orchestration, account isolation, capability enforcement, immutable execution records, approval binding, and idempotent side-effect handling.
2. Build a modular monolith before considering service extraction.
3. Replace behavior incrementally behind stable contracts; do not perform a big-bang cutover.
4. Keep the prototype runnable until every production capability has passed its parity and production-readiness gates.
5. Treat security, observability, recovery, accessibility, and operability as product requirements rather than final hardening tasks.

The focused design documents are:

- [Target architecture](docs/production/architecture.md)
- [Accepted architecture decisions](docs/production/decisions/README.md)
- [Website, Accounts, Packages, and Billing](docs/production/accounts-packages-billing.md)
- [Identity security and passkeys](docs/production/identity-security.md)
- [Passkey envelope-key rotation operations](docs/production/passkey-key-rotation.md)
- [Platform operator authorization](docs/production/operator-authorization.md)
- [Stripe commercial access operations](docs/production/stripe-operations.md)
- [Catalog publication operations](docs/production/catalog-operations.md)
- [Package and usage admission](docs/production/usage-admission.md)
- [Work module production design](docs/production/work-module.md)
- [Work release dead-letter operations](docs/production/work-release-operations.md)
- [Account export and erasure](docs/production/account-erasure.md)
- [Production runtime configuration](docs/production/runtime-configuration.md)
- [Pooled Kubernetes and cell topology](docs/production/kubernetes-topology.md)
- [Global-to-cell routing boundary](docs/production/routing-boundary.md)
- [Route and workload identity rotation operations](docs/production/route-rotation-operations.md)
- [Detailed delivery backlog](docs/production/delivery-plan.md)
- [Quality, security, and operations gates](docs/production/quality-security-operations.md)
- [Production-readiness audit and closeout gates](docs/production/production-readiness-audit.md)
- [Customer API contract and drift policy](docs/production/api-contract.md)
- [Current Phase 2 development slice](docs/production/development-slice.md)

Current executable production surfaces:

- [`website/`](website/) contains the public Infinite Ocean site and Spyglass product/package/pricing experience.
- [`cmd/spyglass`](cmd/spyglass/) provides development, persistent Account API, global app-router, cell app-api, private admission-api, route-receipt/billing/notification/entitlement workers, the Work capacity reconciler, one-shot route-rotation, passkey-key, signed billing and Agent-queue operators, migration, audited Catalog/Work-release modes, and four-eyes Account erasure with leased execution and signed restore replay.
- [`internal/modules`](internal/modules/) contains Identity, Accounts, Sessions, Access, Catalog, Entitlements, Billing, Placement, and the first production Work boundary.
- [`migrations/`](migrations/) contains the initial global and cell PostgreSQL schemas.
- [`deploy/kubernetes/reference/`](deploy/kubernetes/reference/) captures the review-only pooled workload topology; it is deliberately fail-closed until release and environment overlays supply real artifacts and managed configuration.
