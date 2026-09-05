# Stage functional audit and plain-language UI review

Status: in progress. Starting release: 0.3.0-rc.41.

The owner authorized a functional review of Stage, fixes for reproduced failures,
and replacement/removal of unhelpful customer-facing copy. Browser testing uses
the existing Google-linked account. Synthetic records are prefixed `Stage audit
2026-09-04`; existing business questions are not answered with invented facts.

## Observed results before changes

| Mechanism | Observation | Result / follow-up |
| --- | --- | --- |
| Google login and selected Account | Existing owner session opens the private application and selects Infinite Ocean. | Working. |
| Human Work | Created ticket #0004, assigned to user, started, completed; versions advanced 1 -> 2 -> 3. | Working; transition dialogs say "In progress this work?" / "Done this work?". |
| Agent Work | Ticket #0005 asked for a missing rectangle length in Your Turn. Answering 6 metres resumed the agent and completed the task with the correct 42 square metres. | End-to-end execution, question, answer and completion passed. |
| Schedules | Created a weekly schedule; Run now produced a conversation answering 2+2 correctly; paused the schedule. Opening the successful run displayed an Agent failure. | Persisted plan digest used nanoseconds while PostgreSQL stored microseconds. New runs now normalize precision; historical plans recover only when the full original hash matches. |
| Finance ledger/chart | Created AUD0904 ledger and asset/income posting accounts. | Working. |
| Finance journal draft | Unbalanced draft cannot submit. Balanced $12.34 draft saved with both lines. | Working. |
| Finance posting | Attempted to post the synthetic draft without evidence; UI returns "the Finance query is invalid". Posting dialog provides no way to supply supporting information. Other forms request raw Knowledge evidence UUIDs. | Reproduced customer workflow failure. |
| Knowledge | Accepted facts show only internal keys and metadata, with no action to read the saved value. | Reproduced missing read path; `current_claim_id` already exists in API summaries. |
| Agent configuration | Created an isolated audit agent team. Creating an agent with the default no-tools settings failed with "the Agent command is invalid". | Editor incorrectly sent nonzero tool steps with no tools. Correct the allowance when tools are selected or removed. |
| Marketing upload/release | Created an audit campaign, uploaded synthetic text, created a release and submitted it. No approval appeared in Your Turn. | Add a narrowly scoped human Marketing approval, without a fabricated model invocation; approval atomically approves the exact release and activates the campaign internally. External publication remains separate. |
| Marketing content review | Uploaded content appeared as titles and hashes, with no way to retrieve its bytes. | Add an account-authorized exact-version download with content verification and attachment-only delivery. |
| Integrations | Created a pending research connection restricted to the Stage site root. Activation requires an operator-provided provider binding. | Connection creation passed. Provider-dependent execution needs actual configured credentials; none were invented or granted during this audit. |
| Privacy | Saved the existing analytics preference; the new consent receipt appeared and the choice was preserved. | Passed. |
| Exports | Confirmed a synthetic export request; the API required recent identity confirmation and supplied the Security link. | Security gate passed. No export was created without confirmation. |
| Account, Billing, Security, closure and Affiliate | Read current membership, subscription, credit balance, security methods, closure controls and program status. | Read paths passed. Live account closure, new credentials, invitations, real charges and external publication were not performed. Affiliate enrollment/settlement is explicitly unconfigured. |
| UI language | Repeated promotional headings, "governed", "immutable", "projection", "operational reason", and internal IDs appear in ordinary workflows. | Replace with plain labels and useful instructions; retain necessary security/financial explanations. |
| Phone layout | At 360 pixels, public footer links exceeded the viewport and caused horizontal scrolling. Private page titles also consumed excessive space. | Wrap footer links, constrain the footer grid, and reduce private heading size. |

## Validation and release

Focused database tests cover human Marketing approval/rejection, repeated submission,
account isolation, stale-decision rollback, and scheduled execution with nanosecond
clock values. Content tests cover authorization before storage access, account and
campaign ownership, content-hash verification, attachment headers, and the router's
file response limit. UI regressions cover Finance supporting-note persistence and
safe retry, accepted Knowledge links, stale Marketing approvals, and no-tool agents.

The full Go suite is run with a dedicated disposable PostgreSQL instance, not
merely with the database tests skipped. UI verification includes type checking,
component/API tests, lint, production builds, public accessibility and size budgets.
The full Go suite passed with PostgreSQL enabled. All 107 private application
tests passed, alongside 51 API tests, 22 public-site tests and two operations UI
tests. Type checks, lint, production builds and the 19-route public accessibility
check passed. Desktop browser coverage passed 42 checks (two project-specific
skips); the 360-pixel phone project passed 43 checks (one project-specific skip),
including focused reruns after fixing footer overflow and the landing-page
buttons overlapping the consent prompt. All four compressed asset budgets passed.
The deployed live retest will be recorded below.

Supporting notes use the existing owner-statement evidence model. Their short
text is retained as the evidence source reference, with a hash of the statement;
the Finance record stores the evidence ID. They are not silently accepted as
general business facts. No existing business questions were answered with test data.

Human Marketing submissions can request approval again for an already-submitted,
unchanged release, repairing releases created before this fix. An existing open
request is reused. Changes invalidate the basis for approval; the owner must reject
the old request and create a fresh release. Human approvals cannot be exchanged for
runner action authorization.

Cell migration 86 permits the narrowly defined human Marketing approval origin.
After such approvals are created, a rollback must retain support for this origin;
RC.41 cannot decode approval rows without an invocation ID.
