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
- Registration and removal require a reauthentication timestamp no older than ten minutes.
- Registration and assertion both require user presence and user verification; discoverable credentials are required for email-less sign-in.
- The relying-party ID and exact allowed origin are deployment configuration, not request values.
- User handles are random opaque 32-byte values and do not contain email addresses or Account identifiers.
- Credential IDs support lookup, while the complete credential record and ceremony session data are encrypted with AES-256-GCM and record-bound associated data.
- Every successful assertion updates the authenticator counter with compare-and-swap. A concurrent or stale counter update fails instead of overwriting newer state.
- Authenticator clone warnings are rejected and recorded as security events.
- At most ten passkeys may be registered for one User.
- Durable sessions record both the initial authentication method and the latest reauthentication method. Password maps to `single_factor`; a user-verified passkey maps to `user_verified_cryptographic`. Neither value contains or grants Account authority.
- Membership role/lifecycle/removal changes, self-service Account leave, ownership transfer, invitation creation, Stripe Checkout creation, and Stripe Customer Portal creation require an active authorized role plus user-verified cryptographic proof no older than ten minutes. Password proof cannot satisfy that privileged-operation policy.

## 2. Code ownership

| Boundary | Owner | Responsibility |
|---|---|---|
| Use cases and ports | `internal/application/passkeys` | WebAuthn policy, ceremony lifetime, replay order, counter fencing, session issuance, safe summaries |
| Privileged assurance policy | `internal/application/strongauth` | One typed, transport-independent rule for actor binding, assurance class, and ten-minute freshness |
| Durable adapter | `internal/adapters/postgres/passkeys.go` | Encrypted records, scoped atomic ceremony consumption, credential counter CAS, security events |
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

1. An authenticated, recently reauthenticated session begins registration.
2. The repository creates or loads the User's stable opaque handle.
3. The server requires a resident credential, user verification, and no attestation conveyance preference.
4. Completion consumes a ceremony bound to the same User and session.
5. A verified credential is encrypted and inserted with a human-readable local name and security event.
6. Successful user-verified enrollment promotes the current session's recent assurance to `user_verified_cryptographic`. This makes first enrollment usable after password recovery without treating the password itself as strong proof.

### Passkey reauthentication

1. An authenticated session requests an assertion restricted to its User's registered credentials.
2. Completion consumes a ceremony bound to that exact User and session.
3. Successful cryptographic validation and counter update mark the existing session recently reauthenticated.
4. The timestamp unlocks privileged Account mutations for ten minutes; it grants no new Account role.

### Session assurance

The session keeps `authentication_method` separate from `reauthentication_method`. Initial sign-in sets both. A later step-up updates only the reauthentication method and timestamp. For example, a passkey-created session later confirmed with a password remains a passkey-created session, but it no longer satisfies a policy requiring recent user-verified cryptographic proof. Unknown method values are rejected by the application and database constraints.

Active-session API responses expose the two methods and their derived assurance classifications. `strongauth.Require` consumes the session, expected actor, trusted clock, and fixed ten-minute window. Invitation and commercial services call it after Account-role authorization and before persistence or provider calls, so alternate transports cannot bypass the rule. A password confirmation performed after a passkey assertion deliberately replaces the recent assurance and requires another passkey assertion for these operations.

The current privileged set is Membership role/lifecycle/removal changes, self-service Account leave, ownership transfer, invitation creation, Checkout creation, and Customer Portal creation. Invitation acceptance, Account selection, Membership/billing reads, password recovery, and first passkey enrollment are not made impossible by this rule. Recovery replaces the password and revokes all sessions; the User signs in with the new password, enrolls a user-verified passkey, and that enrollment establishes the required recent assurance.

## 4. HTTP surface

```text
POST   /api/v1/passkey-login/challenges
POST   /api/v1/passkey-login/challenges/{ceremonyID}/complete
GET    /api/v1/passkeys
POST   /api/v1/passkey-registrations
POST   /api/v1/passkey-registrations/{ceremonyID}/complete
DELETE /api/v1/passkeys/{credentialID}
POST   /api/v1/passkey-reauthentications
POST   /api/v1/passkey-reauthentications/{ceremonyID}/complete
```

Cookie-authenticated mutations require the configured exact application Origin. Passkey payloads have a dedicated 256 KiB ceiling to accommodate attestation objects while remaining bounded. Errors never reveal whether an anonymous credential ID, user handle, or User exists.

## 5. Persistence and scaling

`passkey_users` is keyed by global User ID and stores the opaque WebAuthn handle. `passkey_credentials` stores the globally unique credential ID, encrypted credential blob, key version, duplicated sign counter for atomic fencing, name, and use timestamps. `passkey_ceremonies` stores encrypted WebAuthn session data and an explicit kind/User/session scope.

All state required between begin and complete requests is durable. A request may begin on one account-api replica and complete on another without session affinity. Ceremony consumption is one SQL update guarded by kind, User, session, expiry, and `consumed_at IS NULL`. Creation opportunistically removes a bounded batch of expired ceremonies so ordinary traffic does not create unbounded expired state.

This design scales with the shared account-api Deployment and global PostgreSQL pool; it creates no User- or Account-specific container.

## 6. Configuration and key operations

Production account-api requires:

- `SPYGLASS_PASSKEY_RP_ID`: the WebAuthn relying-party domain, normally the application host or a deliberately chosen parent domain.
- `SPYGLASS_PASSKEY_ENCRYPTION_KEY`: standard Base64 encoding of exactly 32 random bytes, independent from notification and network-actor keys.
- `SPYGLASS_APP_ORIGIN`: the exact HTTPS browser origin accepted by WebAuthn and mutation-origin checks.

The schema records an encryption key version, but the current process loads one active passkey key. Before rotating a live key, implement a multi-version decrypt keyring plus a bounded re-encryption job, prove all rows use the new version, and only then remove the old key. Losing the only configured key makes existing passkeys and unexpired ceremonies unreadable; password recovery remains the identity recovery path.

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
- HTTP response contracts containing no Account identity and browser presentation on login and identity security pages.

## 8. Remaining identity work

Passkeys are now a production authentication and strong-reauthentication option, but the broader Phase 2 identity program is not complete:

1. Extend the now-executable privileged-operation step-up into a complete owner/platform-administrator enrollment and recovery policy, including recovery codes, ownership-transfer rules, factor-loss review, and break-glass governance.
2. Add a multi-version credential-encryption keyring, re-encryption operator, and key-loss/rollback runbook.
3. Add scheduled retention metrics and an operator path for abnormal ceremony growth; opportunistic cleanup remains only the first bound.
4. Decide whether attestation metadata evaluation is required for managed-enterprise policy; current public customer registration requests no attestation.
5. Add verified contact-method change, passkey rename, compromised-credential response, and customer-visible notification delivery.
6. Run real-browser WebAuthn journeys across supported desktop/mobile platforms and accessibility tooling before release promotion.
