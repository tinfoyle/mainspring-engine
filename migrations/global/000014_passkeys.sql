BEGIN;

CREATE TABLE passkey_users (
    user_id uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    user_handle bytea NOT NULL UNIQUE CHECK (octet_length(user_handle)=32),
    created_at timestamptz NOT NULL
);

CREATE TABLE passkey_credentials (
    credential_id bytea PRIMARY KEY CHECK (octet_length(credential_id) BETWEEN 1 AND 1024),
    user_id uuid NOT NULL REFERENCES passkey_users (user_id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name=btrim(name) AND char_length(name) BETWEEN 2 AND 80),
    encrypted_credential bytea NOT NULL CHECK (octet_length(encrypted_credential)>16),
    encryption_nonce bytea NOT NULL CHECK (octet_length(encryption_nonce)=12),
    encryption_key_version integer NOT NULL CHECK (encryption_key_version>0),
    sign_count bigint NOT NULL CHECK (sign_count BETWEEN 0 AND 4294967295),
    created_at timestamptz NOT NULL,
    last_used_at timestamptz,
    CONSTRAINT passkey_credentials_use_time CHECK (last_used_at IS NULL OR last_used_at>=created_at)
);
CREATE INDEX passkey_credentials_user ON passkey_credentials (user_id,created_at,credential_id);

CREATE TABLE passkey_ceremonies (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('registration','login','reauthentication')),
    user_id uuid REFERENCES users (id) ON DELETE CASCADE,
    session_id uuid REFERENCES sessions (id) ON DELETE CASCADE,
    encrypted_session_data bytea NOT NULL CHECK (octet_length(encrypted_session_data)>16),
    encryption_nonce bytea NOT NULL CHECK (octet_length(encryption_nonce)=12),
    encryption_key_version integer NOT NULL CHECK (encryption_key_version>0),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT passkey_ceremony_scope CHECK (
        (kind='login' AND user_id IS NULL AND session_id IS NULL) OR
        (kind IN ('registration','reauthentication') AND user_id IS NOT NULL AND session_id IS NOT NULL)
    ),
    CONSTRAINT passkey_ceremony_time CHECK (
        expires_at>created_at AND (consumed_at IS NULL OR consumed_at>=created_at)
    )
);
CREATE INDEX passkey_ceremonies_expiry ON passkey_ceremonies (expires_at) WHERE consumed_at IS NULL;
CREATE INDEX passkey_ceremonies_session ON passkey_ceremonies (session_id,expires_at) WHERE consumed_at IS NULL;

ALTER TABLE user_security_events DROP CONSTRAINT user_security_events_event_type_check;
ALTER TABLE user_security_events ADD CONSTRAINT user_security_events_event_type_check CHECK (event_type IN (
    'session_created','session_reauthenticated','session_revoked','sessions_revoked','credential_recovered',
    'passkey_added','passkey_removed','passkey_authenticated','passkey_reauthenticated','passkey_clone_warning'
));

COMMIT;
