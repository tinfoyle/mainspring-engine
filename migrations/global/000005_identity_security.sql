BEGIN;

ALTER TABLE sessions
    ADD COLUMN reauthenticated_at timestamptz,
    ADD COLUMN client_label text NOT NULL DEFAULT 'Unknown browser';
UPDATE sessions SET reauthenticated_at=authenticated_at WHERE reauthenticated_at IS NULL;
ALTER TABLE sessions ALTER COLUMN reauthenticated_at SET NOT NULL;
ALTER TABLE sessions ADD CONSTRAINT sessions_reauthentication_time CHECK (
    reauthenticated_at >= authenticated_at AND reauthenticated_at <= last_seen_at
);

CREATE TABLE user_security_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    session_id uuid REFERENCES sessions (id) ON DELETE SET NULL,
    event_type text NOT NULL CHECK (event_type IN (
        'session_created','session_reauthenticated','session_revoked','sessions_revoked'
    )),
    occurred_at timestamptz NOT NULL
);
CREATE INDEX user_security_events_user_time ON user_security_events (user_id,occurred_at DESC,id DESC);

COMMIT;
