# Identity Security and Passkeys

- Status: Executable Phase 2 identity slice
- Scope: system-wide User authentication; never Account authorization

## 1. Boundary and invariants

Spyglass authenticates one Infinite Ocean `User` before it resolves any Spyglass `Account`. A passkey proves control of a credential for that User. It does not prove Membership, role, selected Account, placement, package access, or billing authority.

```text
passkey/password proof -> User session
User session + Account selection -> active Membership and placement
Account context + operation -> role, object, package, and usage authorization
```

Passkey endpoints accept no Account ID, passkey records have no Account foreign key, and a successful passkey login issues the same global session type as password login. Account switching never duplicates or rebinds a credential.

Required invariants:

- WebAuthn ceremonies are generated server-side, expire after three minutes, are scoped to their ceremony kind, and are consumed once before response validation.
- First-ever registration requires recent password authentication; adding another passkey and removing any passkey require a user-verified passkey timestamp no older than ten minutes.
- Removing the last passkey is transactionally rejected until at least one unused recovery code exists; concurrent deletions are serialized per User so two final factors cannot both bypass the check. Replacing a lost final passkey requires recent password authentication plus a single-use recovery-code grant bound to that exact User session.
- Recovery codes are generated as ten independent 128-bit values, returned once, stored only as domain-separated SHA-256 hashes, consumed atomically, and never grant Account or operator authority.
- Registration and assertion both require user presence and user verification; discoverable credentials are required for email-less sign-in.
- The relying-party ID and exact allowed origin are deployment configuration, not request values.
- User handles are random opaque 32-byte values and do not contain email addresses or Account identifiers.
- Credential IDs support lookup, while the complete credential record and ceremony session data are encrypted with AES-256-GCM and record-bound associated data.
- Every successful assertion updates the authenticator counter with compare-and-swap. A concurrent or stale counter update fails instead of overwriting newer state.
- Authenticator clone warnings are rejected and recorded as security events.
- At most ten passkeys may be registered for one User.
- Renaming a passkey requires recent user-verified cryptographic assurance and changes only its human-readable identity-level label.
- Reporting a passkey as compromised requires the same assurance, but deliberately allows removal of the last passkey. One global-database transaction removes the credential, advances the User security version, revokes every active session, and records both incident and revocation events; the browser then expires identity cookies.
- Durable sessions record both the initial authentication method and the latest reauthentication method. Password maps to `single_factor`; a user-verified passkey maps to `user_verified_cryptographic`. Neither value contains or grants Account authority.
- Membership role/lifecycle/removal changes, self-service Account leave, ownership transfer, invitation creation, Stripe Checkout creation, and Stripe Customer Portal creation require an active authorized role plus user-verified cryptographic proof no older than ten minutes. Password proof cannot satisfy that privileged-operation policy.
- Every active Owner Membership is additionally gated by a system-wide owner-readiness policy: the User must have at least one passkey and an active recovery-code set with at least one unused code. The gate is enforced by the shared Account authorizer for reads and mutations, not only by the browser. Identity and recovery endpoints remain available so an unready owner can enroll or recover.
- Owner readiness is derived from durable factor state on every authorization decision. It is not copied into the Membership, session, or Account cookie. Consuming the last recovery code immediately makes the owner unready until a new set is generated; losing or deleting the last passkey has the same effect.
- A primary-email change requires recent user-verified passkey assurance and proof from the new mailbox. The old email remains the only login until that proof succeeds; completion changes the User and local authentication identifier together, advances `security_version`, revokes every session, and leaves Account Memberships and package access untouched.

## 2. Code ownership

| Boundary | Owner | Responsibility |
|---|---|---|
| Use cases and ports | `internal/application/passkeys` | WebAuthn policy, ceremony lifetime, replay order, counter fencing, session issuance, safe summaries |
| Recovery use cases and ports | `internal/application/recoverycodes` | Passkey-protected set rotation, password-plus-code consumption, session-bound replacement grants |
| Security-posture use case and port | `internal/application/securityposture` | Derive safe User-level factor counts and the owner-readiness decision |
| Account authorization policy | `internal/modules/access` | Apply active Membership, Account, role, package, and mandatory Owner factor gates together |
| Privileged assurance policy | `internal/application/strongauth` | One typed, transport-independent rule for actor binding, assurance class, and ten-minute freshness |
| Verified-contact use case and ports | `internal/application/contactchange` | New-mailbox proof, normalization, single-use lifetime, stale-identity fencing, completion notices |
| Durable adapter | `internal/adapters/postgres/passkeys.go` | Encrypted records, scoped atomic ceremony consumption, credential counter CAS, rename, atomic compromise response, security events |
| Verified-contact adapter | `internal/adapters/postgres/contact_change.go` | Serializable challenge creation/completion, login-identifier update, session revocation, atomic encrypted outbox and security events |
| Recovery adapter | `internal/adapters/postgres/recovery_codes.go` | Hashed code sets, atomic single-use consumption, grant expiry, rotation invalidation, security events |
| Security-posture adapter | `internal/adapters/postgres/security_posture.go` | Aggregate passkey and unused-code state without exposing credential material |
| Development adapter | `internal/adapters/memory/passkeys.go` | Same application port for local journeys and transport tests |
| HTTP adapter | `internal/transport/httpapi` | Bounded JSON, exact-origin checks, session cookies, public problem responses |
| Browser adapter | `internal/transport/browserapp` | Prototype-informed login/security presentation and WebAuthn browser serialization |
| Composition | `internal/bootstrap/accountapi` | RP/origin/key configuration and shared PostgreSQL/session/rate-limit dependencies |
| Schema | `migrations/global/000014_passkeys.sql` | User handles, credentials, ceremonies, indexes, constraints, security-event vocabulary |

The application owns the repository interface. Neither transport imports PostgreSQL concerns, and the PostgreSQL adapter does not make authorization decisions.

## 3. Ceremony flows

### Discoverable login

1. The anonymous client asks for a login challenge. The shared PostgreSQL network budget is consumed before a ceremony is created.
2. The server stores encrypted `SessionData` and returns only public request options plus an opaque ceremony ID and expiry.
3. The browser asks the authenticator for a discoverable credential with user verification required.
4. Completion atomically consumes the exact login ceremony.
5. WebAuthn validation binds type, challenge, exact origin, RP ID hash, user presence, user verification, credential public key, and signature.
6. The credential counter is fenced and updated; only then is a global session issued.
7. Account listing and selection run through the existing Membership/placement authorization boundary.

### Registration

1. First-ever enrollment begins after recent password authentication. A User with an existing passkey must prove that passkey before adding another.
2. The repository creates or loads the User's stable opaque handle.
3. The server requires a resident credential, user verification, and no attestation conveyance preference.
4. Completion consumes a ceremony bound to the same User and session.
5. A verified credential is encrypted and inserted with a human-readable local name and security event.
6. Successful user-verified enrollment promotes the current session's recent assurance to `user_verified_cryptographic`. This makes first enrollment usable after password recovery without treating the password itself as strong proof.

### Lost-passkey replacement

1. A User with a verified passkey creates a set of ten recovery codes. Spyglass returns the plaintext once and persists only hashes.
2. Creating a replacement set invalidates the previous set and every outstanding replacement grant.
3. If every passkey is lost, the User signs in with the password and submits one saved code. The code is atomically consumed and grants only that current session permission to begin replacement enrollment for ten minutes.
4. The replacement WebAuthn ceremony remains bound to the same User and session. Its successful user-verified completion promotes the session to cryptographic assurance.
5. A code or grant cannot select an Account, change Membership, start billing, or invoke platform operations. Password recovery revokes its underlying session, and therefore the grant, through the session foreign key.

The browser always exposes the replacement-code form when a code set exists, even while lost authenticators remain registered in PostgreSQL. Physical loss does not delete credential records, so conditioning this recovery path on an empty server-side passkey list would strand exactly the Users who need it.

The customer-visible boundary is deliberately explicit: Infinite Ocean support cannot read or recreate one-way-hashed codes, impersonate a passkey, issue an owner-ready override, or inherit Account authority. If the User has neither an available authenticator nor a saved unused code, self-service owner recovery is impossible and owner authorization remains locked. A future support-assisted exception, if product and security approve one, must be a separately modeled delayed and multi-party governed workflow; it must never be a database edit or transport bypass.

### Passkey reauthentication

1. An authenticated session requests an assertion restricted to its User's registered credentials.
2. Completion consumes a ceremony bound to that exact User and session.
3. Successful cryptographic validation and counter update mark the existing session recently reauthenticated.
4. The timestamp unlocks privileged Account mutations for ten minutes; it grants no new Account role.

### Verified primary-email change

1. An authenticated User confirms with a passkey, then submits a normalized new email through an exact-Origin mutation. Password-only, stale, future-dated, or cross-User assurance is rejected.
2. Spyglass stores one 30-minute pending challenge per User and new address. Only the SHA-256 token hash is durable. The current mailbox receives a security alert while the new mailbox receives the single-use verification link; both encrypted outbox rows and the request security event commit with the challenge.
3. The old primary email and local login identifier remain authoritative while the request is pending. Starting another request consumes the prior pending request without changing the User.
4. Verification serializes the challenge and User, rechecks active/verified state, original email, security version, and global email uniqueness, and prepares completion notices before mutation.
5. One transaction updates `users.primary_email`, the local `authentication_identities.identifier`, `email_verified_at`, and `security_version`; revokes every User session; consumes the challenge; records `primary_email_changed`; and queues notices to both old and new mailboxes. A stale, raced, expired, or replayed token cannot partially change identity state.
6. The User signs in again with the new email. Immutable User ID, Account Memberships, roles, placements, entitlements, and billing state do not move or duplicate.

### Customer-owner enrollment and recovery

1. Free registration still creates the Account and Owner Membership atomically without contacting Stripe. The new User can authenticate and reach global identity/security functions, but cannot enter or operate the owned Account yet.
2. The owner enrolls a user-verified passkey, then generates and saves a recovery-code set. Only both factors together make `owner_ready` true.
3. Account listing returns `owner_enrollment_required` for each owned Account while the User is unready. Account selection and every Account operation independently fail with `owner_security_enrollment_required`; hiding controls in the browser is only presentation.
4. Promotion through ownership transfer does not copy the previous owner's factors or block the atomic transfer. An unready successor becomes the sole owner but is immediately gated until completing their own global factor enrollment. The prior owner is demoted as one transaction and cannot retain owner authority.
5. A lost final passkey can be replaced only through the password-plus-single-use-code flow above. The replacement passkey alone does not reopen owner access if that consumption exhausted the recovery set; the User must rotate a fresh set first.

### Session assurance

The session keeps `authentication_method` separate from `reauthentication_method`. Initial sign-in sets both. A later step-up updates only the reauthentication method and timestamp. For example, a passkey-created session later confirmed with a password remains a passkey-created session, but it no longer satisfies a policy requiring recent user-verified cryptographic proof. Unknown method values are rejected by the application and database constraints.

Active-session API responses expose the two methods and their derived assurance classifications. `strongauth.Require` consumes the session, expected actor, trusted clock, and fixed ten-minute window. Invitation and commercial services call it after Account-role authorization and before persistence or provider calls, so alternate transports cannot bypass the rule. A password confirmation performed after a passkey assertion deliberately replaces the recent assurance and requires another passkey assertion for these operations.

The current privileged set is verified primary-email change, Membership role/lifecycle/removal changes, self-service Account leave, ownership transfer, invitation creation, Checkout creation, and Customer Portal creation. Invitation acceptance, Account selection, Membership/billing reads, password recovery, and first passkey enrollment are not made impossible by this rule. Recovery replaces the password and revokes all sessions; the User signs in with the new password, enrolls a user-verified passkey, and that enrollment establishes the required recent assurance.

## 4. HTTP surface

```text
POST   /api/v1/passkey-login/challenges
POST   /api/v1/passkey-login/challenges/{ceremonyID}/complete
GET    /api/v1/passkeys
POST   /api/v1/passkey-registrations
POST   /api/v1/passkey-registrations/{ceremonyID}/complete
PATCH  /api/v1/passkeys/{credentialID}
DELETE /api/v1/passkeys/{credentialID}
POST   /api/v1/passkeys/{credentialID}/compromise
POST   /api/v1/passkey-reauthentications
POST   /api/v1/passkey-reauthentications/{ceremonyID}/complete
GET    /api/v1/recovery-codes
POST   /api/v1/recovery-codes
POST   /api/v1/recovery-codes/consume
GET    /api/v1/security-posture
POST   /api/v1/contact-change-requests
POST   /api/v1/contact-change-verifications
```

Cookie-authenticated mutations require the configured exact application Origin. Passkey payloads have a dedicated 256 KiB ceiling to accommodate attestation objects while remaining bounded. Errors never reveal whether an anonymous credential ID, user handle, or User exists.

Ordinary deletion protects recovery posture and therefore refuses to remove a final passkey without an unused recovery code. Compromise response is intentionally different: suspected credentials must not remain usable, so it removes even the final passkey and signs the User out everywhere. The next login uses an uncompromised passkey or the documented password-plus-recovery-code replacement flow.

## 5. Persistence and scaling

`passkey_users` is keyed by global User ID and stores the opaque WebAuthn handle. `passkey_credentials` stores the globally unique credential ID, encrypted credential blob, key version, duplicated sign counter for atomic fencing, name, and use timestamps. `passkey_ceremonies` stores encrypted WebAuthn session data and an explicit kind/User/session scope. `user_recovery_code_sets` and `user_recovery_codes` store one active version and its one-way hashes; `passkey_recovery_grants` binds a short-lived grant to a live User session. `primary_email_change_challenges` stores the old/new normalized addresses, initiating security version, 32-byte token hash, expiry, and consumption state; notification bodies and the raw verification token live only in encrypted outbox envelopes.

All state required between begin and complete requests is durable. A request may begin on one account-api replica and complete on another without session affinity. Ceremony consumption is one SQL update guarded by kind, User, session, expiry, and `consumed_at IS NULL`. Creation opportunistically removes a bounded batch of expired ceremonies. The independent `identity-maintenance-worker` also removes at most a configured batch of consumed or expired ceremonies after the retention window. Its status and metrics expose only total/eligible counts, oldest eligible age, prune totals, failures and an alert flag—never User, credential, challenge or ciphertext values.

This design scales with the shared account-api Deployment and global PostgreSQL pool; it creates no User- or Account-specific container.

## 6. Configuration and key operations

Production account-api requires:

- `SPYGLASS_PASSKEY_RP_ID`: the WebAuthn relying-party domain, normally the application host or a deliberately chosen parent domain.
- `SPYGLASS_PASSKEY_ENCRYPTION_KEYS`: comma-separated `positive-version=standard-base64-key` entries, each decoding to exactly 32 bytes and independent from notification/network-actor keys.
- `SPYGLASS_PASSKEY_ENCRYPTION_ACTIVE_VERSION`: the exact positive version used for every new or updated envelope; it must be present in the keyring.
- `SPYGLASS_APP_ORIGIN`: the exact HTTPS browser origin accepted by WebAuthn and mutation-origin checks.

The process writes only with the active version and decrypts only explicitly configured retained versions. The audited `passkey-admin inspect|reencrypt` command migrates at most 500 authenticated credential/ceremony envelopes per invocation with fresh nonces, compare-and-swap updates, and immutable aggregate operator evidence. An old key may be removed only after old-version counts reach zero and the rollout overlap window has elapsed. See [Passkey envelope-key rotation operations](passkey-key-rotation.md).

## 7. Evidence

Automated evidence covers:

- a generated P-256 assertion accepted only with the correct challenge, exact origin, RP ID, signature, presence, and verification flags;
- session issuance only after an atomic credential-counter update;
- durable separation of initial and recent password/passkey assurance, including rejection of a password step-up for a cryptographic-assurance requirement;
- application-layer rejection of password, stale passkey, future-dated, and cross-User evidence before invitation persistence or Stripe calls;
- a generated P-256 registration that promotes a password-created session only after server-validated user verification, followed by a successful HTTP invitation journey;
- rejection of tampered signatures, expired ceremonies, and ceremony replay;
- resident-key/user-verification registration options and User/session ceremony binding;
- ciphertext randomness, label binding, key-version binding, and tamper rejection;
- PostgreSQL encrypted credential/ceremony round trips, stale counter rejection, replay rejection, and cross-User list isolation;
- active-plus-retained keyring reads, active-only writes, bounded PostgreSQL credential/ceremony re-encryption, old-key retirement, and immutable aggregate operator evidence;
- passkey-only recovery-set rotation, plaintext non-persistence, single-use and concurrent code consumption, replacement-set invalidation, session-bound grants, replay rejection, lost-passkey replacement policy, and concurrent final-factor deletion fencing;
- mandatory owner gating before Account selection and operations, safe enrollment flags in Account choices, global factor-enrollment escape paths, and PostgreSQL-derived empty/code-only/ready posture states;
- platform-administrator Ed25519 authorization bound to phishing-resistant assurance, exact action/environment/reason/scope, ten-minute lifetime, and explicit incident/two-approver break-glass evidence before database composition;
- HTTP response contracts containing no Account identity and browser presentation on login and identity security pages.
- customer-visible factor-loss steps and the no-support-bypass boundary, including a template contract proving code replacement remains reachable while unavailable passkeys are still registered.
- verified-contact denial without passkey assurance, normalized same-address rejection, old-login continuity before proof, new-login activation only after proof, challenge replay rejection, all-session revocation, security-version fencing, Account-membership preservation, encrypted old/new mailbox notices, PostgreSQL atomicity, and browser/OpenAPI contracts.
- passkey rename validation and strong-assurance enforcement, final-credential compromise response, repeat/non-owner target rejection, global session invalidation, security-version fencing, exact-Origin HTTP contracts, and identity-cookie expiry.

## 8. Remaining identity work

Connected local identity checkpoint (2026-08-25): Chromium, Firefox, WebKit and a Chromium phone profile now complete registration, encrypted queued verification delivery, password setup, login into the Vue application, anti-enumerating recovery, encrypted reset delivery, old-password rejection and replacement-password login through the exact local TLS/database/notification composition. A fifth profile retains 200% text coverage. A sixth Chromium profile uses a virtual user-verifying resident-key authenticator to create the first passkey, create recovery codes, reach authoritative owner readiness, sign in discoverably, remove the only available authenticator, spend one code after password confirmation, register a replacement while the old credential remains recorded and reauthenticate with that replacement. All eighteen connected checks pass. The exercise corrected native forms rejected with `Origin: null` under `Referrer-Policy: no-referrer`, passkey redirects dereferencing `event.currentTarget` after an asynchronous ceremony, and the backend ignoring a valid recovery grant whenever an inaccessible credential row still existed. Consumed local messages are deleted, credential traces are disabled and the canonical local script resets only its anonymous login/recovery throttle rows. This is automated local WebAuthn and email/password evidence, not physical WebAuthn, release-environment SMTP or assistive-technology certification.

Passkeys are now a production authentication and strong-reauthentication option, but the broader Phase 2 identity program is not complete:

1. Complete product/security/legal review of the executable customer-visible factor-loss copy and decide whether a delayed, multi-party support-assisted exception will ever exist. The current policy is fail-closed with no support bypass. Platform-administrator signed authorization and dual-approved break glass are executable; the external workforce identity plane remains the enrollment and approval authority. Self-service recovery codes deliberately cannot authorize Account or operator actions.
2. Certify the scheduled ceremony-retention alert thresholds under stage load. Bounded pruning, aggregate backlog metrics, restore gating, least-privilege execution and the `/health/status` operator inspection path are executable.
3. Decide whether attestation metadata evaluation is required for managed-enterprise policy; current public customer registration requests no attestation.
4. Complete release-environment SMTP delivery certification for invitations, ownership and verified-contact notices; local registration and recovery delivery are certified through the TLS Mailpit fixture.
5. Run real-browser WebAuthn journeys across supported desktop/mobile platforms and accessibility tooling before release promotion.
