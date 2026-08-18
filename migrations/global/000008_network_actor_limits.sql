BEGIN;

CREATE TABLE network_actor_rate_limits (
    scope text NOT NULL CHECK (scope IN ('identity_login','identity_recovery')),
    actor_hash bytea NOT NULL CHECK (octet_length(actor_hash) = 32),
    window_started_at timestamptz NOT NULL,
    attempt_count integer NOT NULL CHECK (attempt_count > 0),
    blocked_until timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (scope,actor_hash)
);
CREATE INDEX network_actor_rate_limits_cleanup ON network_actor_rate_limits (updated_at);

COMMIT;
