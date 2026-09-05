# Stage traffic and admin verification — 2026-09-05

## Deployed result

- Release: `0.3.0-rc.46`, source `81383e20e275c5eda65c37a6a0308aa0c6a9e423`.
- Manifest and immutable active checkout: `fdbf6bd16140ffdc2ad46d494fe2897092dd3c7d`.
- All four image identities match `deploy/releases/0.3.0-rc.46.env`; each passed
  the pinned Trivy high/critical vulnerability and secret gate and carries
  BuildKit SBOM/provenance.
- 51 running containers, 50 healthy, zero unhealthy; internal Caddy has no health
  check. Global/cell migration versions are `69/86/86`.
- The protected `2026-08-31-01/stage.env` was unchanged. The initial pull attempt
  stopped before updating services because registry authorization was absent.
  A temporary root-only credential under `/run/spyglass-rc46-docker` enabled the
  pull and was removed after successful deployment.
- The admin host was added by validated Caddy reload at 04:02:34 UTC. Backup:
  `/opt/infiniteocean/caddy/Caddyfile.bak.20260905T040234Z.pre-stage-admin`.

## Original failure and repair

Neither Caddy edge wrote access logs, and the Stage service logs did not contain
visitor IPs. Traffic before logging began is unrecoverable. Access logging began
at 02:18:57 UTC, before the application deployment. The protected host directory
survives application replacements. Its initial configuration backup is
`/opt/infiniteocean/caddy/Caddyfile.bak.20260905T021831Z.pre-stage-access`.

The earlier analytics ingestion and aggregate-report functions existed, but the
separate staff console was not deployed to Stage. RC.46 deploys it and provides
clear distinction between all HTTP requests and optional consented product
events. Small analytics cohorts are explicitly described as suppressed groups,
not a zero-visitor count. The UI has separate modules and a usage guide.

## Verification evidence

- Complete Go suite passed. All PostgreSQL integration cases passed after
  updating the migration-ledger expectation from 156 to 157 and rerunning the
  affected isolation test. Dedicated tests prove traffic authority, immutable
  audit creation, role revocation, lack of raw-table access and no PUBLIC execute.
- Generated contracts, UI workspace type checks, unit tests and lint passed.
  Operations build passed. Desktop and 360-pixel phone report workflows passed
  accessibility and overflow checks; screenshots were visually reviewed.
- Log-reader tests cover current and gzip-rotated logs, trusted peer IP selection,
  IPv4 normalization, excluded secrets, symlinks, missing collection, bounded
  lists, newest-200 ordering and incomplete-collection warnings.
- Live probes appended to public, app, MCP and admin logs. Query/header marker
  values were absent; headers, URI and TLS fields were absent; a forged
  `X-Forwarded-For` value did not replace the peer address. All files are 0640,
  group 65532, under a 2750 directory. The API runs as 65532:65532 with a read-only
  root, one read-only log mount and no published port. A separate non-root
  certificate successfully read all four mounted files.
- Live traffic requests returned 401 without a staff session, 403 from the
  customer origin and 404 when sent to the customer host. The admin HTML itself
  returns 200 and exposes only the passkey sign-in screen when unauthenticated.
- A real Chromium WebAuthn ceremony used the app RP ID from the separate admin
  origin through the exact related-origin document. An unregistered synthetic
  passkey was rejected and no admin session cookie was issued. It was never
  enrolled into any account. Cryptographic application tests separately verify
  signatures and reject other origins and replay.
- The live analytics certificate used five anonymous test browsers. Public
  `signup_handoff_started`, private `registration_started` and mirrored conversion
  `registration_started` each produced five events from five subjects through
  the restricted reporter role. No-consent and withdrawn-consent ingestion
  returned 403. Cleanup erased all synthetic subjects and left zero raw or
  conversion test events. This certificate passed again after deployment.

## Remaining owner acceptance

The owner account still has **zero enrolled passkeys**, and Stage has **zero
active staff users**. Offline assignment requires an existing passkey. The owner
must enroll Windows Hello or a security key at the app's Security page; then an
owner-authorized operator can assign `operations_administrator` and complete a
successful real admin sign-in and authenticated live report review. These positive
owner workflows are **not yet certified**. No credential or session was fabricated
to work around this requirement.

Physical authenticator and assistive-technology acceptance remain human checks.
No remote log archive or automatic collection-failure alert was added. Retention
is bounded by size and rotated-file settings; quiet active files can contain
older records. Requests include test traffic and bots; unique IPs are not people.
See the [admin guide](stage-admin-guide.md) for operation and exact limitations.
