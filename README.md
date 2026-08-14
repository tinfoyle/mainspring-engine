# Mainspring

This repository is transitioning from a validated prototype to the production Mainspring platform.

## Repository status

- [`prototype/`](prototype/) is the complete working prototype preserved as an executable reference implementation.
- [`docs/production/`](docs/production/) is the production rewrite specification, architecture, delivery sequence, and quality plan.
- New production code will be built at the repository root alongside these documents. It must reach behavioral parity through explicit contracts and incremental cutovers; the prototype is not to be copied wholesale back into the root.

The prototype checkpoint before relocation is Git commit `8024f8d`.

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

1. Preserve the application-owned orchestration, tenant isolation, capability enforcement, immutable execution records, approval binding, and idempotent side-effect model.
2. Build a modular monolith before considering service extraction.
3. Replace behavior incrementally behind stable contracts; do not perform a big-bang cutover.
4. Keep the prototype runnable until every production capability has passed its parity and production-readiness gates.
5. Treat security, observability, recovery, accessibility, and operability as product requirements rather than final hardening tasks.
