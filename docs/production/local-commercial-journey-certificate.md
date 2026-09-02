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
   grant; and
9. the logged-in customer can read the resulting billing and token state.

The `invoice.paid` fixture deliberately contains `status: "paid"` and omits the
deprecated `paid` boolean. This keeps the Stage regression in permanent coverage.

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
