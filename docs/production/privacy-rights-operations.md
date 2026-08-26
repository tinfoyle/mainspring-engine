# Privacy-rights fulfillment operations

- Status: locally constructed and certified; environment role grants and staff procedure approval remain release work
- Process mode: `spyglass privacy-rights-admin`
- Database scope: global PostgreSQL only
- Related architecture: [Privacy, analytics and Affiliate architecture](privacy-analytics-affiliates.md)

## Boundary

Customers submit strongly authenticated access, correction, erasure, restriction, objection and portability requests from the Vue Privacy route. The operator command records the staff-side lifecycle after the underlying work has actually been performed:

1. `list-open` returns at most 100 content-minimized open requests due on or before an explicitly authorized timestamp and appends one immutable queue-access event;
2. `inspect` reads one exact request and appends an immutable access event;
3. `start-review` moves only `submitted` to `in_review` against the exact current version; and
4. `resolve` moves only `in_review` to `completed`, `partially_completed` or `declined` against the exact current version.

The queue result contains only request ID, version, right, scope, state and request/due/update timestamps. It does not return the requesting User ID, email, Account, Affiliate identity or customer content. `inspect` is the separate audited step that resolves the opaque request to the authenticated User when fulfillment work actually begins. Queue listings are ordered by response deadline and request ID. A request submitted on a month-end date is due on the last valid day of the following month—not a normalized date in the month after that.

`resolve` requires an opaque evidence UUID and the SHA-256 digest of the reviewed fulfillment artifact. The command never accepts the artifact, customer content, an email address, a case narrative or a free-form response. Actual export, correction, restriction, objection or erasure work remains in its governed application workflow; this command cannot claim that work happened merely by invoking it. Affiliate access and portability can now be fulfilled immediately through the strong-authenticated self-service JSON export; a tracked request is still available when the customer needs reviewed assistance or another right.

Every action requires the standard short-lived phishing-resistant operator authorization envelope, an exact environment confirmation and a content-minimized operational reason. Authorization is bound to action, environment, request ID, expected version, outcome and evidence binding. A stale version or invalid state fails without writing a transition.

## Execute-only database role

Provision the SQL role `spyglass_privacy_rights_operator` as the environment credential mapped to the inventory role `global.privacy-rights-operator`. It receives no direct table privileges:

```sql
GRANT USAGE ON SCHEMA public TO spyglass_privacy_rights_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_list_open_privacy_rights_requests(uuid,timestamptz,integer,text,text,text)
  TO spyglass_privacy_rights_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_inspect_privacy_rights_request(uuid,uuid,text,text,text)
  TO spyglass_privacy_rights_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_transition_privacy_rights_request(uuid,uuid,bigint,text,text,uuid,bytea,text,text,text)
  TO spyglass_privacy_rights_operator;
```

Migrations 43 and 54 revoke all three functions from `PUBLIC`; migration 55 repairs and constrains the stored response deadline without widening operator authority. Fresh-PostgreSQL integration coverage proves the role has function execution but no direct `privacy_rights_requests` or queue-audit read authority.

## Required configuration

All actions require:

- `SPYGLASS_DATABASE_URL`
- `SPYGLASS_OPERATOR_ID`, `SPYGLASS_OPERATOR_REASON`
- `SPYGLASS_ENVIRONMENT`, `SPYGLASS_CONFIRM_ENVIRONMENT`
- the standard `operator_authorization` configuration profile

`list-open` requires `SPYGLASS_PRIVACY_RIGHTS_DUE_BEFORE` as an explicit RFC3339 timestamp and accepts `SPYGLASS_PRIVACY_RIGHTS_LIMIT` from 1 through 100, defaulting to 100. Both values are bound into the short-lived authorization scope.

`inspect`, `start-review` and `resolve` require `SPYGLASS_PRIVACY_RIGHTS_REQUEST_ID`. `start-review` and `resolve` additionally require `SPYGLASS_PRIVACY_RIGHTS_VERSION`. `resolve` also requires:

- `SPYGLASS_PRIVACY_RIGHTS_RESOLUTION_STATE`
- `SPYGLASS_PRIVACY_RIGHTS_EVIDENCE_ID`
- `SPYGLASS_PRIVACY_RIGHTS_EVIDENCE_SHA256` as exactly 64 hexadecimal characters

Run `inspect` first and use the returned `request_version`. After the authorized fulfillment activity is complete, hash the immutable evidence artifact outside Spyglass, obtain a new action-scoped authorization, and run `resolve`. Never put customer data, case content or legal analysis in an environment variable or operator reason.

## Evidence and failure behavior

- Inspections, review starts and resolutions are append-only events.
- Open-queue access is separately append-only and records the cutoff, limit and result count without copying request or User identities into the access event.
- Submission/cancellation and operator events share one ordered request version history.
- Existing Account API binaries remain write-compatible during a rolling migration; compatibility triggers fill the new event shape and advance versions until the new binary is active.
- Duplicate or stale operator work returns a conflict instead of overwriting the newer state.
- Customers see the resulting state through the existing Privacy route; evidence IDs, digests, staff identity and reasons are never returned through the customer API.
- A decline or partial completion must be supported by the approved legal/operations procedure and its reviewed artifact. The software supplies evidence integrity, not the legal decision.

This local checkpoint does not grant the role in Stage or production and does not authorize a release.

## Fulfillment path inventory

| Scope | Immediate customer control | Tracked path for the remaining rights |
|---|---|---|
| Identity | verified login-email correction and session/authentication review | access, portability, erasure, restriction and objection use the verified request queue and reviewed evidence artifact |
| Account | owner-governed portable ZIP export and audited closure/erasure workflow | non-owner access, correction, restriction and objection use the verified request queue |
| Affiliate | passkey-confirmed privacy-safe JSON export | correction, restriction, objection, closure and legally limited erasure use the verified request queue |
| Analytics | consent withdrawal and immediate current-browser subject erasure | identity-linked access, portability, correction, broader erasure, restriction and objection use the verified request queue |

The operator boundary records discovery, review and evidence integrity; it does not turn a queue transition into fulfillment. The approved staff procedure must identify the applicable direct workflow or produce and review the scoped artifact before `resolve` is authorized.

## Customer communication checkpoint

The Vue Privacy route can explicitly refresh the existing customer-safe request list, preserves previously rendered history when that read fails, displays the last recorded update and explains every open and terminal lifecycle state. Completed, partially completed and declined outcomes tell the customer that the reviewed response or next steps are delivered separately; the browser does not infer fulfillment from the state alone. Operator evidence identifiers and hashes, staff identity, operational reasons and fulfillment content remain excluded from the customer API and UI.
