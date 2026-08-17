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
- [Website, Accounts, Packages, and Billing](docs/production/accounts-packages-billing.md)
- [Pooled Kubernetes and cell topology](docs/production/kubernetes-topology.md)
- [Detailed delivery backlog](docs/production/delivery-plan.md)
- [Quality, security, and operations gates](docs/production/quality-security-operations.md)
- [Current Phase 2 development slice](docs/production/development-slice.md)

Current executable production surfaces:

- [`website/`](website/) contains the public Infinite Ocean site and Spyglass product/package/pricing experience.
- [`cmd/spyglass`](cmd/spyglass/) composes the development Account API.
- [`internal/modules`](internal/modules/) contains Identity, Accounts, Sessions, Access, Catalog, Entitlements, Billing, and Placement boundaries.
- [`migrations/`](migrations/) contains the initial global and cell PostgreSQL schemas.
- [`deploy/kubernetes/reference/`](deploy/kubernetes/reference/) captures the review-only pooled workload topology; it is deliberately fail-closed until release and environment overlays supply real artifacts and managed configuration.
