# Spyglass Docker environments

Run these commands from `deploy/docker/spyglass` inside the `ubunturojo` WSL distribution.

```bash
make verify
```

This builds and tests the standalone website image, builds the multi-mode Spyglass application image, starts the minimal edge/website/development-application topology, and exercises the public/private local origins through Caddy on port `8088`.

Use `make down` to stop the stack while preserving state. The destructive reset is deliberately explicit:

```bash
make reset CONFIRM=spyglass-local
```

The next Phase 2.5 slice replaces the development application service with persistent global and two-cell PostgreSQL-backed process modes.
