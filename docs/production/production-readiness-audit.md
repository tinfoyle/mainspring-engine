# Spyglass production-readiness audit

- Audit date: 2026-08-18
- Scope: production Go module, public website, PostgreSQL migrations, CI, reference Kubernetes topology, and production runbooks
- Verdict: **ready for a connected staging environment; not ready for customer production traffic**

This verdict separates executable software from environment evidence. The repository now has a strong security and modularity foundation, but a successful local suite and review-only manifests are not substitutes for live Stripe/SMTP, applied cluster policy, observability, load, restore, accessibility, and canary evidence.

## What is executable now

| Area | Current evidence | Readiness |
|---|---|---|
| Public website | Infinite Ocean/Spyglass routes, responsive build, rendered-route tests, strict edge browser policy, same-origin public Catalog proxy, illustrative fallback, GET-only private signup handoff, exact-artifact deployed-origin certification command | Staging candidate |
| Identity and Accounts | Free registration, verification, password/passkey login, recovery, sessions, multi-Account Memberships, owner security policy, invitations, role/lifecycle operations | Staging candidate |
| Catalog and packages | Immutable publications, governed plans/offers/packages/limits, four-eyes administration, local entitlement snapshots, effective-offer validation | Staging candidate |
| Stripe boundary | Server-created Checkout/Portal, allowlisted local offers, signed raw webhooks, durable inbox, asynchronous projection/reconciliation, operator replay/refresh | Connected provider exercise required |
| Global/cell isolation | Signed request-bound routing, placement generation, forced RLS, constrained roles, replay receipts, two-cell PostgreSQL integration contracts | Applied managed-database exercise required |
| Work | Typed lifecycle, routed CRUD/assignment, capacity admission, reconciled release, browser surface, Account-scoped persistence | Partial product package |
| Agents and runners | Boardrooms/personas/runs, immutable plans, dispatch/projection workers, encrypted runner exchange, workload identity, one-use capabilities, action ledger | Partial product package |
| Kubernetes | Shared workload classes, dedicated runner namespace, least-privilege RBAC, default-deny policies, PDB/topology/HPA contracts, exact content-free worker metric endpoints | Review-only base; adapter and overlay still required |
| Data lifecycle | Logical closure, governed erasure preparation/execution, restore quarantine and signed database replay | External-store and real restore evidence missing |
| Quality | Full Go, race, vet, vulnerability, website build/lint/render, npm production audit, PostgreSQL migration, manifest verification, static scratch-image build, runtime identity, SBOM/provenance, signed-release workflow contracts, and bounded content-free Account-read load evidence tooling in CI | Strong repository evidence; connected release run required |

## Findings closed during this audit

1. The public Catalog no longer discloses a scheduled future offer before its effective time. Public rendering, registration, and billing now use the same effective-offer rule.
2. Architecture enforcement found two real dependency inversions: an application canary and an infrastructure adapter imported transport packages for header constants. The constants now live in the platform security contexts and the inversions are removed.
3. A repository architecture test now fails CI if production modules point inward to adapters/transports/bootstrap, if application code imports adapters/transports, or if production code imports the prototype.

## Launch blockers

### P0 — required before any customer production traffic

1. **Connected billing proof.** Run a real Stripe test-mode journey for Checkout, signed webhook delivery, duplicate and out-of-order events, failure, recovery, cancellation, Portal, reconciliation mismatch, replay, and refresh. Archive provider event IDs and local audit evidence without card data.
2. **Deployable environment overlay.** Supply digest-pinned images, managed secrets/certificates, exact database/provider/Kubernetes API egress, the sandbox RuntimeClass, ingress routes, metrics adapter, Pod monitors, alerts, and connection caps. The checked-in reference must remain free of literal credentials.
3. **Applied isolation and resilience proof.** Exercise non-owner forced-RLS roles, cross-Account attacks, pod/node loss, route-key and CA rollover, runner compromise assumptions, queue recovery, one-hot-Account fairness, and downstream saturation in the target cluster and managed databases.
4. **Observability and incident response.** The runtime now publishes bounded-cardinality HTTP counters/durations and content-free worker/HPA metrics. Add OpenTelemetry trace export with safe Account/cell correlation, queue-age and projection-lag dashboards, paging thresholds, ownership, and executable runbooks. Complete a staging burn-in and one game day.
5. **Restore and privacy proof.** Restore global and cell backups into quarantine, verify checkpoint fencing and signed replay, exercise Account export/erasure across every configured external store, and archive retention/attestation evidence.
6. **Customer-journey certification.** Test deployed-origin signup, real email verification/recovery, passkeys on supported devices, multi-Account switching, paid/downgrade flows, keyboard/screen-reader use, responsive layouts, and failure recovery. Resolve legal, privacy, consent, subprocessors, support, and status ownership.
7. **Release supply chain.** The static scratch image and commit-pinned multi-architecture SBOM/provenance/Cosign release workflow are executable. Produce the first reviewed digest, archive verification and scan evidence, enforce its workflow identity at admission, and rehearse a staged rollback between two retained artifacts.

### P1 — required for the advertised product scope

1. Finish the user-facing Agents conversation/orchestration, schedules, attachments, and customer retry/manual-resolution paths. Dispatch/projection dead-letter inspection and exact-target operator requeue are executable; they still require staging role and runbook rehearsal.
2. Complete Knowledge before claiming source-attributed business memory; complete Finance and Marketing before advertising those packages as usable rather than preview/locked surfaces.
3. Decide first-party-versus-partner publication policy and produce a sanitized published OpenAPI artifact. All 56 customer operations now have exact success contracts and generated types; real development-handler responses are validated against resolved OpenAPI schemas; the public pricing boundary has a complete runtime-validated Catalog fixture; and route, owner, authentication, reference, no-generic-response, backward-compatibility, Go/TypeScript generation, and Go transport drift checks are executable.
4. Complete downgrade read-only/export/retention/restoration behavior for every package and transport, including background work and agent capabilities.
5. Implement and rehearse Account movement between cells, including copy/change capture, reconciliation, placement switch, rollback window, and source retirement.

## Ordered closeout plan

### Gate 1 — connected staging

- Build digest-pinned artifacts from the committed revision.
- Create one global database, two cell databases, Stripe test mode, TLS SMTP, workload certificates, and the environment overlay.
- Apply migrations with constrained runtime roles; publish a reviewed Catalog and Stripe mappings.
- Prove public website -> verification email -> free Account -> owner security enrollment -> Stripe Checkout -> signed projection -> package access.
- Exit only when restart, duplicate-delivery, wrong-origin, wrong-Account, stale-placement, and provider-outage cases are captured as repeatable tests.

### Gate 2 — operational platform

- Instrument every request/queue/run with safe request, operation, Account, cell, and workload correlation; never attach customer content or credentials.
- Deliver custom metrics used by the HPA contracts and define downstream database/provider ceilings.
- Run the reviewed [many-small-Account and one-hot-Account certification](load-certification.md), plus queue-backlog, pod-loss, node-loss, database-failover, and provider-degradation tests.
- Exit only when SLO dashboards, alerts, capacity limits, and recovery runbooks agree with observed behavior.

### Gate 3 — product and contract completion

- Finish the package capabilities being sold and keep incomplete packages unpublished or explicitly preview-only.
- Establish OpenAPI and generated-client ownership, contract compatibility, accessibility targets, analytics consent, and support workflows.
- Exercise upgrade, downgrade, cancellation, failed payment, grace/recovery, invitation, ownership transfer, closure, export, and deletion end to end.
- Exit only when every public claim maps to an enabled entitlement, an executable use case, and a named acceptance test.

### Gate 4 — release candidate and canary

- Complete external security/privacy review and close or explicitly accept every finding.
- Perform clean-environment and restored-environment release suites; archive evidence and artifact identities.
- Roll out internal then canary Accounts by cohort with mismatch, billing, queue, and isolation monitors.
- Exit only after the agreed observation window has no unresolved mismatch, isolation, duplicate-effect, restore, or accessibility failures.

## Go/no-go rule

Production launch is a **no-go** while any P0 item lacks current evidence. “Current” means evidence from the exact release artifact and target environment, not a local mock, an earlier image, or a review-only manifest. P1 scope may be deferred only by removing the corresponding public offer/claim and keeping its entitlement unavailable.
