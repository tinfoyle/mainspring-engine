# Operations Console operations

- Status: Stage enablement authorized on 2026-09-04; see the release handoff for deployed artifact and human acceptance status
- Stage origin: `https://ops.stage.infiniteocean.net`; [step-by-step admin guide](stage-admin-guide.md)
- Local origin: `https://ops.infiniteocean.localhost:8444`
- Services: separate Vue staff UI and `operations-api`
- Authentication: Stage requires Google plus an independent TOTP authenticator; isolated `__Host-spyglass_operations` session cookie
- Contract: `api/operations.openapi.json`

## Boundary

The Operations Console is a staff tool, not a customer impersonation system. A staff session keeps the staff User as the actor at all times. It cannot mint a customer session, impersonate a passkey, bypass Account authority, expose secrets or payment instruments, or silently mutate customer data.

Customer support inspection requires a Google-and-authenticator-verified staff User with `support` or `operations_administrator`, an exact User/Account lookup, a support ticket and plain-language reason, an exact User and Account scope, and a five-to-sixty-minute grant. The UI defaults to fifteen minutes.

The resulting view is read-only and always displays a persistent support-access banner. Closing it revokes the grant. Expiry, explicit revocation, role revocation and staff suspension all deny later access. Grant creation, each view and revocation are immutable, staff-attributed and customer-visible in Account security history and Account export evidence.

## Roles and exposed actions

| Role | Console authority |
|---|---|
| `support` | exact customer lookup, time-bounded read-only Account view and support history |
| `analytics` | cohort-protected landing, checkout and onboarding aggregate reports |
| `billing` | classified failure reports, exact Stripe-event replay request and exact subscription refresh request |
| `privacy` | content-minimized due queue, exact request inspection, version-fenced review start and evidence-bound resolution |
| `affiliate` | exact enrollment inspection, content-free risk signals and version-fenced reactivate/suspend/close |
| `operations_administrator` | all console actions, including traffic IPs and access logs; does not grant database, customer or deployment authority |

Every console action requires a ticket and reason. The API records the immutable staff User ID as `operations/<user-id>` when invoking an existing operator boundary. The billing, privacy and Affiliate pools have execute-only privileges for their specific security-definer functions. The identity pool is limited to reviewed User/staff/passkey/session/security-rate-limit relations, and cannot read Accounts or business data. The support projection pool can execute only Operations projection functions plus read the restore checkpoint required by the service readiness gate. It has no direct customer-table access.

The console intentionally excludes Affiliate settlement/check issuance, billing credits, legal-hold or retention decisions, money adjustment, privacy evidence creation, and Account erasure execution. Those remain separately governed procedures. New actions must be added one at a time with a named role, exact API contract, narrow database function, immutable evidence and denial tests.

## Staff governance

Role governance is deliberately offline. The target must already be an active ordinary User with an existing Google identity. Role assignment permits first-time authenticator enrollment, never dashboard access on Google alone. Use the migration credential only for this command; never place it in the console service.

```sh
spyglass operations-staff show --email=staff@example.com

spyglass operations-staff assign \
  --email=staff@example.com \
  --role=support \
  --actor=authorizing-owner \
  --reason="Approve support duty for the launch review."

spyglass operations-staff revoke \
  --email=staff@example.com \
  --role=support \
  --actor=authorizing-owner \
  --reason="Support duty ended; revoke console authority."
```

Set `SPYGLASS_GLOBAL_MIGRATION_DATABASE_URL` and `SPYGLASS_ENVIRONMENT` for the command. Assignment/revocation and staff suspension/reactivation are immutable governance events. Revoking a User's final role suspends Operations access and revokes active Operations sessions.

## Local operation and certification

Run only from UbuntuRojo:

```sh
cd deploy/docker/spyglass
docker compose --project-name spyglass --env-file env/local.env \
  --file compose.yml --file compose.local.yml up -d
```

The local role installer creates five distinct login roles: `spyglass_operations_identity`, `spyglass_operations_projection`, `spyglass_operations_billing`, `spyglass_operations_privacy` and `spyglass_operations_affiliate`. Values in `env/local.env` are local-only and must never be reused in Stage or production.

The disposable browser certificate creates two ordinary Users, enrolls a virtual passkey, pauses for offline staff assignment, then exercises support, analytics, billing, privacy, Affiliate, mobile and accessibility behavior. See `ui/tests/browser/operations-console-live.spec.ts`. Physical authenticator and assistive-technology checks remain human release gates.

## Privacy, retention and incident response

Operations sessions are revocable and expire after eight hours, with a thirty-minute inactivity limit and fifteen-minute reauthentication window. Support grants expire in at most one hour. Expired/revoked rows and immutable access evidence remain available for audit until the associated Account is erased.

Operations support evidence contains customer identifiers and therefore participates in the independently approved Account-erasure transaction. Outside that exact transaction it cannot be updated or deleted. PostgreSQL integration coverage proves both sides of this rule. Staff governance evidence is retained independently because it describes platform authority rather than customer activity; normal User privacy procedures still apply to the staff identity.

For suspected misuse: revoke the staff role offline, verify the resulting staff state, preserve the immutable governance/access evidence, rotate the affected Operations database credential if compromise is possible, and follow the incident procedure. Never delete or edit evidence to remediate an incident.

## Environment release gates

Before enabling this console outside local Docker:

- choose a dedicated Operations subdomain and set the exact origin; do not share the customer origin or cookie;
- provision five unique database credentials with the checked least-authority grants; billing, privacy and Affiliate remain execute-only;
- provision Google-connected staff identities, grant roles offline, and complete authenticator enrollment;
- verify no raw-table privilege, cross-origin request, password-only login or customer-session reuse is possible;
- run the PostgreSQL, generated-contract, Docker and browser certificates against the candidate artifact;
- complete owner workflow review, physical authenticator-app setup review and assistive-technology review; and
- document monitoring, backup and credential rotation for that environment.

The owner authorized Stage analytics, traffic logging and the secure modular admin console on 2026-09-04 and explicitly retained the passkey requirement. This supersedes the former RC.9 Stage hold. Production/LKE remains outside that authorization. See the current release handoff for deployment evidence and any outstanding physical-passkey acceptance.


## Authenticator implementation (RC.47)

The owner authorized replacing the Stage passkey requirement with Google plus
an authenticator on 2026-09-05. This supersedes earlier passkey-only Stage gates.
The customer passkey implementation is unchanged. Historical passkey certificates
describe the previous admin mechanism, not current acceptance.

Google's existing app callback performs authorization-code exchange with PKCE,
state and nonce verification. The app signs no customer session during the
admin flow. It returns an encrypted one-minute assertion in a POST body to the
fixed admin origin. The admin API requires the exact app Origin, a matching
host-only browser challenge cookie and a previously unused database challenge.
Identity resolution uses the pre-connected Google issuer and subject, never
email matching. Challenges expire after ten minutes.

TOTP uses the interoperable RFC 6238 SHA-1/6-digit/30-second profile, accepting
at most one step either side of server time. A database row lock serializes
verification and prevents replay across sessions and concurrent API workers.
Successful verification, credential changes, challenge consumption, session
creation and immutable authentication audit commit in one transaction.
Per-user failures are limited to five in fifteen minutes; sign-in starts also
have a distributed network budget. Setup secrets use AES-GCM with a dedicated
derived keyring and a User-bound label. Key versions are explicit. Retain old
root key versions until their envelopes have been rotated.

Recovery codes contain 128 random bits each and are stored only as SHA-256
hashes. Consuming one revokes admin sessions and locks the old authenticator;
the recovery challenge only permits replacement enrollment. Replacement
rotates all recovery codes and invalidates other pending challenges.
No seed, code or assertion is logged. The operations identity database role
can access only its authentication tables and append authentication events;
it cannot grant staff roles or mutate audit history.

The new tables are operations_authenticators, operations_login_challenges and
operations_authentication_events. Authentication events contain only User ID,
event name and timestamp. Login challenges are swept after expiry on the next
sign-in start. The staff authentication records are protected administrative
identity material, not customer analytics. The existing staff governance and
restricted support projection boundaries remain in force.
