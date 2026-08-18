# Staging release certification

Status: executable anonymous-boundary gate; connected customer/provider and resilience stages still require a target environment

Production evidence must identify the exact source revision, immutable image, deployed origins, and published Catalog it certifies. A screenshot, a successful local test, or an unversioned statement that staging “looks good” is not release evidence.

## Anonymous boundary gate

`cmd/staging-cert` is a non-mutating, standard-library-only probe for the public Infinite Ocean and Spyglass boundaries. It:

- requires separate exact HTTPS website and application origins;
- binds evidence to a full Git revision and immutable image digest;
- checks every anonymous information route for HTML, the production browser-security policy, and absence of the historical product name;
- proves `/signup` is a GET-only handoff to the private application origin and does not collect identity fields on the marketing site;
- checks both the same-origin website Catalog proxy and Account API Catalog;
- requires an exact Catalog version and reviewed package/offer set;
- rejects provider references in public Catalog JSON;
- records status, duration, content length, and SHA-256 only—never response bodies, cookies, credentials, or customer identifiers; and
- creates a new `0600` evidence file and refuses to overwrite earlier evidence.

Run from the exact release checkout after DNS, TLS, ingress, website, Account API, Catalog publication, and image promotion are complete:

```text
go run ./cmd/staging-cert \
  -environment staging \
  -website-origin https://staging.infiniteocean.net \
  -app-origin https://app.staging.infiniteocean.net \
  -revision <40-character-git-revision> \
  -image-digest sha256:<64-lowercase-hex-characters> \
  -catalog-version 2 \
  -packages knowledge,work,agents,finance,marketing,integrations \
  -offers free-v1,team-monthly-v1,operating-monthly-v1 \
  -output evidence/staging-anonymous-<release>.json
```

The reviewed environment may intentionally publish a smaller package/offer set; the command input must describe the approved release claim, not automatically discover and bless whatever happens to be deployed. A nonzero exit or any `passed: false` check blocks promotion. Archive the evidence outside the repository beside the signed release manifest.

## Connected certification sequence

The anonymous gate is necessary but insufficient. Run the remaining stages against the same revision and image digest in this order:

1. **Identity and Account journey.** Create a new identity through the public handoff; receive a real TLS email; verify; enroll a passkey and recovery codes; create/join/switch Accounts; exercise invitation, ownership, session revocation, and recovery on supported browsers and devices. Record synthetic IDs in the restricted test record, not the anonymous evidence artifact.
2. **Stripe test mode.** Upgrade an Account through server-created Checkout, deliver the signed webhook, observe asynchronous projection, then exercise duplicate, out-of-order, failed-payment, recovery, cancellation, Portal, reconciliation mismatch, exact-event replay, and current-subscription refresh. Preserve Stripe test event IDs and immutable operator authorization IDs without payloads or card data.
3. **Isolation and load.** Use the bounded read driver in [load-certification.md](load-certification.md) and the separate deterministic Work write/replay driver in [write-certification.md](write-certification.md) to run many-small-Account and one-hot-Account profiles through at least two router and cell replicas. Record latency histograms, errors, queue ages, database connections, HPA decisions, durable row/capacity invariants, and fairness. Include wrong-Account, wrong-cell, stale-placement, stale-entitlement, replay, and downstream-saturation attacks. The content-free client reports do not replace coincident cluster evidence.
4. **Failure and recovery.** Remove a router pod, cell pod, runner node, provider path, and database primary in controlled exercises. Verify bounded retries, no cross-cell fallback, durable queue recovery, one-use capability behavior, and documented degraded modes.
5. **Restore.** Restore global and every participating cell backup into quarantine, demonstrate readiness failure at the pinned erasure checkpoint, replay every archived signed directive in order, and rerun the release and isolation suites before allowing ingress.
6. **Accessibility and responsive certification.** Follow [Accessibility target and release certification](accessibility-certification.md) across public acquisition and private identity/Account/Work/Agents journeys. Archive the rendered-DOM and real-browser automated reports plus keyboard, focus, screen-reader, zoom/reflow, reduced-motion, forced-colors, contrast, error-recovery, and supported-device results. The repository axe/jsdom and lint gates are regression evidence, not conformance evidence.
7. **Canary.** Exercise candidate route key and workload certificates using the dedicated canary Account, deploy an internal cohort, then a bounded customer cohort. Observe billing mismatch, authorization denial, queue, latency, and isolation signals for the agreed window before broader promotion.

Each stage must append or link a signed/immutable result to the release record. A rerun after code, image, Catalog, policy, secret, ingress, database migration, or target-environment changes is a new certification; evidence is not transferable between artifacts.

## Data handling

Certification tools must default to content-free output. Do not archive session cookies, passwords, passkey material, recovery codes, email bodies, Stripe payloads, Account business content, model prompts/results, database URLs, certificate private keys, or secret environment values. Store any synthetic Account/provider identifiers needed for reconciliation in the restricted staging test system with a retention deadline and named owner.
