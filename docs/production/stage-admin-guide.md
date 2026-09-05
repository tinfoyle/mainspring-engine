# Stage admin console

The staff console is at **https://ops.stage.infiniteocean.net**. RC.48 replaces
the Stage passkey requirement with **Google sign-in plus an independent
authenticator code**. Staff sessions remain separate from customer sessions.
See the [release handoff](stage-release-operator-handoff.md) for deployment status.

## First sign-in

1. Use your existing Google-connected Spyglass account. A platform owner assigns
   its staff role using the offline command below. An ordinary account is not
   automatically an administrator.
2. Open https://ops.stage.infiniteocean.net in Firefox and select
   **Continue with Google**. Choose your approved Google account.
3. Install/open Google Authenticator on your Android phone. Select **+**, then
   **Scan a QR code**, and scan the admin setup screen. No Bluetooth or USB key
   is needed. If scanning is unavailable, expand **Enter a setup key instead**
   and choose a time-based entry in the authenticator app.
4. Enter the six-digit code labeled **Spyglass Admin** and select
   **Confirm authenticator**.
5. Download or write down the eight recovery codes and store them safely
   offline. Select **I saved my recovery codes**, then **Open admin**.
   The codes appear only once. Do not send the QR image, setup key, authenticator
   codes or recovery codes to anyone, including in chat or support tickets.

For subsequent sign-ins, select **Continue with Google**, then enter the current
Spyglass Admin authenticator code. These codes work offline on the phone.
A code already used successfully cannot be used again: wait for the next code.
Five incorrect attempts within fifteen minutes pause verification for that
staff account, even if you start another login.

Google Authenticator can operate without syncing to a Google account. Keeping
this admin entry independent of the account used for sign-in gives better
separation; preserve the offline recovery codes if choosing that option.
Authenticator codes are susceptible to phishing. Bookmark the exact admin URL.

## Lost phone or authenticator

Sign in with the approved Google account, select **Lost your authenticator?**,
and enter an unused recovery code. This does not open admin. It revokes existing
admin sessions, disables the old authenticator for login, and opens replacement
setup. Scan the new QR code, confirm a new authenticator code, and save the new
recovery codes. Replacement invalidates all previous recovery codes.

If replacement is interrupted, sign in again with Google and use another unused
recovery code. There is no email/SMS fallback. If both the authenticator and all
recovery codes are lost, the operator must revoke staff access and follow a
separately reviewed identity-recovery procedure; do not remove MFA requirements
or edit authentication evidence to regain access.

## Session security

Use **Sign out** when finished. Sessions expire after eight hours or thirty
minutes of inactivity. Support grants and sensitive billing, privacy or
affiliate changes require a code verified in the last fifteen minutes. If
prompted, enter a fresh code, select **Confirm**, then repeat the original
action. Staff revocation invalidates access. Customer sessions and legacy
passkey login endpoints cannot open the Stage admin console.

## Read traffic and IP logs

Open **Traffic & logs**. Select Last hour, Last 24 hours or Last 7 days. Supply
a review reference and a useful reason, then choose **Load traffic**. The read
request is recorded in the immutable staff audit trail before files are read.

- **Summary** shows requests, unique IPs, server errors, files read, daily totals,
  sites and response codes. Requests include bots, page assets and health checks.
- **Unique IPs** lists addresses with request counts and first/last seen times.
  Filter by part of an IP. **Download displayed IPs** exports only the displayed
  rows; store the CSV somewhere access-controlled and delete it when no longer
  needed. The list is capped at the busiest 1,000 addresses, with an explicit
  warning; the summary counts all addresses processed by the scan.
- **Request logs** shows the latest 200 requests in the selected period. An IP
  filter also applies here. Columns show time, IP, site, method, response code
  and duration. These are access logs, not raw application debug output.

An IP address is not a person. Shared networks combine people; changing networks
can give one person multiple IPs. The source is the connecting address at the
public TLS edge (`request.remote_ip`), not an untrusted forwarding header. If a
CDN or proxy is added in front, revisit the trusted-source design before using
these counts as client traffic.

Collection began **2026-09-05 02:18:57 UTC**. Earlier visitor IPs were not recorded
and cannot be recovered from the old service logs. The report shows the oldest
and newest available records. An unavailable collection returns an error;
malformed records or scan limits display an incomplete-report warning. An empty
period with readable logs is different from unavailable logs. There is no
background refresh: use Load traffic for a fresh report.

## Read product analytics

Open **Analytics**, start with **Event only**, a seven-day period and a minimum
of five browsers, then choose **Run report**. Group by page, button, offer or
another available dimension when you need more detail. Reports show only events
from browsers that allowed analytics. Each daily event/surface/dimension group
must meet the minimum cohort; smaller groups are omitted.

An empty Stage report can be expected while the traffic report still shows
requests. It does not mean zero visitors or failed collection. Browser counts
are distinct within a row; adding rows does not produce a count of people or a
conversion rate. Requests and product events have different definitions and
should not be compared as if they were the same metric. See
[analytics reporting](analytics-reporting-operations.md) for consent, withdrawal,
erasure and report limits.

## Staff roles and offline commands

Only `operations_administrator` can see traffic IPs and access logs. `analytics`
can see cohort-protected product reports. Support, billing, privacy and affiliate
roles open their respective modules. An administrator has every module but no
console ability to grant staff roles, become a customer or view secrets.

Run on the Stage VPS from `/opt/spyglass-stage/current`, substituting the active
manifest from the release handoff. The wrapper passes the protected migration
credential only to an ephemeral operator container; it never prints it.

```bash
release_file="$PWD/deploy/releases/0.3.0-rc.48.env"
stage_env=/opt/spyglass-stage/secrets/2026-08-31-01/stage.env
sudo bash deploy/docker/spyglass/stage-operations-staff.sh "$release_file" "$stage_env" \
  show --email=staff@example.com
sudo bash deploy/docker/spyglass/stage-operations-staff.sh "$release_file" "$stage_env" \
  assign --email=staff@example.com --role=operations_administrator \
  --actor=authorizing-owner --reason="Approve administration of Stage traffic and analytics."
sudo bash deploy/docker/spyglass/stage-operations-staff.sh "$release_file" "$stage_env" \
  revoke --email=staff@example.com --role=operations_administrator \
  --actor=authorizing-owner --reason="Administrator duty ended; revoke staff access."
```

Use the smallest role appropriate to the person's task. Assignment requires an
active ordinary account with a connected Google identity. Enrollment is enforced\nat first admin sign-in before a staff session can be created. Revoking the final role
suspends staff access and revokes its sessions. Role changes are audited. See
[Operations Console operations](operations-console-operations.md) for other
module boundaries and incident response.

For a first-time staff member, `show` reports “the User is not operations staff.”
That is expected until an owner assigns the first role; it is not a service fault.

## Collection, deployment and maintenance

The existing Hostinger Caddy owns public TLS. The reviewed
`enable-stage-access-logs.py` installer adds a filtered JSON writer per Stage
host; `enable-stage-admin-host.py` adds the admin host after its services are
deployed. Both validate the exact installed Caddy binary, preserve a timestamped
backup, preserve the bind-mounted configuration inode, and reload the edge.
Neither restarts the shared edge or modifies unrelated site blocks.

Logs live at `/opt/infiniteocean/caddy/data/spyglass-access`. The directory is
root-owned, group 65532, mode 2750; files are mode 0640. It is mounted read-only
at `/var/log/spyglass/access` in the non-root operations API. No other Caddy data,
Docker socket, application secret directory or arbitrary file browser is exposed.
URLs, query strings, all request/response headers, TLS details and request bodies
are excluded. The API returns a fixed allowlist of fields and Stage host names.

Files roll at 10 MiB, keeping at most seven rotated files per host with a
168-hour rotated-file age setting. Age cleanup occurs on rotation; an active
file on a quiet site can contain older records. This is size-bounded rotation,
not a promise that every byte is physically deleted after seven days. The UI
queries at most seven days. Reports have a ten-second deadline, 128 MiB
decompressed scan budget, 40-file cap and 100,000 distinct-IP cap. Scan limits
produce a warning. Retained logs can be shorter than the selected period under
heavy traffic. A concurrent rotation may also produce a partial report; retry.

After every release, verify all four host requests append a record, the IP is
the connecting address, and a synthetic query/header marker is absent from the
log. Check that the API can read the mount as UID/GID 65532, that missing logs
show an error, and that an unauthenticated report request is denied. Check the
latest available timestamp when investigating a quiet report. Service readiness
alone does not prove that the external edge is writing new records.

From the current repository in UbuntuRojo, run the self-contained verification
helpers over the existing SSH connection. These commands do not copy secrets.
The analytics certificate creates five anonymous test browsers, checks consent,
public/private/conversion reports and withdrawal, then erases only those test
subjects and confirms zero remaining test events. The traffic check sends
synthetic probes and checks all four files, redaction, forwarding-header spoof
rejection, API denials and readability as the API's non-root user.

```bash
ssh -F /mnt/c/Users/Tinfo/.ssh/config -o BatchMode=yes infiniteocean \
  sudo python3 - --stage < deploy/docker/spyglass/verify-stage-traffic.py
ssh -F /mnt/c/Users/Tinfo/.ssh/config -o BatchMode=yes infiniteocean \
  sudo python3 - --stage < deploy/docker/spyglass/certify-stage-analytics.py
```

There is no remote log archive or alerting service added by this release. Logs
survive application redeployment in the host Caddy data directory; include that
directory only in protected host backups with a documented expiry. Database
backups preserve staff governance and report-access audits. Rotate the five
operations database credentials through a new protected Stage secret set, rerun
the role installer and recreate the API; never edit an active secret set in
place. CSV exports and backups need their own access and expiry controls.

## Add a back-office module

1. Add its Vue component under `ui/apps/operations/src/modules/` and register its
   ID, label, description, roles and lazy component in `modules/registry.ts`.
   Navigation and the overview derive from this registry.
2. Define its exact request/response in `api/operations.openapi.json`, regenerate
   using `go run ./cmd/apicontract -write`, and add the typed API helper.
3. Add an operations API handler with same-origin, passkey-session and current
   staff-role checks. Menu visibility is not authorization.
4. Give the service only the database functions or read-only resources it needs.
   Recheck current authority at the data boundary, and record an immutable audit
   event before privileged access. Do not add broad table or filesystem access.
5. Test authorized and denied requests, role revocation, audit creation, empty
   and error states, desktop/phone layout and accessibility. Update this guide.

The traffic, analytics and help modules demonstrate the component path. Existing
support, billing, privacy and affiliate screens remain registered modules while
their legacy screen bodies are in `App.vue`.
