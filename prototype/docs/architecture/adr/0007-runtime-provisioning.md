# ADR-0007: Abstract runtime provisioning

Status: Proposed  
Date: 2026-08-06

## Context

The MVP runs on one Docker-capable Hostinger VPS. After meaningful usage, the platform is expected to move to Kubernetes. Provisioning logic embedded directly in billing handlers or Docker Compose shell commands would make that migration expensive and would place Docker authority in an internet-facing process.

## Decision

Introduce a runtime-provisioning boundary:

```go
type RuntimeProvisioner interface {
    ProvisionTenant(context.Context, TenantSpec) (Runtime, error)
    UpgradeTenant(context.Context, TenantID, ImageVersion) error
    SuspendTenant(context.Context, TenantID) error
    ResumeTenant(context.Context, TenantID) error
    DestroyTenant(context.Context, TenantID) error
}
```

The MVP implements `DockerProvisioner`. A future deployment implements `KubernetesProvisioner` without changing billing, tenant, or boardroom domain code.

The Docker provisioner runs as a private local daemon and is the only Mainspring process with Docker authority. It accepts typed operations over a protected local transport. It does not accept arbitrary image names, Compose documents, host paths, environment variables, or shell fragments from callers.

Provisioning and lifecycle operations run as Temporal workflows and are idempotent. Runtime resources use immutable tenant UUIDs and standard labels. Images are pinned to immutable versions or digests.

Tenant placement includes a `runtime_host_id` from the beginning. Host records expose health, capacity, supported image versions, and last heartbeat even while only one host exists.

## Consequences

- Docker access is isolated from public web services.
- Kubernetes migration becomes a new infrastructure adapter rather than a domain rewrite.
- The platform must maintain a tenant runtime registry and reconcile desired state with actual resources.
- Upgrade, suspend, restore, move, and delete workflows are first-class lifecycle operations.
- Host-specific paths and container IP addresses cannot become durable business identifiers.

## Alternatives considered

- **Mount the Docker socket into the control plane:** rejected because compromise of an internet-facing service would grant broad host control.
- **Generate and execute arbitrary Compose files in request handlers:** rejected because it is difficult to validate, audit, retry, and migrate.
- **Start directly on Kubernetes:** rejected because its cost and operational surface are unnecessary before MVP usage.

## Revisit when

- The first additional runtime host is introduced.
- Kubernetes design begins.
- Tenant placement needs region, compliance, or dedicated-host constraints.

