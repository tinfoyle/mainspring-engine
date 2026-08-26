# Privacy-rights fulfillment operations

- Status: locally constructed and certified; environment role grants and staff procedure approval remain release work
- Process mode: `spyglass privacy-rights-admin`
- Database scope: global PostgreSQL only
- Related architecture: [Privacy, analytics and Affiliate architecture](privacy-analytics-affiliates.md)

## Boundary

Customers submit strongly authenticated access, correction, erasure, restriction, objection and portability requests from the Vue Privacy route. The operator command records the staff-side lifecycle after the underlying work has actually been performed:

1. `inspect` reads one exact request and appends an immutable access event;
2. `start-review` moves only `submitted` to `in_review` against the exact current version; and
3. `resolve` moves only `in_review` to `completed`, `partially_completed` or `declined` against the exact current version.

`resolve` requires an opaque evidence UUID and the SHA-256 digest of the reviewed fulfillment artifact. The command never accepts the artifact, customer content, an email address, a case narrative or a free-form response. Actual export, correction, restriction, objection or erasure work remains in its governed application workflow; this command cannot claim that work happened merely by invoking it. Affiliate access and portability can now be fulfilled immediately through the strong-authenticated self-service JSON export; a tracked request is still available when the customer needs reviewed assistance or another right.

Every action requires the standard short-lived phishing-resistant operator authorization envelope, an exact environment confirmation and a content-minimized operational reason. Authorization is bound to action, environment, request ID, expected version, outcome and evidence binding. A stale version or invalid state fails without writing a transition.

## Execute-only database role

Provision the SQL role `spyglass_privacy_rights_operator` as the environment credential mapped to the inventory role `global.privacy-rights-operator`. It receives no direct table privileges:

```sql
GRANT USAGE ON SCHEMA public TO spyglass_privacy_rights_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_inspect_privacy_rights_request(uuid,uuid,text,text,text)
  TO spyglass_privacy_rights_operator;
GRANT EXECUTE ON FUNCTION public.spyglass_transition_privacy_rights_request(uuid,uuid,bigint,text,text,uuid,bytea,text,text,text)
  TO spyglass_privacy_rights_operator;
```

Migration 43 revokes both functions from `PUBLIC`. Fresh-PostgreSQL integration coverage proves the role has function execution but no direct `privacy_rights_requests` read authority.

## Required configuration

All actions require:

- `SPYGLASS_DATABASE_URL`
- `SPYGLASS_PRIVACY_RIGHTS_REQUEST_ID`
- `SPYGLASS_OPERATOR_ID`, `SPYGLASS_OPERATOR_REASON`
- `SPYGLASS_ENVIRONMENT`, `SPYGLASS_CONFIRM_ENVIRONMENT`
- the standard `operator_authorization` configuration profile

`start-review` and `resolve` additionally require `SPYGLASS_PRIVACY_RIGHTS_VERSION`. `resolve` also requires:

- `SPYGLASS_PRIVACY_RIGHTS_RESOLUTION_STATE`
- `SPYGLASS_PRIVACY_RIGHTS_EVIDENCE_ID`
- `SPYGLASS_PRIVACY_RIGHTS_EVIDENCE_SHA256` as exactly 64 hexadecimal characters

Run `inspect` first and use the returned `request_version`. After the authorized fulfillment activity is complete, hash the immutable evidence artifact outside Spyglass, obtain a new action-scoped authorization, and run `resolve`. Never put customer data, case content or legal analysis in an environment variable or operator reason.

## Evidence and failure behavior

- Inspections, review starts and resolutions are append-only events.
- Submission/cancellation and operator events share one ordered request version history.
- Existing Account API binaries remain write-compatible during a rolling migration; compatibility triggers fill the new event shape and advance versions until the new binary is active.
- Duplicate or stale operator work returns a conflict instead of overwriting the newer state.
- Customers see the resulting state through the existing Privacy route; evidence IDs, digests, staff identity and reasons are never returned through the customer API.
- A decline or partial completion must be supported by the approved legal/operations procedure and its reviewed artifact. The software supplies evidence integrity, not the legal decision.

This local checkpoint does not grant the role in Stage or production and does not authorize a release.
