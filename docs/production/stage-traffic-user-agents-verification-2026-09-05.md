# Stage traffic user agents — 2026-09-05

RC.49 is deployed from source `3ea72ffdf5eb6d10994ca8ac9120769ef524ed64`.
Its manifest and immutable Stage checkout are
`8225c3e526d28fbc9af34ffb4034116114a2dc2d`.

## Use

Refresh the admin console, open **Traffic & logs**, select **Load traffic**,
then **Request logs**. The User agent column is at the right of the table;
scroll horizontally on narrower screens. Long values have a short preview and
**Show full user agent** to expand. Old entries and requests without this field
show **Not recorded**. User-agent collection began at **19:53:01 UTC**.

The edge copies only the User-Agent header into a separate JSON field using
[Caddy log_append](https://caddyserver.com/docs/caddyfile/directives/log_append).
It still removes the request-header object, response headers, URI/query and TLS
details. The report accepts only its fixed field allowlist, strips control and
format characters from user agents and caps them at 1,024 characters. Vue renders
them as text. They describe what the client claims to be, not verified identity.

## Evidence

- Full `go test ./...`, focused log reader/service/API tests, contract generation
  check and UI lint passed. No database migration was needed.
- 57 operations/API unit tests and three installer upgrade tests passed.
- Firefox, desktop Chromium and phone-layout browser tests passed, including
  accessibility, long-value expansion, HTML-as-text and missing-value display.
- Live probes on public, app, MCP and ops hosts recorded the exact synthetic
  user agent. Synthetic query, cookie, authorization and custom private-header
  markers were absent; spoofed forwarding IPs were ignored.
- Unauthenticated operations report: 401. Cross-origin report: 403.
  Customer-host operations route: 404. Non-root log mount readability passed.
- 51 long-running containers, 50 healthy checks, zero unhealthy. The live
  operations UI bundle contains the new field, expansion and missing-value text.
- Host Caddy configuration was validated and reloaded with a timestamped backup.
  Re-running the installer is idempotent; unrelated host blocks are preserved.
- The temporary VPS registry login was removed after deployment.

The owner confirmed actual authenticator enrollment and admin traffic access
before this change. Authenticated report UI behavior for RC.49 was exercised
with browser fixtures; live collection, access boundaries and deployed assets
were checked separately.

A shell quoting error exposed the publishing token in private task output.
The token must be retired and replaced before another publication. No Stage
application or admin-authentication secret was included in that command.
