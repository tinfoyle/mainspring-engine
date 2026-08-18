BEGIN;

CREATE TABLE credential_recovery_challenges (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id),
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT credential_recovery_expiry CHECK (expires_at > created_at),
    CONSTRAINT credential_recovery_consumption CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);
CREATE UNIQUE INDEX credential_recovery_one_pending_user
    ON credential_recovery_challenges (user_id) WHERE consumed_at IS NULL;
CREATE INDEX credential_recovery_cleanup ON credential_recovery_challenges (expires_at) WHERE consumed_at IS NULL;

ALTER TABLE user_security_events DROP CONSTRAINT user_security_events_event_type_check;
ALTER TABLE user_security_events ADD CONSTRAINT user_security_events_event_type_check CHECK (event_type IN (
    'session_created','session_reauthenticated','session_revoked','sessions_revoked','credential_recovered'
));

COMMIT;
