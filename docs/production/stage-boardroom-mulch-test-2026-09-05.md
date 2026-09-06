# Stage boardroom request test — 2026-09-05

Follow-up: the harness is now repaired and the [Boardroom end-to-end verification](stage-agent-harness-verification-2026-09-05.md) passed. The original failure evidence below is retained.

Original result: failed twice before the agent could produce a reply. This was a plain-English
request entered through the Weekly Operations boardroom, not an MCP-created
schedule or a manually assembled workflow.

## Request and live result

The owner asked for another test using the bots inside the application. A fresh
conversation asked Shop Coordinator to check Lowe's, Home Depot, Walmart and
Tractor Supply for bagged mulch in Plymouth, NC; email the verified account
address daily at 08:00 Eastern; send one test now; and leave recurrence paused.

The boardroom accepted the message and started a model invocation. The model
selected work.summary.read. Its authorization succeeded, but the tool service
could not record the anti-replay receipt:

    permission denied for table tool_context_receipts

Broker audit recorded work.summary.read / failed / tool_boundary_unavailable.
The run ended with tool_step_failed. One retry using the normal “Retry failed
turns” control repeated the same model-success/tool-failure sequence.

- First run: e8d0b77a-f744-4aaa-ab99-b3e6e3892dc9
- Retry: 1c9a019c-405a-8194-ae68-2a9fad3e51b0
- Conversation: 42fc9d9e-e185-8854-bd1e-80e2e1668ef4
- Both pre-existing schedules remained paused; no schedule was created.
- No report delivery was queued by this test.

[Open the boardroom conversation](https://app.stage.infiniteocean.net/app/agents/boardrooms/eee01c43-a20a-49c5-b040-211fde575e3c/conversations/42fc9d9e-e185-8854-bd1e-80e2e1668ef4).

## Findings to address

The deployed tool router uses the app-router database role. Its role grants
omit access needed by ToolContextReceiptRepository.Consume to insert a receipt.
This failure occurs before work-summary dispatch or retailer access.

The executor treats a tool error as terminal, so the UI shows “Failed” without
an explanatory agent reply. Graceful reporting of a recoverable tool failure is
also missing.

Separately, the internal boardroom tools do not expose the schedule operations
added to the public MCP endpoint. Fixing receipt permissions alone will not
enable autonomous creation, test execution and pausing of a self-email schedule.
Those actions need a boardroom tool contract bound to the requesting user's
authority and email consent.

The [earlier MCP test](stage-mcp-mulch-test-2026-09-05.md) proved the underlying
schedule/agent/email path when driven explicitly by an external assistant.
This test did not prove that a boardroom agent can assemble that workflow.
