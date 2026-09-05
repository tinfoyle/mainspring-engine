# Recurring research reports

A user can ask a connected MCP assistant to check supplier prices every morning and email a report. The assistant uses Spyglass agent discovery and schedule tools to save the task. The application then runs it without keeping that assistant conversation open.

## Customer setup

1. Open Agents and choose a team with an active published agent.
2. Open Schedules → New schedule, enter the task, daily time and timezone, and select the team and agent.
3. Add up to six public HTTPS source links, one per line. Each occurrence retrieves these pages anew. JavaScript-only prices, location selectors, logins and retailer bot restrictions can prevent price verification; a missing price must remain unavailable.
4. Select **Email me the completed report**. This grants permission for recurring delivery to the schedule creator’s verified account email. There is no arbitrary recipient field.
5. Save, then open the schedule and choose **Run now** for a test. **Recent runs** links to the result and shows email status. Refresh the history while a run is in progress.
6. Pause to stop future occurrences and pending email deliveries. A message already being sent cannot be recalled.

Price reports should include retailer, product/type, bag size, advertised price, price per cubic foot where comparable, locality/stock confidence, source links and retrieval time. Do not treat shipping prices, third-party marketplace listings, stale snippets or missing pages as confirmed local pickup prices.

## Delivery and security

Customer report bodies remain in cell-owned agent messages. The cell report queue stores identifiers, the exact consented template, state and bounded error codes. A narrow global SQL function resolves only an active member’s verified email for the matching active account/cell and current enabled Agents entitlement. The integration connector worker uses the existing platform SMTP transport, with escaped message content and a stable Message-ID. Agents never receive SMTP credentials.

Only the creator can enable/revise an emailed report or resume/trigger it. Other authorized teammates can pause or delete the schedule. Changes to the task or its active state cancel unsent deliveries. Membership/entitlement revocation blocks sending when checked before dispatch.

The queue waits for a fully successful agent run. Failed, partially failed or canceled runs do not email a misleading completion. After 24 hours a waiting report fails visibly. Connection failures before SMTP DATA may retry at most three attempts. Ambiguous DATA outcomes or a crash after sending begins become **unknown** and are not automatically retried. **Sent** means the SMTP server accepted the message; delivery to an inbox is not guaranteed.

## Operations

Docker integration connector workers enable `SPYGLASS_REPORT_EMAIL_ENABLED=true` and receive `SPYGLASS_SMTP_ADDRESS`, `SPYGLASS_SMTP_SERVER_NAME`, optional username/password/root CA, sender address/name and `SPYGLASS_APP_ORIGIN` from the existing protected environment. For Kubernetes, provision these in the worker Secret and permit outbound SMTP to the configured endpoint. Do not log environment values or report bodies.

Global migration 72 and cell migration 87 create the recipient projection and durable delivery queue/functions. Role installation grants the connector worker only the required functions; customer access uses the existing account routing and row isolation. The report table participates in account export, erasure counting and the namespace write fence.

The MCP workflow uses team/agent discovery → schedule create → trigger → history → run/messages → pause. The internal agent tool broker does not yet create schedules from an ordinary in-app conversation; the MCP assistant and Schedules screen are the supported entry points. Configured explicit pages do not replace a search provider for open-ended web discovery.

## Plymouth, NC acceptance scenario

Use a landscaping company in Plymouth, NC, compare Lowe’s, Home Depot, Walmart and Tractor Supply. Default test time is 8:00 a.m. America/New_York. Compare common bagged wood mulch while keeping types and sizes distinct; mark unverified local prices and stock. Send only to the requesting owner’s verified address. Pause the test schedule when verification finishes unless the owner asks to keep it running.

Live evidence is recorded separately after deployment. Passing mocked provider tests alone does not establish usable prices from a real retailer.

## Local verification, 2026-09-05

- Full Go suite passed, including the new MCP typed-tool inventory and OAuth client metadata tests.
- Full PostgreSQL suite exercised all migrations. Three omissions (export column omissions, erasure coverage inventory and migration count) were corrected; the affected integration cases then passed.
- Schedule integration coverage exercises automatic enqueue, duplicate claim denial, exact lease matching, captured report content, ambiguous delivery without retry, cross-account history isolation, and pause cancellation.
- Real TLS/SMTP fixture covers accepted DATA with a subsequent connection loss and uncertain DATA acceptance, with stable Message-ID, escaped HTML and correct report links.
- Source-capture tests prove pages are read again, changed prices replace prior captures, scripts are stripped, and blocked sources remain explicitly unavailable.
- UI type checking, lint, app build and four schedule UI tests passed, including uncertain delivery guidance.
