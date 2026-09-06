# Agent tools and approved actions

Spyglass runs its own provider-neutral harness. A boardroom turn calls granted
platform tools, observes results, and continues reasoning. Every call is bound
to an immutable capability grant, account, invocation and operation ID, signed,
checked against account access, protected against replay, and audited. Agents
do not receive provider credentials or browser sessions.

## Extending the platform

`internal/application/agenttools/catalog.json` is the tool catalog used by persona
publishing and broker registration. It supplies canonical names, schemas, effects,
handlers and timeouts. The agent editor uses a generated copy. Run
`go run ./cmd/apicontract -write` after a catalog change; `-check` detects drift.

Add a catalog definition, implement its handler or explicit account-scoped router
dispatch, add tenant/permission tests and regenerate contracts. New tools must
also be included in the OpenAPI tool capability enum. Publish a new persona
version to enable the capability. Existing versions remain immutable, and new
catalog entries never automatically grant access to existing agents.

A routed tool cannot choose an arbitrary HTTP path or account. A consequential
action needs its closed payload schema, an approval-worker handler, and a
versioned executor registration in the database. Reconciliation must inspect
the exact operation instead of repeating an uncertain effect.

## Daily reports from Boardroom

In the agent editor select Read schedules and Prepare a daily report. Enable
human-approved actions and the daily report action. Five tool calls per turn is
the default for new agents. Current team, persona and run IDs are supplied by the
trusted dispatch snapshot.

Ask for a daily report in plain English. The agent can inspect existing schedules
and call `prepare_daily_schedule`. Preparation validates the settings and returns
a full `schedules.create` proposal, without creating anything or sending email.
The model's final result schema includes the closed payload for each granted
action; proposals appear in Your Turn.

Review the report instructions, source URLs, daily time and timezone, email
permission, and immediate-run option. Approval binds email delivery to the
approving user's verified account address. Arbitrary recipient addresses and
creator IDs are not tool parameters.

The approval worker atomically creates one schedule and its optional immediate
trigger, keyed by the approved operation ID. Retries reconcile the same operation.
The existing schedule worker fetches fresh public HTTPS source captures, runs
the report agent, and queues its email. The email worker checks recipient
eligibility again. Blocked sources must be described as unavailable.

A recurring schedule remains active after the test report until paused in
Schedules. Test operators should pause it after checking the email. Preparing
a proposal is not evidence that a schedule exists or an email was delivered.

## Errors and permissions

Safe, classified tool failures return to the model within its existing budget.
The agent can explain a limitation or continue other useful work. Identity,
audit, cancellation and unclassified transport failures still stop the run.
An uncertain mutation must not be retried with a new operation ID.

The router role can insert replay receipts and read only their request IDs for
conflict detection. It cannot read receipt contents or delete replay protection.
Database tests exercise the actual deployment grants and prove receipt insertion,
replay rejection, account isolation and duplicate-free schedule creation.

## Scope

Available choices include Work summary; Finance and Marketing reads/drafts;
team and agent discovery; schedule discovery and preparation; configured web
research search/read; and approved Stripe, Finance, Marketing and schedule
actions. Public MCP exposes additional operations; those are not automatically
granted to agents.
