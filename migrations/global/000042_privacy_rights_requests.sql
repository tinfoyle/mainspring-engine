BEGIN;

CREATE TABLE privacy_rights_requests (
    request_id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id),
    kind text NOT NULL CHECK (kind IN ('access','correction','erasure','restriction','objection','portability')),
    scope text NOT NULL CHECK (scope IN ('identity','account','affiliate','analytics')),
    state text NOT NULL CHECK (state IN ('submitted','in_review','completed','partially_completed','declined','canceled')),
    verified_at timestamptz NOT NULL,
    requested_at timestamptz NOT NULL,
    response_due_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT privacy_rights_time_order CHECK (
        verified_at <= requested_at AND response_due_at > requested_at AND updated_at >= requested_at
    )
);
CREATE INDEX privacy_rights_requests_user ON privacy_rights_requests (user_id,requested_at DESC,request_id DESC);
CREATE UNIQUE INDEX privacy_rights_requests_open
    ON privacy_rights_requests (user_id,kind,scope)
    WHERE state IN ('submitted','in_review');

CREATE TABLE privacy_rights_request_events (
    event_id uuid PRIMARY KEY,
    request_id uuid NOT NULL REFERENCES privacy_rights_requests (request_id) ON DELETE CASCADE,
    state text NOT NULL CHECK (state IN ('submitted','in_review','completed','partially_completed','declined','canceled')),
    occurred_at timestamptz NOT NULL
);
CREATE INDEX privacy_rights_request_events_history ON privacy_rights_request_events (request_id,occurred_at,event_id);

CREATE FUNCTION spyglass_reject_privacy_rights_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'privacy rights request events are immutable';
END;
$$;
CREATE TRIGGER privacy_rights_request_events_immutable
BEFORE UPDATE OR DELETE ON privacy_rights_request_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_privacy_rights_event_mutation();

REVOKE ALL ON TABLE privacy_rights_requests,privacy_rights_request_events FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_reject_privacy_rights_event_mutation() FROM PUBLIC;

COMMIT;
