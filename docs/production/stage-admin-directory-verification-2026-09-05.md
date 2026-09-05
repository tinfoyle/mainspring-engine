# Stage administrator directories — 2026-09-05

RC.50 is deployed from source `4601b5587d7df6bf3e1f38208b0181f9acd3fb3d`.
The reviewed manifest and immutable active checkout are
`a3c04bd8dd7f103567627325bdc3ef4360a9fe15`. Global migration is 71;
cell migrations remain 86/86.

## Use

Refresh the [admin console](https://ops.stage.infiniteocean.net/#directory) and
open **Users & teams**. Users load automatically; select **Teams** for accounts.
Choose 25, 50 or 100 rows per page. First, Previous and Next navigate the list.
View user or View team opens exact customer lookup with its ID and review reason.
The full instructions are in the [admin guide](stage-admin-guide.md#browse-all-users-and-teams).

All retained states are included, including users without memberships and teams
without members. Counts describe active memberships. Pages are ordered by
creation time and ID, newest first, and reflect current data rather than a
fixed snapshot of the whole browsing session.

## Security and verification

- Directory reads require the current operations_administrator role at the
  HTTP handler, service and SQL projection. Existing Google plus authenticator
  session authentication and same-origin checks apply.
- Global migration 71 installs an explicit user/team field projection. The
  runtime role receives only function EXECUTE, with PUBLIC execution revoked.
  It receives no SELECT on users, accounts or memberships.
- Every page, including an empty page, inserts an immutable directory_viewed
  audit event. An audit insertion failure prevents the page from being returned.
  Directory access does not create support grants or customer sessions.
- Full Go suite and contract generation check passed. The PostgreSQL suite
  exercised the new migration; its migration-count assertion and the API
  route-count assertion were updated. Final focused PostgreSQL and contract
  reruns passed; the other PostgreSQL tests passed in the full run.
- 62 operations/API unit tests passed. Six Firefox, desktop Chromium and phone
  browser cases passed for directory/traffic screens, accessibility, pagination,
  page-size changes and exact lookup navigation. Phone and desktop renders
  were visually reviewed.
- The live restricted-role certificate returned **two users and two teams**
  across one-row pages without duplicates, checked the field allowlist and
  confirmed each audit record. No users, teams or memberships were created.
- Anonymous directory request: 401. Cross-origin directory request: 403.
  Customer-host directory request: 404. The published admin bundle contains
  the directory module and pagination actions.
- Traffic collection and User-Agent redaction checks passed again on all four
  Stage hosts. Non-root access-log readability passed.
- 51 long-running containers, 50 healthy checks, zero unhealthy. The active
  release pointer and global migration 71 were checked. Temporary registry
  credentials on the VPS were removed.

Authenticated browser interactions used test fixtures. Live directory data and
auditing were verified through the restricted database projection; live HTTP
denials and published UI assets were checked separately.
