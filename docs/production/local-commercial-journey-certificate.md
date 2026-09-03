# Local commercial journey certificate

## Purpose

`deploy/docker/spyglass/verify-commercial-journey.sh` is the repeatable release
gate for the customer path from a new email address to paid access. It runs only
inside an isolated `ubunturojo` Docker Compose project and never contacts Stripe,
Stage, production, or a public email provider.

The certificate proves:

1. a unique customer registers through the real Account API;
2. Mailpit receives the verification email and the link completes registration;
3. password login, required owner security enrollment, recovery codes, and Account
   selection succeed;
4. the real checkout endpoint creates the configured `$50 USD / month` session;
5. the local Stripe fixture presents the hosted checkout and signs current Stripe
   event shapes with the configured webhook secret;
6. out-of-order invoice, subscription, and checkout events are accepted;
7. an exact provider replay remains idempotent;
8. the billing worker projects one active subscription, every package in the
   current Team plan, and exactly one current Catalog-defined included AI Token
   grant;
9. the logged-in customer can read the resulting billing and token state;
10. the first real Baseline answer passes through the same owner-statement,
    Knowledge evidence, claim acceptance, durable Fact and answer-binding chain
    used by the Vue application;
11. that capture deliberately presents a timestamp 30 seconds ahead of the Cell,
    proving bounded browser clock skew is normalized without weakening rejection
    of unreasonable future evidence;
12. that customer completes a real Business Baseline from interview through
    generated inventory, explicit gap review, frozen plan approval, and
    capacity-governed Work materialization;
13. the generated Work item retains its Baseline provenance, moves through
    `open`, `in_progress`, and `done`, and is paired with exact owner-reviewed
    Knowledge evidence; and
14. the customer confirms that evidence, marks the Baseline ready, and reloads
    the same durable ready assessment with its reassessment date intact.

The `invoice.paid` fixture deliberately contains `status: "paid"` and omits the
deprecated `paid` boolean. This keeps the Stage regression in permanent coverage.

The Baseline leg also deliberately submits a changed plan digest and requires a
`422` rejection before approving the exact frozen plan. Its Work creation crosses
the Knowledge-owned Baseline and Work-owned capacity boundaries using separately
authorized package grants. The broker accepts deterministic child Work operations
only for signed Baseline plan or maintenance materialization routes; ordinary Work
commands remain bound to their exact top-level operation ID.

The companion Chromium journey in
`ui/tests/browser/private-launch-critical.spec.ts` drives the same customer-visible
sequence through the Vue application, including Work completion, evidence binding,
ready-state reload, overflow checks, and axe accessibility inspection. That browser
test uses a stateful synthetic API so presentation failures stay easy to isolate;
the Compose certificate above is the connected HTTP/database proof.

## Run it

From `ubunturojo`:

```bash
cd /mnt/c/Users/Tinfo/Documents/Mainspring/deploy/docker/spyglass
make verify-commercial-journey
```

To retain a content-free JSON certificate:

```bash
./verify-commercial-journey.sh --out /tmp/spyglass-commercial-journey.json
```

The script owns the `spyglass-commercial-journey` Compose project. It removes
that project's disposable containers, networks, and volumes before a run and
again after success. On failure it leaves the project running and prints the
relevant service logs for diagnosis.

## Safety boundary

The application accepts `SPYGLASS_STRIPE_BASE_URL` only for `local` and
`local-secure`, and only when the host is `stripe-fixture`, `localhost`, or a
loopback address. Stage and production fail startup if an override is supplied.
The fixture is a separate Docker target and is not present in the production
application image.

## Baseline release gate

Use the focused gate before any Stage deployment that changes Baseline,
Knowledge, Work, routing, entitlements, authentication, or the private UI:

```bash
cd /mnt/c/Users/Tinfo/Documents/Mainspring/deploy/docker/spyglass
make verify-baseline-journey
```

That one command combines focused domain/API regressions, the full Vue Baseline
workflow with accessibility and overflow checks, and this connected disposable
Docker journey. It therefore catches presentation/state-machine failures and
real cross-package persistence failures without contacting Stage or production.
