# Testing Mainspring

Mainspring has two test loops:

- `make test-fast` runs all Go package tests and static analysis without starting infrastructure. Use it continuously while developing.
- `make test-full` builds a clean Docker Compose stack, runs the complete black-box application suite, prints service diagnostics on failure, and removes the isolated stack and volumes afterward.

The full suite defaults to Compose project `mainspring-test` and edge port `18088`, so it does not reuse or erase the normal `mainspring-dev` database. Override `MAINSPRING_TEST_PROJECT`, `MAINSPRING_TEST_EDGE_PORT`, `MAINSPRING_TEST_TEMPORAL_UI_PORT`, or `MAINSPRING_TEST_RUNNER_PORT` when running concurrent suites. Set `MAINSPRING_TEST_KEEP_STACK=true` to retain a failed stack for investigation.

The deterministic suite always uses the mock runner. Real Codex invocation remains an explicit, billable `make smoke-codex` check and is not part of `test-full`.

## Document recall contract

`make test-document-recall` runs the recall scenario against an already-started development stack. The full suite runs it automatically. It verifies that:

1. documents appear in the boardroom attachment picker;
2. only the selected document is attached to the new conversation;
3. an agent searches within that signed document scope and recalls a unique fact with a citation;
4. a decoy document's fact does not leak into the response; and
5. the attachment and recalled fact remain available on a follow-up without reattaching.

## Documented-baseline onboarding contract

`scripts/baseline-onboarding-smoke-test.sh` exercises the primary owner onboarding path. It resets and archives any current assessment, verifies that the development reset removes documents and their conversation/run attachments, completes Mia's business interview, and proceeds directly into the one-topic-at-a-time evidence interview using contextual uploads, public research, and conversational answers. It asserts that all applicable evidence topics must be answered before review, that the final answer redirects to the interview-complete anchor, and that gap review is a recap showing the owner's words and recorded outcome with corrections collapsed behind an explicit action. It then assigns gaps across agent/owner/shared/external responsibilities, rejects an unapproved plan, and verifies the approved parent ticket and child work items. A second integrated pass deliberately selects the trade demo template but describes a software company; the contract proves that business facts override the template, software/security/privacy/release topics are selected, and trade licensing and field-closeout prompts are absent. It also proves that email and Google Drive appear only as post-onboarding integrations and that mailbox replacement never pre-populates saved connection settings. The contract confirms that baseline onboarding transactionally opens the operational application.

## Unknown-answer escalation contract

`make test-unknown-answer` exercises the reusable evidence-gap path with legal compliance as the deterministic scenario. It verifies that the manager searches authorized documents before answering, plainly reports insufficient evidence, offers an approval-gated work item with a work plan and definition of done, and creates the linked Work ticket only after owner approval. The full suite runs this scenario before uploading fixtures so the missing-document branch is deterministic.

Agent-work regression coverage verifies that unavailable business-specific questions become a grouped first-class owner-input request with an attached owner subtask, completed autonomous output becomes a `work.review` approval, unknown citation IDs are rejected while authorized bindings remain server-controlled, and retrieval-budget exhaustion produces a finalizable tool result instead of failing the ticket run.

The Your Turn suite verifies that Mia renders a single conversational input surface, equivalent private questions receive stable fact keys, structured agent-supplied fact requirements remain supported, Enter sends while Shift+Enter inserts a line, the transcript remains pinned to its latest message, question-like legacy approvals can be recognized and migrated, completed work stays isolated in the review tab, and consequential actions stay isolated in approvals. Live development verification should additionally confirm that the highest-leverage question shows every affected ticket, submit one answer, observe all matching question links resolve, confirm each fully satisfied owner subtask closes, and verify the parent agents resume. The shared business-fact value and its version-history record must remain available to later ticket runs.

## Web research contract

`make test-web-research` runs the deterministic Firecrawl adapter, capability broker, SSRF guard, citation, result-bound, grant-condition, and MCP reuse tests without contacting the public web. It verifies that search requests do not scrape every result, page reads explicitly disable TLS-verification bypass, private targets and credentialed URLs are rejected, `web.search` cannot silently become `web.read`, and the configured MCP tools invoke the same signed broker path as boardroom agents.

Docker validation additionally requires checking the Compose stack on a Docker host: no research service should publish a host port, agent runners must remain only on `agent-egress`, and Firecrawl's retrieval containers must be unable to reach platform/private/metadata addresses. Those network-denial checks are deployment tests because application-level DNS validation cannot enforce redirect and DNS-rebinding behavior inside Firecrawl.

## Full-suite coverage

The black-box suite covers owner bootstrap, legacy and conversational baseline onboarding, unknown-answer escalation, boardroom runs and follow-ups, recurring schedules, document upload and recall, email, work queue, agent customization and tool execution, RAG authorization, and execution-capacity enforcement. Each focused smoke script remains runnable independently.
