# Spyglass production-readiness audit

- Audit date: 2026-08-18
- Scope: production Go module, public website, PostgreSQL migrations, CI, reference Kubernetes topology, and production runbooks
- Verdict: **ready for a connected staging environment; not ready for customer production traffic**

This verdict separates executable software from environment evidence. The repository now has a strong security and modularity foundation, but a successful local suite and review-only manifests are not substitutes for live Stripe/SMTP, applied cluster policy, observability, load, restore, accessibility, and canary evidence.

## What is executable now

| Area | Current evidence | Readiness |
|---|---|---|
| Public website | Infinite Ocean/Spyglass routes, responsive build, shared accessible landmark/skip-link frame, rendered DOM axe-core A/AA checks, strict edge browser policy, same-origin public Catalog proxy, illustrative fallback, GET-only private signup handoff, exact-artifact deployed-origin certification command, and exact-build multi-engine landing/pricing consent and acquisition checks | Staging candidate; complete browser/device matrix required |
| Identity and Accounts | Free registration, verification, password/passkey login, recovery, verified primary-email change, sessions, multi-Account Memberships, owner security policy, invitations, role/lifecycle operations, and a 20-state private rendered-structure accessibility contract | Staging candidate; live SMTP plus private browser/assistive-technology matrix required |
| Catalog and packages | Immutable publications, governed plans/offers/packages/limits, four-eyes administration, local entitlement snapshots, effective-offer validation | Staging candidate |
| Stripe boundary | Server-created Checkout/Portal, allowlisted local offers, signed raw webhooks, durable inbox, asynchronous projection/reconciliation, operator replay/refresh | Connected provider exercise required |
| Global/cell isolation | Signed request-bound routing, placement generation, forced RLS, constrained roles, replay receipts, two-cell PostgreSQL integration contracts | Applied managed-database exercise required |
| Work | Typed lifecycle, routed CRUD/assignment, capacity admission, reconciled release, browser surface, Account-scoped persistence | Partial product package |
| Agents and runners | Boardrooms/personas/runs, immutable plans, dispatch/projection workers, encrypted runner exchange, workload identity, one-use capabilities, action ledger | Partial product package |
| Kubernetes | Shared workload classes, dedicated runner namespace, least-privilege RBAC, default-deny policies, PDB/topology/HPA contracts, exact content-free worker metric endpoints | Review-only base; adapter and overlay still required |
| Data lifecycle | Logical closure, governed erasure preparation/execution, restore quarantine and signed database replay | External-store and real restore evidence missing |
| Quality | Full Go, race, vet, vulnerability, website build/lint/render, full npm dependency audit, PostgreSQL migration, manifest verification, static scratch-image build, runtime identity, SBOM/provenance, signed-release workflow contracts, bounded content-free read/write evidence tooling, and content-safe OTLP/HTTP tracing in CI | Strong repository evidence; connected release run required |

## Findings closed during this audit

1. The public Catalog no longer discloses a scheduled future offer before its effective time. Public rendering, registration, and billing now use the same effective-offer rule.
2. Architecture enforcement found two real dependency inversions: an application canary and an infrastructure adapter imported transport packages for header constants. The constants now live in the platform security contexts and the inversions are removed.
3. A repository architecture test now fails CI if production modules point inward to adapters/transports/bootstrap, if application code imports adapters/transports, or if production code imports the prototype.

## Launch blockers

### P0 — required before any customer production traffic

1. **Connected billing proof.** Run a real Stripe test-mode journey for Checkout, signed webhook delivery, duplicate and out-of-order events, failure, recovery, cancellation, Portal, reconciliation mismatch, replay, and refresh. Archive provider event IDs and local audit evidence without card data.
2. **Deployable environment overlay.** Supply digest-pinned images, managed secrets/certificates, exact database/provider/Kubernetes API egress, the sandbox RuntimeClass, ingress routes, metrics adapter, Pod monitors, alerts, and connection caps. The checked-in reference must remain free of literal credentials.
3. **Applied isolation and resilience proof.** Exercise non-owner forced-RLS roles, cross-Account attacks, pod/node loss, route-key and CA rollover, runner compromise assumptions, queue recovery, one-hot-Account fairness, and downstream saturation in the target cluster and managed databases.
4. **Observability and incident response.** The runtime now publishes bounded-cardinality HTTP counters/fixed histograms, content-free worker/HPA metrics, and opt-in bounded OTLP/HTTP traces with HMAC-pseudonymous Account/cell correlation. A binary-validated, mTLS, allowlisting collector gateway plus version-controlled queue/SLO/collector rules, dashboard, and response procedures are checked in. Apply them with a real backend, authenticated application scrape, paging route, and exact egress; add the remaining billing/isolation/database/restore signals and worker/database/controller spans. Complete a staging burn-in and one game day.
5. **Restore and privacy proof.** Restore global and cell backups into quarantine, verify checkpoint fencing and signed replay, exercise Account export/erasure across every configured external store, and archive retention/attestation evidence.
6. **Customer-journey certification.** Test deployed-origin signup, real email verification/recovery/verified-contact change, passkeys on supported devices, multi-Account switching, paid/downgrade flows, keyboard/screen-reader use, responsive layouts, and failure recovery against the exact matrix and evidence rules in [Accessibility target and release certification](accessibility-certification.md). All public routes pass both layout-independent and browser-computed DOM/axe gates, and 39 private Vue fixture scans cover the shell plus all eighteen view families. A repeatable exact-build Playwright job now supplies 291 applicable checks for the complete public landing/feature/pricing/policy inventory plus private Your Turn, Checkout, GDPR Privacy, fail-closed Affiliate, authoritative owner-security onboarding, Account authority, Billing, portability, closure, Work, Knowledge, Business Baseline, Agents/Boardrooms/conversations, Schedules, Finance, Integration connections/executions and Marketing campaigns/releases in Chromium, Firefox and WebKit desktop; Chromium phone emulation at 360×800, 390×844 and 412×915; Chromium reflow at 320×800; a touch-enabled Chromium tablet at 768×1024; and Chromium forced-colors and reduced-motion emulation at 1280×900 with an asserted motion media state; a separate Chromium profile enlarges text to 200% at 1280×900 across seventeen primary private and seventeen public routes. That matrix now also proves tracked Affiliate-scoped privacy-rights submission, deduplication, cancellation and passkey handoff, server-confirmed multi-Account switching, failed-switch restoration, read-only evidence preservation, absent-package upgrade boundaries, recoverable Account-load failure, capacity-blocked draft retention, offline queue retention with authoritative reconnect, exact session-expiry sign-in return, one-mutation enforcement during pending Your Turn decisions, authoritative Work, Marketing, Membership and Schedule conflict reloads, safe Checkout provider failure, and scoped Integration-provider failure with retained customer intent. A separate connected-local job supplies 12 checks for the exact Go-rendered login, signup, password recovery/reset, registration-verification and verified-contact boundary across the same four profiles without creating a User or submitting a password. Browser-computed axe, overflow, consent/event gating, validation and focus checks found and closed five contrast defects, a governed-identifier overflow, an unreachable scrollable progress track, an unmeasured persistent navigation CTA, a consent card that covered signup's primary action, and fixed-count private/public grids that overflowed under text enlargement. Successful identity and owner-enrollment ceremonies, remaining package-domain mutation conflicts, capacity denials and downstream failures, 200% text-only zoom on connected identity pages, 400% browser zoom/reflow, authenticated provider journeys and physical passkey ceremonies, physical devices and assistive technologies remain open. Resolve legal, privacy, consent, subprocessors, support, and status ownership.
7. **Release supply chain.** The static scratch image and commit-pinned multi-architecture SBOM/provenance/Cosign workflows produced the first reviewed RC.2 application/website pair. Archive independent Cosign and vulnerability/secret-scan evidence, enforce workflow identity at admission, retain a second compatible pair, and rehearse staged rollback between them.

Current local recovery and Affiliate amendment (2026-08-25): the exact-build matrix supplies 331 applicable passes and eleven intentional profile skips. In addition to the recorded conflict, capacity and downstream paths, it proves active Affiliate aggregate/recurring-ledger presentation without referred-customer identity, fail-closed suspended-code behavior with retained history/appeal access, and consent-independent Checkout recovery after backend self-referral denial. The Go projector now explicitly covers a final lost-dispute reversal; service and connected-PostgreSQL evidence already covers multi-Account self-referral denial, invoice replay, recurring cycles, Refund reversal/replay, adverse-before-earning reconciliation, suspension and immutable customer-detached history. The connected-local identity matrix supplies thirteen passes including 200% text across all six identity routes. Remaining local certification is 400% browser zoom/reflow, provider-backed journeys, physical devices and assistive technologies. Affiliate flags and settlement remain closed. No identity, credential, provider, database or deployed environment was used for the new browser evidence.

### P1 — required for the advertised product scope

1. Finish interactive Agents orchestration beyond the executable Conversation/Message reads, immutable customer retry/accept decisions, and one ordered Persona pass, plus schedules, attachments, and governed consequential adapters. Dispatch/projection dead-letter inspection and exact-target operator requeue are executable; they still require staging role and runbook rehearsal.
2. Complete Knowledge before claiming source-attributed business memory; complete Finance and Marketing before advertising those packages as usable rather than preview/locked surfaces.
3. Decide first-party-versus-partner publication policy and produce a sanitized published OpenAPI artifact. All 182 customer operations now have exact success contracts and generated types; real development-handler responses are validated against resolved OpenAPI schemas; the public pricing boundary has a complete runtime-validated Catalog fixture; and route, owner, authentication, reference, no-generic-response, backward-compatibility, Go/TypeScript generation, and Go transport drift checks are executable.
4. Complete downgrade read-only/export/retention/restoration behavior for every package and transport, including background work and agent capabilities.
5. Rehearse the implemented PostgreSQL Account-movement workflow in connected stage and extend its handler inventory to every Phase 3 external store before production; retain evidence for queue-gated freeze, high-water-mark reconciliation, placement switch, rollback window and source retirement.

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
- Run the reviewed [many-small-Account and one-hot-Account read certification](load-certification.md) and [deterministic Work write/replay certification](write-certification.md), plus queue-backlog, pod-loss, node-loss, database-failover, and provider-degradation tests.
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
