BEGIN;

ALTER TABLE accounts ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version > 0);

CREATE TABLE account_closure_requests (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    state text NOT NULL CHECK (state IN ('cooling_off','processing','blocked','canceled','closed')),
    requested_by_user_id uuid NOT NULL REFERENCES users (id),
    reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 3 AND 300 AND reason=btrim(reason)),
    account_version bigint NOT NULL CHECK (account_version > 0),
    requested_at timestamptz NOT NULL,
    execute_after timestamptz NOT NULL CHECK (execute_after > requested_at),
    next_attempt_at timestamptz NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    lease_expires_at timestamptz,
    blocker_code text CHECK (blocker_code IN ('billing_active','checkout_active')),
    canceled_by_user_id uuid REFERENCES users (id),
    cancel_reason text CHECK (cancel_reason IS NULL OR char_length(btrim(cancel_reason)) BETWEEN 3 AND 300),
    canceled_at timestamptz,
    closed_at timestamptz,
    delete_after timestamptz,
    CONSTRAINT account_closure_request_terminal_shape CHECK (
        (state IN ('cooling_off','processing') AND blocker_code IS NULL AND canceled_at IS NULL AND closed_at IS NULL AND delete_after IS NULL)
        OR (state='blocked' AND blocker_code IS NOT NULL AND canceled_at IS NULL AND closed_at IS NULL AND delete_after IS NULL)
        OR (state='canceled' AND blocker_code IS NULL AND canceled_by_user_id IS NOT NULL AND cancel_reason IS NOT NULL AND canceled_at IS NOT NULL AND closed_at IS NULL AND delete_after IS NULL)
        OR (state='closed' AND blocker_code IS NULL AND canceled_at IS NULL AND closed_at IS NOT NULL AND delete_after > closed_at)
    )
);
CREATE UNIQUE INDEX account_closure_one_current ON account_closure_requests (account_id) WHERE state IN ('cooling_off','processing','blocked');
CREATE INDEX account_closure_claim ON account_closure_requests (next_attempt_at,id) WHERE state IN ('cooling_off','processing','blocked');
CREATE INDEX account_closure_owner_history ON account_closure_requests (requested_by_user_id,requested_at DESC,id DESC);

CREATE TABLE account_lifecycle_events (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    closure_request_id uuid NOT NULL REFERENCES account_closure_requests (id),
    action text NOT NULL CHECK (action IN ('closure_requested','closure_blocked','closure_canceled','account_closed')),
    from_state text NOT NULL CHECK (from_state IN ('active','closing')),
    to_state text NOT NULL CHECK (to_state IN ('active','closing','closed')),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user','workload')),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 255),
    reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 3 AND 300 AND reason=btrim(reason)),
    blocker_code text CHECK (blocker_code IN ('billing_active','checkout_active')),
    occurred_at timestamptz NOT NULL,
    CONSTRAINT account_lifecycle_event_shape CHECK (
        (action='closure_requested' AND from_state='active' AND to_state='closing' AND actor_kind='user' AND blocker_code IS NULL)
        OR (action='closure_blocked' AND from_state='closing' AND to_state='closing' AND actor_kind='workload' AND blocker_code IS NOT NULL)
        OR (action='closure_canceled' AND from_state='closing' AND to_state='active' AND actor_kind='user' AND blocker_code IS NULL)
        OR (action='account_closed' AND from_state='closing' AND to_state='closed' AND actor_kind='workload' AND blocker_code IS NULL)
    )
);
CREATE INDEX account_lifecycle_events_account_time ON account_lifecycle_events (account_id,occurred_at DESC,id DESC);

CREATE FUNCTION reject_account_lifecycle_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Account lifecycle events are immutable';
END;
$$;

CREATE TRIGGER account_lifecycle_events_immutable
BEFORE UPDATE OR DELETE ON account_lifecycle_events
FOR EACH ROW EXECUTE FUNCTION reject_account_lifecycle_event_mutation();

COMMIT;
