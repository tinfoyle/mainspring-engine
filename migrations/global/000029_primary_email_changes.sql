BEGIN;

CREATE TABLE primary_email_change_challenges (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    old_email text NOT NULL,
    new_email text NOT NULL,
    display_name text NOT NULL,
    security_version bigint NOT NULL CHECK (security_version > 0),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT primary_email_change_old_normalized CHECK (old_email = lower(btrim(old_email))),
    CONSTRAINT primary_email_change_new_normalized CHECK (new_email = lower(btrim(new_email))),
    CONSTRAINT primary_email_change_distinct CHECK (old_email <> new_email),
    CONSTRAINT primary_email_change_expiry CHECK (expires_at > created_at),
    CONSTRAINT primary_email_change_consumption CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);
CREATE UNIQUE INDEX primary_email_change_one_pending_user
    ON primary_email_change_challenges (user_id) WHERE consumed_at IS NULL;
CREATE UNIQUE INDEX primary_email_change_one_pending_email
    ON primary_email_change_challenges (new_email) WHERE consumed_at IS NULL;
CREATE INDEX primary_email_change_cleanup
    ON primary_email_change_challenges (expires_at) WHERE consumed_at IS NULL;

ALTER TABLE identity_notification_outbox
    DROP CONSTRAINT identity_notification_outbox_kind_check;
ALTER TABLE identity_notification_outbox
    ADD CONSTRAINT identity_notification_outbox_kind_check CHECK (kind IN (
        'verification','invitation','recovery','discard','ownership_transfer','contact_change'
    ));

ALTER TABLE user_security_events DROP CONSTRAINT user_security_events_event_type_check;
ALTER TABLE user_security_events ADD CONSTRAINT user_security_events_event_type_check CHECK (event_type IN (
    'session_created','session_reauthenticated','session_revoked','sessions_revoked','credential_recovered',
    'passkey_added','passkey_removed','passkey_authenticated','passkey_reauthenticated','passkey_clone_warning',
    'recovery_codes_rotated','recovery_code_consumed','primary_email_change_requested','primary_email_changed'
));

COMMIT;
