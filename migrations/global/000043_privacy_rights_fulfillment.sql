BEGIN;

ALTER TABLE privacy_rights_requests
    ADD COLUMN version bigint;

UPDATE privacy_rights_requests request
SET version = (
    SELECT count(*)::bigint
    FROM privacy_rights_request_events event
    WHERE event.request_id = request.request_id
);

ALTER TABLE privacy_rights_requests
    ALTER COLUMN version SET NOT NULL,
    ALTER COLUMN version SET DEFAULT 1,
    ADD CONSTRAINT privacy_rights_request_version_positive CHECK (version > 0);

ALTER TABLE privacy_rights_request_events
    ADD COLUMN action text,
    ADD COLUMN version bigint,
    ADD COLUMN actor text,
    ADD COLUMN reason text,
    ADD COLUMN environment text,
    ADD COLUMN evidence_id uuid,
    ADD COLUMN evidence_sha256 bytea;

ALTER TABLE privacy_rights_request_events DISABLE TRIGGER privacy_rights_request_events_immutable;

WITH numbered AS (
    SELECT event_id,
           row_number() OVER (PARTITION BY request_id ORDER BY occurred_at,event_id)::bigint AS event_version
    FROM privacy_rights_request_events
)
UPDATE privacy_rights_request_events event
SET action = CASE event.state WHEN 'submitted' THEN 'submitted' WHEN 'canceled' THEN 'canceled' ELSE 'resolved' END,
    version = numbered.event_version
FROM numbered
WHERE numbered.event_id = event.event_id;

ALTER TABLE privacy_rights_request_events ENABLE TRIGGER privacy_rights_request_events_immutable;

ALTER TABLE privacy_rights_request_events
    ALTER COLUMN action SET NOT NULL,
    ALTER COLUMN version SET NOT NULL,
    ADD CONSTRAINT privacy_rights_event_action CHECK (action IN ('submitted','canceled','inspected','review_started','resolved')),
    ADD CONSTRAINT privacy_rights_event_version_positive CHECK (version > 0),
    ADD CONSTRAINT privacy_rights_event_operator_shape CHECK (
        (action IN ('submitted','canceled') AND actor IS NULL AND reason IS NULL AND environment IS NULL AND evidence_id IS NULL AND evidence_sha256 IS NULL)
        OR
        (action IN ('inspected','review_started') AND char_length(btrim(actor)) BETWEEN 3 AND 200 AND actor !~ E'[\r\n]'
            AND char_length(btrim(reason)) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'
            AND environment ~ '^[a-z][a-z0-9-]{0,99}$' AND evidence_id IS NULL AND evidence_sha256 IS NULL)
        OR
        (action = 'resolved' AND char_length(btrim(actor)) BETWEEN 3 AND 200 AND actor !~ E'[\r\n]'
            AND char_length(btrim(reason)) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'
            AND environment ~ '^[a-z][a-z0-9-]{0,99}$' AND evidence_id IS NOT NULL AND octet_length(evidence_sha256) = 32)
    ),
    ADD CONSTRAINT privacy_rights_event_state_shape CHECK (
        (action = 'submitted' AND state = 'submitted') OR
        (action = 'canceled' AND state = 'canceled') OR
        (action = 'inspected') OR
        (action = 'review_started' AND state = 'in_review') OR
        (action = 'resolved' AND state IN ('completed','partially_completed','declined'))
    );

CREATE INDEX privacy_rights_requests_due
    ON privacy_rights_requests (response_due_at,request_id)
    WHERE state IN ('submitted','in_review');

CREATE FUNCTION public.spyglass_bump_legacy_privacy_rights_version() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NEW.state IS DISTINCT FROM OLD.state AND NEW.version = OLD.version THEN
        NEW.version := OLD.version + 1;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER privacy_rights_requests_legacy_version
BEFORE UPDATE ON privacy_rights_requests
FOR EACH ROW EXECUTE FUNCTION public.spyglass_bump_legacy_privacy_rights_version();

CREATE FUNCTION public.spyglass_complete_legacy_privacy_rights_event() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NEW.action IS NULL OR NEW.version IS NULL THEN
        IF NEW.action IS NOT NULL OR NEW.version IS NOT NULL OR NEW.state NOT IN ('submitted','canceled') OR
           NEW.actor IS NOT NULL OR NEW.reason IS NOT NULL OR NEW.environment IS NOT NULL OR
           NEW.evidence_id IS NOT NULL OR NEW.evidence_sha256 IS NOT NULL THEN
            RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid legacy privacy rights event';
        END IF;
        NEW.action := NEW.state;
        SELECT request.version INTO NEW.version
        FROM public.privacy_rights_requests request
        WHERE request.request_id=NEW.request_id;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER privacy_rights_request_events_legacy_shape
BEFORE INSERT ON privacy_rights_request_events
FOR EACH ROW EXECUTE FUNCTION public.spyglass_complete_legacy_privacy_rights_event();

CREATE FUNCTION public.spyglass_inspect_privacy_rights_request(
    p_event_id uuid, p_request_id uuid, p_actor text, p_reason text, p_environment text
) RETURNS TABLE (
    request_id uuid, user_id uuid, version bigint, kind text, scope text, state text,
    verified_at timestamptz, requested_at timestamptz, response_due_at timestamptz, updated_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE target public.privacy_rights_requests%ROWTYPE;
BEGIN
    IF p_event_id IS NULL OR p_request_id IS NULL OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid privacy rights inspection';
    END IF;
    SELECT * INTO target FROM public.privacy_rights_requests request WHERE request.request_id=p_request_id FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='privacy rights request not found'; END IF;
    INSERT INTO public.privacy_rights_request_events
        (event_id,request_id,action,state,version,actor,reason,environment,occurred_at)
    VALUES (p_event_id,target.request_id,'inspected',target.state,target.version,btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());
    RETURN QUERY SELECT target.request_id,target.user_id,target.version,target.kind,target.scope,target.state,
        target.verified_at,target.requested_at,target.response_due_at,target.updated_at;
END;
$$;

CREATE FUNCTION public.spyglass_transition_privacy_rights_request(
    p_event_id uuid, p_request_id uuid, p_expected_version bigint, p_action text, p_target_state text,
    p_evidence_id uuid, p_evidence_sha256 bytea, p_actor text, p_reason text, p_environment text
) RETURNS TABLE (
    request_id uuid, user_id uuid, version bigint, kind text, scope text, state text,
    verified_at timestamptz, requested_at timestamptz, response_due_at timestamptz, updated_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE target public.privacy_rights_requests%ROWTYPE; now_at timestamptz := statement_timestamp();
BEGIN
    IF p_event_id IS NULL OR p_request_id IS NULL OR p_expected_version < 1 OR
       p_action NOT IN ('review_started','resolved') OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR
       (p_action='review_started' AND (p_target_state IS DISTINCT FROM 'in_review' OR p_evidence_id IS NOT NULL OR p_evidence_sha256 IS NOT NULL)) OR
       (p_action='resolved' AND (p_target_state IS NULL OR p_target_state NOT IN ('completed','partially_completed','declined') OR p_evidence_id IS NULL OR octet_length(p_evidence_sha256) IS DISTINCT FROM 32)) THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid privacy rights transition';
    END IF;
    SELECT * INTO target FROM public.privacy_rights_requests request WHERE request.request_id=p_request_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='privacy rights request not found'; END IF;
    IF target.version<>p_expected_version OR
       (p_action='review_started' AND target.state<>'submitted') OR
       (p_action='resolved' AND target.state<>'in_review') THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='privacy rights request state conflict';
    END IF;
    target.version := target.version + 1;
    target.state := p_target_state;
    target.updated_at := now_at;
    UPDATE public.privacy_rights_requests request
    SET version=target.version,state=target.state,updated_at=target.updated_at
    WHERE request.request_id=target.request_id;
    INSERT INTO public.privacy_rights_request_events
        (event_id,request_id,action,state,version,actor,reason,environment,evidence_id,evidence_sha256,occurred_at)
    VALUES (p_event_id,target.request_id,p_action,target.state,target.version,btrim(p_actor),btrim(p_reason),p_environment,
            p_evidence_id,p_evidence_sha256,now_at);
    RETURN QUERY SELECT target.request_id,target.user_id,target.version,target.kind,target.scope,target.state,
        target.verified_at,target.requested_at,target.response_due_at,target.updated_at;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_inspect_privacy_rights_request(uuid,uuid,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_transition_privacy_rights_request(uuid,uuid,bigint,text,text,uuid,bytea,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_bump_legacy_privacy_rights_version() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_complete_legacy_privacy_rights_event() FROM PUBLIC;

COMMIT;
