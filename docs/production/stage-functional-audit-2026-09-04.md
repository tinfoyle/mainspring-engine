# Stage functional audit and plain-language UI review

Status: completed on 2026-09-04. Starting release: 0.3.0-rc.41; final deployed release: 0.3.0-rc.44.

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
The deployed live retests and final validation are recorded below.

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

## Live RC.42 retest and follow-up

- The original failed schedule conversation opens and shows the correct result.
- Two agents with no tools were created successfully. Direct arithmetic and a
  separate summary-agent run both completed with correct answers.
- Saving a summary agent exposed a further UI defect: refreshing the roster
  selected that agent as a regular participant even though it was hidden from
  the checklist. The follow-up preserves the allowed selection, includes the
  summary in the token estimate, and explains the two-agent prerequisite.
- The original Finance draft posted with a supporting note, then reversed to
  zero. A zero-balance statement comparison was confirmed and the period closed.
  Both posting accounts and then the ledger were archived. The archive
  prerequisite previously produced only "invalid query"; the follow-up explains
  the required order. Default Finance dates now use the browser's local date.
- The exact Marketing file downloaded successfully, and resubmitting the old
  release created its missing Your Turn approval. The final decision exposed a
  router omission: Marketing authority was not carried to the cell. The follow-up
  carries only currently granted Marketing authority on approval-decision
  requests; missing/read-only Marketing cannot activate a campaign, while other
  approvals remain usable. Signed-router tests verify both granted and denied
  package states using the real cell authorizer.
- Saved Knowledge opens to its actual value and citations. Internal key prefixes
  and generated question hashes are replaced with readable titles.
- The synthetic integration was revoked successfully. No provider credentials
  were bound. The existing business setup and its approved Work remain intact.

## Final RC.44 live acceptance

RC.44 is deployed from source `171992fe96389367e0bd07f0f8edbc2593a7f9e0`.
The tracked manifest and active VPS checkout are
`ab964530a32240239e2b773d30617ccf00294d3d`; exact image identities are in
[`0.3.0-rc.44.env`](../../deploy/releases/0.3.0-rc.44.env). RC.43 was published
but not activated; the final candidate also removes the last public footer slogan.

| Retest | Final observation |
| --- | --- |
| Marketing approval | Approved the original repaired Your Turn request. The request cleared, release advanced to Approved v3, and campaign became Active v2. Pause, complete and archive then succeeded, ending at Archived v5. No external publication occurred. |
| Agent summary selection | Changed the summary agent in both directions; the checklist retained only eligible participants and included the summary in the 2,000-token estimate. A fresh two-agent run completed successfully; both calculation and summary returned 8 × 7 = 56. |
| Schedule | The old failed history opens correctly; a fresh resumed Run now succeeded with 2+2=4. The schedule is paused at v4, preventing future scheduled execution. |
| Finance | Posted the original $12.34 draft with a note, reversed it to zero, confirmed a zero statement comparison, and closed the period. Archived both posting accounts and then the ledger, retaining its history. |
| Marketing file review | The exact-version file action produced a browser download. Server tests verify ownership, stored byte length and full content hash before delivery. |
| Knowledge | Opened an accepted fact and read its saved value and citations. Readable titles replace internal prefixes and generated question hashes. |
| Test integration | Revocation succeeded. The connection remains revoked and unbound. |
| Public phone layout | Rechecked the deployed page at 360 pixels: document width and content width both measured 345 pixels (the remaining space is the scrollbar), with no horizontal overflow. The footer slogan is absent. |
| Deployment | Live image identities match all three RC.44 manifest digests. There are 49 running containers, 48 healthy workloads and zero unhealthy workloads. Public and login origins return HTTP 200. Database ledgers are global 68 and cell 86/86. |

The final private application suite passed all 109 tests. The Go packages and
database migration suite passed after the follow-up fixes. A local WSL/Docker
interruption stopped the disposable PostgreSQL container during one full-suite
attempt; after restoring that test container, the database suite passed. This
was a local test infrastructure failure, not a Stage database failure. All ten
follow-up desktop/phone browser checks passed across focused reruns after their
old-copy assertions were updated. Final builds and compressed asset budgets
passed. Each of the three RC.44 images passed high/critical vulnerability and
secret scans with zero findings and carries SBOM/provenance attestations.

The copy review removed motivational slogans and redundant headings, replaced
terms such as Baseline, Persona and synthesis manager with Business setup,
agent and summary agent, and made errors and prerequisites actionable. Necessary
supporting information remains available; stored business content was not rewritten.

## Retained test records and limits

- Human test ticket #0004 and agent test ticket #0005 are completed.
- Audit ledger AUD0904 and campaign `0249f827-441d-4611-8abf-9743cdb7aee9`
  are archived; financial test entries net to zero.
- Schedule `760cffc1-c7c5-4d21-986d-613d6f45e847` is paused at v4.
- Audit agent team `3fa61870-1b3d-4db5-8cd5-81cf7047a9b6` retains its
  successful synthetic conversations. It has no external tools.
- The test integration is revoked. Existing business questions, facts and the
  prior business setup remain intact apart from the explicitly synthetic answer.

This audit does not claim every provider-dependent or destructive mechanism was
executed. Actual payment/checkout, external-provider binding and delivery,
email/SMS-code receipt and redemption, passkey changes, invitations, account
closure/erasure and a full external MCP-client authorization/tool/refresh/revoke
certificate remain untested here. The export request correctly required recent
identity confirmation; artifact creation/download was not exercised. Affiliate
enrollment/settlement remains unconfigured. These limits are distinct from the
reproduced workflow defects fixed above. No production or LKE deployment occurred.
