BEGIN;

CREATE TABLE identity_notification_outbox (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('verification','invitation','recovery','discard')),
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,
    key_version integer NOT NULL CHECK (key_version > 0),
    processing_state text NOT NULL CHECK (processing_state IN ('queued','processing','failed','delivered','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz,
    lease_expires_at timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL,
    delivered_at timestamptz,
    CONSTRAINT identity_notification_delivery_time CHECK (delivered_at IS NULL OR delivered_at >= created_at)
);
CREATE INDEX identity_notification_claim
    ON identity_notification_outbox (COALESCE(next_attempt_at,created_at),created_at,id)
    WHERE processing_state IN ('queued','failed','processing');

COMMIT;
