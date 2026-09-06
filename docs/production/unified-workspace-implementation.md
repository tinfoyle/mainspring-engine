# Unified workspace implementation

User-authorized objective: implement the proposed unified workspace, verify it, and deploy to Stage. The prototype is a reference, not completion evidence.

## Required outcome

- Persistent chat with named conversations, saved drafts, run recovery/polling across views and refresh, and account isolation.
- One optional dynamic working view. Work, Knowledge and Documents are primary; Your Turn remains discoverable as Needs you. Operational Finance, Marketing and Schedules remain accessible.
- Group administrative destinations in Settings; preserve required setup, restricted-account access, security and separate staff console.
- Keep existing deep links, report/approval URLs, OAuth callbacks and return paths working. No orphaned capabilities.
- Integrated document library using existing server admission/version/retrieval protections; explicit context selection for documents, work and saved knowledge with visible source/version boundaries.
- Quiet responsive layout, accessible keyboard/focus handling, meaningful loading/errors, no loss of unsaved edits, no duplicate sends or approvals.
- Automated regression checks, rendered desktop/mobile checks, live Stage workflow verification, and updated operator/user documentation.

## Work log

2026-09-06: Inspected current router (34 destinations plus redirects), 16 navigation entries, route-owned Agents state, Knowledge document backend and safe-navigation guards. Implementation started from clean main 2fff3eb. No deployment yet.

2026-09-06 implementation: added a persistent account-scoped conversation store and chat surface, unified view navigation, Settings and optional toolbar pins, a document library with upload/publication/search/pagination, and explicit versioned context from Work, Knowledge and Documents. Existing routes and server approval controls remain in place. The business interview persists in the same chat area; its notebook appears in the working view. No database migration is needed.

Verification in progress: full Go suite, generated API contract check and a real PostgreSQL document publication/account-boundary test passed. All UI unit suites passed (116 customer, 11 operations, 22 public, 55 API tests); an added conversation configuration-refresh regression subsequently passed with the 21-test focused shell/agent/store suite. Lint passed. Customer production build passed after fixing a test-only type error. Legacy browser navigation expectations were updated to the new toolbar while retaining pending-command, unsaved-edit, read-only and recovery assertions. Stage remains RC59 until the remaining browser checks and release publication complete.

Release checks: all 38 applicable legacy desktop browser cases passed; the phone keyboard/focus switch passed. The workspace suite passed 32 cases across Chromium, Firefox, 360-pixel phone and 320-pixel reflow, then was rerun after chat scroll-following polish. Production asset budgets pass (private JavaScript approximately 135 KiB gzip against 140 KiB, CSS approximately 15 KiB against 20 KiB). A route comparison found no removed paths or names. Desktop and phone screenshots were visually reviewed. See [the workspace user guide](workspace-user-guide.md) for navigation and persistence boundaries.

RC60 live check found a completion-path defect: the document arrived as user-supplied text but was absent from the frozen server context, so citation policy rejected its result and the queue dead-lettered it while the run stayed planned. RC61 connects whole-document attachment to the existing `context.knowledge_document_ids` admission and immutable snapshot. UI wording now explicitly says whole published revision, and prepared files stop claiming to be checking. Cell migration 90 narrowly permits authenticated completed results rejected by output/policy validation to close as agent failures, using the same lease/account/digest checks and without publishing rejected content or actions. The owner test run is the only live failed projection targeted for requeue after deployment. Tests cover document registration, terminal rejection, replay and incorrect digests/codes.
