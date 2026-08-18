# Passkey Envelope-Key Rotation Operations

- Status: executable operator contract
- Scope: encrypted WebAuthn credential and ceremony envelopes in the global database

Passkey rotation is a staged migration, not a secret replacement. Removing a key while any credential or ceremony still uses its version causes customer factor loss. `spyglass passkey-admin inspect|reencrypt` exists so an operator can prove the old version has reached zero before changing the deployed keyring. Each invocation also verifies the signed exact-scope authorization in [Platform Operator Authorization](operator-authorization.md) before opening PostgreSQL.

## Invariants

1. Every configured version maps to exactly one independent 32-byte AES key.
2. The active version must be present in the keyring. New and updated envelopes use only that version.
3. Old configured versions are decrypt-only. A stored version is never guessed or silently mapped to another key.
4. Re-encryption authenticates the old ciphertext and record-bound associated data before writing a fresh nonce and active-version ciphertext.
5. A missing, wrong, or tampered old key rolls back the entire batch.
6. Batches are limited to 500 envelopes and serialized with a PostgreSQL advisory lock. Row updates also compare the old version and ciphertext.
7. Inspection and re-encryption append immutable, content-free operator events. Logs and events contain versions and counts, never credential IDs, User IDs, ciphertext, or keys.
8. No account-api rollout may remove an old key until two inspections around the rollout window both show zero old credentials and ceremonies.

## Restricted database authority

Create a short-lived operator role through the environment's privileged database workflow. It needs connection plus schema usage and only the encrypted passkey columns required by the adapter:

```sql
GRANT USAGE ON SCHEMA public TO spyglass_passkey_rotation_operator;
GRANT SELECT (credential_id,user_id,encrypted_credential,encryption_nonce,encryption_key_version)
    ON public.passkey_credentials TO spyglass_passkey_rotation_operator;
GRANT UPDATE (encrypted_credential,encryption_nonce,encryption_key_version)
    ON public.passkey_credentials TO spyglass_passkey_rotation_operator;
GRANT SELECT (id,kind,user_id,session_id,encrypted_session_data,encryption_nonce,encryption_key_version)
    ON public.passkey_ceremonies TO spyglass_passkey_rotation_operator;
GRANT UPDATE (encrypted_session_data,encryption_nonce,encryption_key_version)
    ON public.passkey_ceremonies TO spyglass_passkey_rotation_operator;
GRANT SELECT,INSERT ON public.passkey_key_rotation_operator_events TO spyglass_passkey_rotation_operator;
```

The role receives no User email, password identity, session token, Membership, Account, billing, serving, Stripe, SMTP, or migration authority. Revoke the credential when the rotation is complete.

## Rotation procedure

Assume version 1 is live and version 2 is new.

1. Generate version 2 in the environment KMS/secret manager. Never copy it into source control, a ticket, shell history, or logs.
2. Deploy account-api with `SPYGLASS_PASSKEY_ENCRYPTION_KEYS=1=<old>,2=<new>` and `SPYGLASS_PASSKEY_ENCRYPTION_ACTIVE_VERSION=2`. Keep version 1 present.
3. Complete the account-api rollout. Verify readiness and a dedicated non-customer passkey login/registration canary. New envelopes must report version 2.
4. Run an attributed inspection with both keys loaded:

   ```text
   spyglass passkey-admin inspect
   ```

5. Run bounded batches until `credential_versions` and `ceremony_versions` contain no non-active count:

   ```text
   SPYGLASS_PASSKEY_REENCRYPT_BATCH=100 spyglass passkey-admin reencrypt
   ```

6. Inspect again. Review the immutable operator events and database backup/replica health.
7. Wait at least the three-minute ceremony TTL plus the maximum deployment overlap window. Inspect once more to catch an old replica that wrote version 1.
8. Remove version 1 from the account-api and operator keyrings, retain active version 2, roll out, and repeat the passkey canary.
9. Destroy version 1 only after the organization-specific recovery/escrow interval and approval record have both completed.

Every invocation requires `SPYGLASS_DATABASE_URL`, `SPYGLASS_OPERATOR_ID`, `SPYGLASS_OPERATOR_REASON`, `SPYGLASS_ENVIRONMENT`, matching `SPYGLASS_CONFIRM_ENVIRONMENT`, `SPYGLASS_PASSKEY_ENCRYPTION_KEYS`, and `SPYGLASS_PASSKEY_ENCRYPTION_ACTIVE_VERSION`. Re-encryption additionally accepts `SPYGLASS_PASSKEY_REENCRYPT_BATCH`, default 100 and maximum 500.

## Failure and rollback

- If inspection or re-encryption cannot authenticate an old envelope, stop. Restore the exact old key mapping; do not relabel versions or delete the record.
- Before version 1 is removed, rollback means redeploying version 1 as active while retaining version 2 for reads. Envelopes already moved to version 2 remain readable.
- After version 1 is removed but before it is destroyed, add it back to the keyring if an old writer or restored backup is discovered.
- If an old key has been destroyed while old-version rows remain, database mutation cannot repair the ciphertext. Treat it as a factor-loss incident and use the governed customer recovery path.
- A restored database must be inspected with every key version present in that backup before serving. Passkey rotation does not override the Account-erasure restore gate.
