BEGIN;

CREATE TABLE user_recovery_code_sets (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    version bigint NOT NULL CHECK (version>0),
    created_at timestamptz NOT NULL,
    UNIQUE (id,user_id)
);

CREATE TABLE user_recovery_codes (
    set_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    position smallint NOT NULL CHECK (position BETWEEN 1 AND 10),
    code_hash bytea NOT NULL UNIQUE CHECK (octet_length(code_hash)=32),
    created_at timestamptz NOT NULL,
    used_at timestamptz,
    PRIMARY KEY (set_id,position),
    FOREIGN KEY (set_id,user_id) REFERENCES user_recovery_code_sets (id,user_id) ON DELETE CASCADE,
    CONSTRAINT recovery_code_use_time CHECK (used_at IS NULL OR used_at>=created_at)
);
CREATE INDEX user_recovery_codes_available ON user_recovery_codes (user_id,set_id,position) WHERE used_at IS NULL;

CREATE TABLE passkey_recovery_grants (
    session_id uuid PRIMARY KEY REFERENCES sessions (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CONSTRAINT passkey_recovery_grant_window CHECK (expires_at>created_at)
);
CREATE INDEX passkey_recovery_grants_expiry ON passkey_recovery_grants (expires_at);

ALTER TABLE user_security_events DROP CONSTRAINT user_security_events_event_type_check;
ALTER TABLE user_security_events ADD CONSTRAINT user_security_events_event_type_check CHECK (event_type IN (
    'session_created','session_reauthenticated','session_revoked','sessions_revoked','credential_recovered',
    'passkey_added','passkey_removed','passkey_authenticated','passkey_reauthenticated','passkey_clone_warning',
    'recovery_codes_rotated','recovery_code_consumed'
));

COMMIT;
