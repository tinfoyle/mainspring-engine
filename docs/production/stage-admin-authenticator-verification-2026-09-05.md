# Stage admin authenticator verification — 2026-09-05

Candidate: 0.3.0-rc.47. The owner explicitly authorized Google plus an
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

Deployment results are recorded after the immutable candidate is installed.
The owner must scan the setup QR code in their own Android authenticator and
enter a current code in the admin page. No owner credential is generated or
confirmed by a test harness. Successful owner authenticator login remains a
human acceptance step until that setup is complete.

See [the admin guide](stage-admin-guide.md) for enrollment, everyday login,
recovery and staff role commands.
