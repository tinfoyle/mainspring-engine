# ADR-0009: Adopt a minimum viable security boundary

Status: Proposed  
Date: 2026-08-06

## Context

The MVP should not over-invest in infrastructure that may change after product validation. It will nevertheless process customer communications, documents, financial records, credentials, and untrusted content. Several security boundaries are inexpensive to establish initially and costly to retrofit after tenants and integrations exist.

## Decision

Adopt the following minimum baseline for the MVP:

### Host and containers

- Only the edge proxy publishes public ports.
- Internet-facing containers do not receive Docker authority.
- Containers run as non-root where supported, without privilege, with unnecessary capabilities removed.
- Tenant and agent workloads have CPU, memory, process, disk, and execution-time limits.
- PostgreSQL, Temporal, the provisioner, RAG, and tenant services remain on private Docker networks.

### Identity and tenancy

- Tenant sessions use host-only, `Secure`, `HttpOnly`, and `SameSite` cookies.
- State-changing browser requests use CSRF protection.
- Hostname, session tenant, runtime tenant, and target resource tenant must agree.
- Tenant database credentials are unique and unavailable to the control plane, gateway, and agent runners.

### Agents and tools

- Agent runners are ephemeral and receive no Docker, database, or tenant OAuth credentials.
- Tool access uses short-lived capability tokens and server-side authorization.
- Model and tool outputs are treated as untrusted input and validated before use.
- Consequential operations require action-ledger checks and approval where configured.

### Data and operations

- Secrets and sensitive payloads are redacted from logs.
- Audit events record tenant, actor, run, action, and correlation identifiers.
- Backups are encrypted and stored outside the VPS failure domain.
- Restore procedures are tested, not merely documented.
- Dependencies and base images are pinned and updated through controlled rollouts.

Advanced hardening such as rootless Docker, dedicated tenant hosts, signed images, strict egress proxies, intrusion detection, and Kubernetes network policy is deferred until usage or threat modeling justifies it.

## Consequences

- The MVP retains a comprehensible security model without requiring a full platform-security program.
- Some operational work is required before the first customer, especially backup restoration, secret redaction, and tenant-isolation testing.
- Docker containers are treated as process-isolation boundaries, not equivalent to separate physical hosts.
- Deferred controls are explicit and can be prioritized using observed usage and risk.

## Alternatives considered

- **Defer all security work until after usage:** rejected because tenant identity, credential placement, and action semantics are architectural boundaries.
- **Implement enterprise-grade isolation immediately:** rejected because dedicated hosts, comprehensive policy infrastructure, and Kubernetes would delay MVP learning.

## Revisit when

- The first external customer is onboarded.
- Financial execution beyond drafting or approval is enabled.
- A penetration test or threat model identifies a higher-priority control.
- The platform moves to multiple hosts or Kubernetes.

