# ADR-0003: Use a database and role per tenant

Status: Proposed  
Date: 2026-08-06

## Context

Mainspring must be multi-tenant from its first release. Shared tables with a `tenant_id` are resource-efficient but make every query and migration part of the security boundary. A PostgreSQL server container for every tenant provides greater physical separation but creates substantial idle memory, backup, connection, monitoring, and upgrade overhead.

## Decision

For the MVP, use one shared PostgreSQL server with separate physical databases and roles:

```text
mainspring_control
temporal
temporal_visibility
tenant_<immutable_uuid>
```

Each tenant receives:

- A database identified by an immutable internal UUID
- A unique owner/role and generated secret
- `CONNECT` permission only for its database
- pgvector in its database for vector retrieval
- Independently executable tenant migrations
- Independently addressable backups and restores

The control-plane database stores only tenant registry, placement, billing, and provisioning information. It does not store customer documents, conversations, or business records.

The provisioner owns the privileged database-administration credential. Control-plane, gateway, agent-runner, and customer-facing processes do not receive it. Database and role identifiers are generated from validated UUIDs rather than customer input.

The persistence layer must support a future dedicated-server implementation for tenants that require stronger isolation.

## Consequences

- Tenant queries do not require a row-level `tenant_id` filter within the tenant database.
- A leaked tenant database credential cannot legitimately connect to another tenant database.
- Migrations must be orchestrated across many databases and report per-tenant status.
- Connection count, schema compatibility, backup cataloging, and restore procedures require platform tooling.
- A shared PostgreSQL process remains a common infrastructure failure domain.

## Alternatives considered

- **Shared tables with row-level security:** rejected for the MVP because application mistakes can more easily become cross-tenant disclosures.
- **PostgreSQL container per tenant:** deferred because it conflicts with VPS resource-density goals.
- **SQLite per tenant:** rejected for now because PostgreSQL and pgvector better match concurrent workflows, retrieval, operational tooling, and future migration needs.

## Revisit when

- A tenant requires dedicated infrastructure.
- Shared PostgreSQL resource contention becomes material.
- The system moves to Kubernetes or a managed database platform.
- Migration fan-out becomes an operational bottleneck.

