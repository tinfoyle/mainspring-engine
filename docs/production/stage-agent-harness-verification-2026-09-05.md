# Stage agent harness verification — 2026-09-05

Result: **passed through Boardroom, product approval, scheduled execution, and inbox delivery**.

The end-to-end check ran on Stage RC58, source
`f7e5ed98ce6ef0e20d0032cc74d5642da0c350d7`, immutable checkout
`4925d39bdedc54bd299ab388467e7d750d030220`. Global migration 72 and both cell
migration 89 were applied. All 53 containers were running, with 52 healthy
(the edge container has no health check) and none unhealthy.

## What was repaired

The custom Go model/tool loop already existed. Tool execution failed because
the deployed router role could not insert replay-prevention receipts. Tool
definitions had drifted between publishing, the agent editor and broker registration,
and several useful capabilities were not available to configure.

The final response schema allowed only empty action payloads, preventing useful
approved actions. The repair supplies closed payload schemas for granted actions,
a shared platform tool catalog, validated team/schedule discovery, and daily report
preparation and approved creation. Classified tool errors can return safe feedback
to the model without losing the whole run. Permission, tenant, replay and audit
checks still run on the server.

The first successful Boardroom test also exposed that report email included only
the contribution, omitting findings. Migration 89 includes findings,
recommendations, questions and source labels, without raw action payloads.

See [agent tool harness and extension guide](agent-tool-harness.md).

## Live verification

The owner-account Shop Coordinator was published as version 2 using the normal UI.
Its system instructions were preserved. Granted tools: Work summary, team
discovery, team-agent discovery, schedule discovery, and daily report preparation.
The only newly allowed consequential action was daily schedule creation, requiring
human approval. Maximum tool calls: five.

A new Boardroom conversation asked in ordinary English for daily bagged-mulch
prices in Plymouth, NC from Lowe's, Home Depot, Walmart and Tractor Supply,
at 08:00 America/New_York, to the verified account email, with an immediate
test after approval. The test explicitly left prior paused schedules alone and
said the new one would be paused after inbox verification.

The model selected and successfully used:

1. `work.summary.read`
2. `schedules.read`
3. `schedules.prepare`

All three have authorized and succeeded audit events. Four successful model
steps connected the calls and final answer. The agent detected the existing
paused tests and generated the report instructions, six source URLs, time zone,
time, recipient permission and immediate-run setting itself. It correctly said
that preparation had not yet created a schedule or sent email.

| Evidence | ID |
| --- | --- |
| Final Boardroom conversation | `56b5908a-d5d6-8405-a854-4bd456c28fae` |
| Proposal run | `133e92bc-dacf-4c78-851c-34d2c901d226` |
| Proposal invocation | `60a87339-fe59-8a54-b655-5f7cf0b9ceee` |
| Approval | `c2a6a6e9-5539-8bfd-86c7-73404161c454` |
| Created schedule / approved operation | `cf92630a-9a03-8efd-a50f-95b0ba47b8b3` |
| Report conversation | `1896d6ce-4a8d-8f9c-937a-72122c07b72e` |
| Report run | `fd423d7f-57a3-887b-94bd-4a9ff8a9e2ad` |
| Report invocation | `dde85b1b-45ca-8d67-aee8-d1f181cf59e3` |
| Report delivery | `6a6ba2ad-c66e-8e21-8640-aa95af86a24f` |

[Open the request and agent reply](https://app.stage.infiniteocean.net/app/agents/boardrooms/eee01c43-a20a-49c5-b040-211fde575e3c/conversations/56b5908a-d5d6-8405-a854-4bd456c28fae).

[Open the completed report](https://app.stage.infiniteocean.net/app/agents/boardrooms/eee01c43-a20a-49c5-b040-211fde575e3c/conversations/1896d6ce-4a8d-8f9c-937a-72122c07b72e).

The concrete proposal was reviewed and approved in Your Turn under the owner's
authorization for the self-email test. The approved action succeeded in one
attempt. The application created and triggered the schedule; the external
assistant did not create it through MCP, SQL, or the Schedules form, and did not
press Run now.

The report run succeeded. Delivery succeeded in one attempt at
**2026-09-06 01:06:10 UTC** (September 5, 9:06 PM Eastern). Gmail independently
confirmed the message in INBOX, ID `1a0744062c69f924`. Both MIME alternatives
contained the complete four-retailer report, source URLs, findings and
recommendations. Its Message-ID was
`spyglass-report-6a6ba2ad-c66e-8e21-8640-aa95af86a24f@infiniteocean.net`.

## Cleanup and limits

The new schedule was paused through its normal UI after verification. It is
version 2 with no next-run time. The first successful Boardroom test schedule
`71ccb9c7-2511-86c8-b329-1937df19f7bd` is also paused. Both older test schedules
remain paused; the account had no active schedules at final cleanup.

No current retailer price was verified. The sites refused retrieval or presented
human verification. The agent reported unavailable values and did not invent
prices. This was a functionality test, not a price-data validation. The live
test used the immediate occurrence; daily recurrence and timezone behavior have
automated coverage. Emails preserve the agent's text, including Markdown syntax
when the model uses it; this change does not add rich Markdown email rendering.

## Automated checks

- Full Go suite passed.
- Full PostgreSQL suite found only the migration-count expectation, corrected and
  rechecked; the remaining suite passed. Final migration, schedule, receipt-role
  and approval-role tests passed with PostgreSQL enabled.
- Actual deployed role grants prove receipt insertion, replay rejection, denial
  of receipt-content reads/deletes, and account-isolated schedule creation.
- Approved schedule creation and immediate trigger are atomic and replay-safe.
- Schema checks cover all catalog tools and action payloads, including strict
  provider constraints. API generation check passed.
- Seventeen relevant agent-editor, Boardroom and approval UI tests passed,
  along with type checks, lint and the UI build.
- Matched release images passed the publication vulnerability scans with SBOM
  and provenance. Temporary registry credentials were removed from Stage.
