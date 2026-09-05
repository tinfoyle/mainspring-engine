# Stage admin authenticator verification — 2026-09-05

Candidate: 0.3.0-rc.48. The owner explicitly authorized Google plus an
independent authenticator code as the admin authentication route.

## Local evidence

- Full Go suite passed.
- Full PostgreSQL migration/integration suite passed against the disposable
  UbuntuRojo PostgreSQL instance (global 70; combined test ledger 158).
- RFC 6238 vectors, freshness, replay and encrypted handoff binding tests passed.
- Restricted-role PostgreSQL authentication journey passed: Google subject
  lookup, first enrollment, stable pending setup on refresh, encrypted secret
  storage, concurrent code replay, per-user attempt limits across challenges,
  recovery consumption, old-session revocation, replacement, recovery-code
  invalidation, reauthentication, staff revocation and immutable audit.
- Operations API denial tests passed for Google-only, password, email-code and
  legacy passkey sessions, cross-origin mutations and stale sensitive actions.
  Session rotation is retained when a reauthentication prompt is returned.
- Existing customer Google flows and the admin Google callback bridge tests
  passed. The bridge uses no customer session and carries its encrypted ticket
  in a POST body, bound to the exact admin origin and browser challenge.
- Operations/API UI unit tests: 57 passed. UI lint and operations build passed.
- Nine browser checks passed across Firefox, desktop Chromium and phone-sized
  Chromium, including accessibility, overflow, setup, recovery-code download
  and acknowledgement, failed code handling, recovery-only setup, traffic
  reports and analytics. Screenshots were reviewed.

## Deployment and owner acceptance

RC.48 is deployed. Source revision is
`0b4e098f8ade791029bb9f22883bbf37afbf3361`; the manifest and active immutable
checkout are `cfc62274ce49446a909f804349d2bd7d773d1839`.

- 51 running containers, 50 healthy checks, zero unhealthy.
- Global migration 70, cell A/B remain 86/86.
- Live traffic checks: all four sites append redacted logs, spoofed forwarding
  headers are ignored, anonymous admin reports return 401, cross-origin
  requests return 403, and the customer host does not expose admin routes.
- Live analytics certificate passed for public, private and conversion
  surfaces, consent rejection, withdrawal and synthetic-subject cleanup.
- The approved owner Google identity received the administrator role through
  the audited offline command. Live sign-in with that Google identity reaches
  the QR setup page automatically. Google verification and enrollment-start
  audit events exist; no owner admin session or confirmed authenticator exists.
- Live database privileges allow the identity lookup columns and deny
  secret_hash. Protected Stage environment values were unchanged. Temporary
  registry credentials were removed after deployment.
- The disposable local PostgreSQL container was stopped after testing.

The owner must scan the QR code in their Android authenticator and enter a
current code in the admin page. No test harness enrolled the owner's device.
Successful owner authenticator login and authenticated live dashboard
acceptance remain human steps until that setup is complete.

See [the admin guide](stage-admin-guide.md) for enrollment, everyday login,
recovery and staff role commands.


RC.47 live testing identified a native-form handoff problem: no-referrer made
the browser's POST Origin opaque, which the admin API correctly rejected.
RC.48 uses the origin-only policy for that one handoff document; callback
queries remain excluded. The exact Origin and encrypted challenge checks are
retained. The identity role is also restricted to user_id/provider/identifier
columns and cannot read customer password hashes. These fixes have focused
backend, PostgreSQL and browser regression coverage.
