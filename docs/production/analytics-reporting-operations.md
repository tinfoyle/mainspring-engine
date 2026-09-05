# Analytics reporting operations

- Status: local implementation complete; release-environment provisioning remains open
- Data boundary: consented, first-party, content-free events only
- Companion architecture: [Privacy, analytics and Affiliate architecture](privacy-analytics-affiliates.md)

## Report contract

`spyglass analytics-report` emits JSON aggregate rows containing only a UTC bucket, reviewed event name, public/private/conversion surface, one reviewed dimension value, event count and distinct anonymous source-subject count. It never returns a consent-subject, User, Account, payment, subscription, Affiliate or customer-content identifier.

The database enforces the boundary in `spyglass_analytics_funnel_report(...)`:

- daily windows may cover at most 395 days;
- hourly windows may cover at most 31 days;
- the minimum cohort is always between 5 and 100 distinct consent subjects;
- only `none`, `device_class`, `locale`, `route_name`, `cta_code`, `feature_code`, `package_code`, `offer_code`, `campaign_code`, `method`, `referral_present`, `entry_method`, `result`, `entry_point`, `queue_state`, `task_category` and `duration_bucket` can be selected as dimensions; and
- groups below the selected cohort are omitted rather than returned with a small count.

The dedicated `spyglass_analytics_reporter` role has no table or sequence privileges and can execute only the aggregate report function. The function is security-definer, has a fixed search path and has no PUBLIC execute grant. Production and Stage must create a separate random reporter password and provide only `SPYGLASS_ANALYTICS_REPORT_DATABASE_URL` to the one-shot reporting job.

## Local operation

The Stage staff console exposes this report at **Analytics** on
`https://ops.stage.infiniteocean.net`. Access requires a passkey and the
`analytics` or `operations_administrator` role. See the
[Stage admin guide](stage-admin-guide.md) for enrollment, roles, report controls,
and how consented product reports differ from request/IP traffic.

Run through the UbuntuRojo local Docker composition:

```sh
cd deploy/docker/spyglass
make analytics-report
make analytics-report ARGS='--from=2026-08-01T00:00:00Z --to=2026-09-01T00:00:00Z --bucket=day --dimension=offer_code --minimum-cohort=10'
```

Defaults are the previous 30 days, daily buckets, no dimension and a five-subject cohort. The command fails closed for an invalid timestamp, window, bucket, dimension or cohort. Output is written to stdout so an operator can direct it to an access-controlled analysis tool without granting that tool raw-database access.

Public and private rows remain independent directional evidence. `conversion` rows are the reviewed milestones mirrored through the short-lived handoff and can be compared with public `signup_handoff_started` cohorts without joining a private subject or identity. They remain pseudonymous aggregate product evidence, not causal attribution or billable Affiliate evidence. Commercial Affiliate attribution remains authoritative only in the checkout/subscription ledger.

## Privacy and retention behavior

The report reads the retained raw-event store at execution time. Browser-subject erasure cascades to its events, so later reports exclude them. The 395-day retention worker removes expired events, and the report cannot query a longer range. Previously exported aggregate reports require their own approved access and deletion schedule; no automated export or third-party analytics vendor is enabled by this implementation.

Adding a dimension or event requires simultaneous registry, ingestion, migration, application allowlist, tests and documentation review. Free text, arbitrary URLs/query strings, identifiers and provider payloads remain prohibited.

## Handoff boundary

A consented public `signup_handoff_started` response may set a signed, HTTP-only, Secure, SameSite=Lax cookie for the configured Infinite Ocean parent domain and `/api/v1`. It contains only the anonymous public subject, the exact handoff event and a maximum 24-hour expiry. On the application host, only the reviewed registration, verification, Account creation, security, checkout, subscription and first-entry milestones can be mirrored. The mirror table stores the public subject/consent plus bounded milestone fields, but no private subject, User, Account or private event ID. One handoff records at most one copy of each milestone. Public withdrawal clears the browser handoff and PostgreSQL refuses a new mirror unless that source subject's latest public decision still allows analytics. Public-subject erasure cascades to existing rows; private erasure also removes the public subject while the handoff is still present. Run `make verify-analytics-handoff` for the self-cleaning local cross-subdomain proof. Stage, GHCR and LKE are not contacted by the local reporting workflow.
