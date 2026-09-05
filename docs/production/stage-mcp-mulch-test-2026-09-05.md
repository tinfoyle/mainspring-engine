# Stage MCP mulch-report test — 2026-09-05

Result: the MCP → saved schedule → source capture → agent → self-email path
completed, and one report arrived in the owner's Gmail inbox. **Dependable
big-box price collection is still blocked by retailer access controls.** No
current price or local stock was verified.

## Scenario and boundaries

Pretend landscaping company in Plymouth, NC 27962; Lowe's, Home Depot,
Walmart and Tractor Supply; compare ordinary bagged wood mulch with sizes
and colors kept distinct. The saved test used 08:00 America/New_York, six
explicit retailer pages and email to its creator's verified address.
Verification used Run now through MCP rather than waiting for the next
morning. The schedule was paused afterward, version 2, with no next run.

## Live evidence

- Normal Google session, explicit browser consent, hosted native-client metadata,
  S256 PKCE and authorization-code exchange succeeded. Token refresh also passed.
  No authentication database edits or forged credentials were used.
- Authenticated MCP tools/list returned 103 available tools. Its 14 new schedule
  and agent tools were present. Five optional research tools were absent because
  their provider is not configured.
- Team/agent discovery selected the existing Weekly Operations / Shop Coordinator.
  Schedule create and manual trigger succeeded. Repeating each exact operation
  returned the original object; history contained no duplicate occurrence.
- A mismatched account argument was rejected with HTTP 400 before lookup.
- The first run failed with model_step_failed; broker audit showed its model
  request canceled at 15 seconds. After the transport correction, run
  63f7ef52-db9c-8e03-96ff-c5a46c9476a6 succeeded in conversation
  b2d39ac3-6045-88bc-87fd-e726848cf971.
- Source fetch diagnostics from the app API network returned HTTP 403 for Lowe's,
  Home Depot and Tractor Supply. Walmart returned a human-verification page.
  The agent reported unavailable prices and did not invent a cheapest store.
- The successful report was sent in one delivery attempt. Gmail placed it in
  INBOX at 2026-09-05 23:37:25 UTC. Its subject was
  “Plymouth mulch prices — Stage delivery test — 2026-09-05”.
  The body matched the agent's report; report and pause links were correct.
- The earlier failed run's delivery became failed / agent_run_failed with zero
  send attempts. It did not send a misleading completion email.
- MCP pause returned paused, version 2, next_run_at null. The test schedule is
  97aacc9b-961f-4b8e-90ed-0b95ee281353.
- The temporary OAuth grant was revoked (HTTP 200), its next MCP request was
  rejected (HTTP 401), and its native test credential file was removed.
- RC55 final deployment passed preflight and health waiting: 53 running
  containers, 52 healthy checks, global migration 72 and both cells at 87.

[View the paused Stage test](https://app.stage.infiniteocean.net/app/schedules/97aacc9b-961f-4b8e-90ed-0b95ee281353).

## Fixes discovered by the live test

RC52 added typed MCP schedule/run access, fresh bounded source captures,
self-email permission, durable delivery and recent-run UI. RC53 repaired
browser consent referrer/form-action policies while retaining CSRF, exact
redirect and PKCE checks.

RC54 extended the runner's capability-call timeout beyond the provider's
five-minute budget while retaining short request/result exchanges and caller
cancellation. It added public outbound access for the source reader and enabled
the previously omitted integration-connectors deployment profile.

RC55 allows an explicitly empty credential index so the self-email worker can
start before external account connectors exist. Empty indexes grant no
credentials. The existing source directory also required group traversal:
directory mode 750, key mode 640, both with the Stage secrets group. No key
value changed. Stage preflight now checks those mounts before rollout.

## Remaining limits

The daily scheduler, agent execution and email delivery do not provide permission
to bypass retailer blocks or prove current local pricing. This test establishes
the workflow and honest missing-data reporting, not useful daily price coverage.
Production purchasing reports need an approved supplier feed or another permitted,
reliably accessible source, with local store selection and freshness checks.

The supported creation paths are a connected MCP assistant and the Schedules UI.
An ordinary in-app agent conversation does not yet have schedule-creation tools.
Explicit source pages are not open-ended web discovery. UI “Sent” means SMTP
acceptance; this test additionally checked actual Gmail receipt.

See [Recurring research reports](recurring-reports.md) for setup, consent,
retry behavior and local test coverage.

## Historical first attempt

The original RC51 Codex login failed because the client attempted dynamic
registration, which this server does not support. Discovery and unauthenticated
denial worked, but no research/run/email occurred in that attempt. The hosted
native-client metadata endpoint and normal consent/PKCE flow resolved that
authorization blocker; the evidence above supersedes the original blocked result.
