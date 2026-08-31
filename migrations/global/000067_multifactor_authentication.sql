BEGIN;

CREATE TABLE user_mfa_methods (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('sms','email')),
    destination_ciphertext bytea,
    destination_nonce bytea,
    destination_key_version integer,
    destination_fingerprint bytea NOT NULL CHECK (octet_length(destination_fingerprint)=32),
    destination_hint text NOT NULL CHECK (char_length(destination_hint) BETWEEN 3 AND 80),
    created_at timestamptz NOT NULL,
    last_used_at timestamptz,
    CONSTRAINT user_mfa_destination_envelope CHECK (
        (kind='sms' AND destination_ciphertext IS NOT NULL AND destination_nonce IS NOT NULL AND destination_key_version>0)
        OR (kind='email' AND destination_ciphertext IS NULL AND destination_nonce IS NULL AND destination_key_version IS NULL)
    ),
    CONSTRAINT user_mfa_use_time CHECK (last_used_at IS NULL OR last_used_at>=created_at),
    UNIQUE (user_id,kind,destination_fingerprint)
);
CREATE INDEX user_mfa_methods_user ON user_mfa_methods (user_id,created_at,id);

CREATE TABLE user_mfa_challenges (
    id uuid PRIMARY KEY,
    result_method_id uuid,
    method_id uuid REFERENCES user_mfa_methods (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id uuid NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    purpose text NOT NULL CHECK (purpose IN ('enrollment','reauthentication')),
    kind text NOT NULL CHECK (kind IN ('sms','email')),
    destination_ciphertext bytea,
    destination_nonce bytea,
    destination_key_version integer,
    destination_fingerprint bytea NOT NULL CHECK (octet_length(destination_fingerprint)=32),
    destination_hint text NOT NULL CHECK (char_length(destination_hint) BETWEEN 3 AND 80),
    code_hash bytea NOT NULL CHECK (octet_length(code_hash)=32),
    failed_attempts smallint NOT NULL DEFAULT 0 CHECK (failed_attempts BETWEEN 0 AND 5),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT user_mfa_challenge_shape CHECK (
        (purpose='enrollment' AND result_method_id IS NOT NULL AND method_id IS NULL)
        OR (purpose='reauthentication' AND result_method_id IS NULL AND method_id IS NOT NULL)
    ),
    CONSTRAINT user_mfa_challenge_destination CHECK (
        (kind='sms' AND destination_ciphertext IS NOT NULL AND destination_nonce IS NOT NULL AND destination_key_version>0)
        OR (kind='email' AND destination_ciphertext IS NULL AND destination_nonce IS NULL AND destination_key_version IS NULL)
    ),
    CONSTRAINT user_mfa_challenge_window CHECK (expires_at>created_at),
    CONSTRAINT user_mfa_challenge_consumption CHECK (consumed_at IS NULL OR consumed_at>=created_at)
);
CREATE INDEX user_mfa_challenges_active ON user_mfa_challenges (user_id,session_id,created_at)
    WHERE consumed_at IS NULL;

ALTER TABLE sessions DROP CONSTRAINT sessions_authentication_method_check;
ALTER TABLE sessions DROP CONSTRAINT sessions_reauthentication_method_check;
ALTER TABLE sessions
    ADD CONSTRAINT sessions_authentication_method_check CHECK (authentication_method IN ('password','passkey','oidc','sms_otp','email_otp')),
    ADD CONSTRAINT sessions_reauthentication_method_check CHECK (reauthentication_method IN ('password','passkey','oidc','sms_otp','email_otp'));

ALTER TABLE identity_notification_outbox DROP CONSTRAINT identity_notification_outbox_kind_check;
ALTER TABLE identity_notification_outbox ADD CONSTRAINT identity_notification_outbox_kind_check CHECK (kind IN (
    'verification','invitation','recovery','discard','ownership_transfer','contact_change','subscription_lifecycle',
    'mfa_email','mfa_sms'
));

ALTER TABLE user_security_events DROP CONSTRAINT user_security_events_event_type_check;
ALTER TABLE user_security_events ADD CONSTRAINT user_security_events_event_type_check CHECK (event_type IN (
    'session_created','session_reauthenticated','session_revoked','sessions_revoked','credential_recovered',
    'passkey_added','passkey_removed','passkey_renamed','passkey_compromised','passkey_authenticated',
    'passkey_reauthenticated','passkey_clone_warning','recovery_codes_rotated','recovery_code_consumed',
    'primary_email_change_requested','primary_email_changed','mfa_method_added','mfa_reauthenticated'
));

COMMIT;
