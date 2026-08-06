# ADR-0002: Separate the control plane from tenant runtimes

Status: Proposed  
Date: 2026-08-06

## Context

The public Mainspring experience handles marketing, purchasing, billing, and provisioning. A purchased boardroom contains private business records, documents, integrations, schedules, and conversations. Customers access their boardroom using a tenant-specific subdomain and a separate login.

Mixing these responsibilities would give an internet-facing billing application broad access to every customer's operational data and would make independent tenant placement difficult.

## Decision

Separate Mainspring into a control plane and tenant runtimes.

The control plane owns:

- Public and account-area identities
- Billing references and subscription state
- Tenant registry, subdomains, placement, and runtime health
- Provisioning and lifecycle operations
- Platform-level operational audit events

Each tenant runtime owns:

- Its own users, sessions, and invitations
- Boardrooms, personas, messages, schedules, approvals, and tools
- Documents, RAG data, integrations, and business records
- Tenant-level audit events

Tenant requests enter through a gateway that resolves the request hostname to a registered tenant runtime. The gateway may cache routing data but has no tenant database credentials. The tenant runtime authenticates the tenant-specific session.

Tenant session cookies must be host-only. The `Domain` attribute must not be set to the parent Mainspring domain. For every authenticated request, the effective hostname, authenticated tenant, and runtime tenant must match.

The initial owner account is established with a single-use invitation created after successful provisioning. A front-of-house account does not automatically become a tenant session.

## Consequences

- A compromise of the control-plane application does not automatically grant direct database access to customer business data.
- The same email address may represent separate identities in the account area and one or more tenant runtimes.
- Cross-plane operations require explicit commands and status records rather than direct table access.
- Support tooling must use audited, scoped access paths rather than a universal tenant credential.
- Tenant placement can later move between hosts or Kubernetes clusters without changing the public account service.

## Alternatives considered

- **Single application and shared session across all subdomains:** rejected because it weakens the tenant boundary and expands the control plane's blast radius.
- **One central identity session valid for every tenant:** deferred. It may improve convenience later, but requires carefully audience-bound tokens and explicit tenant switching.
- **Direct Caddy configuration per tenant:** possible, but a tenant gateway provides one controlled hostname-to-runtime resolution point.

## Revisit when

- Enterprise single sign-on or cross-tenant user administration is required.
- The product needs one user to move frequently between multiple tenant organizations.
- Tenant runtimes move to separate regions or clusters and routing requirements change.

