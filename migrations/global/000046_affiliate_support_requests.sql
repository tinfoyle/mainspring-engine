BEGIN;

CREATE TABLE affiliate_support_requests (
    request_id uuid PRIMARY KEY,
    affiliate_id uuid NOT NULL REFERENCES affiliate_enrollments (affiliate_id),
    user_id uuid NOT NULL REFERENCES users (id),
    kind text NOT NULL CHECK (kind IN ('enrollment_appeal','commission_review')),
    commission_entry_id uuid REFERENCES affiliate_commission_entries (entry_id),
    state text NOT NULL CHECK (state IN ('submitted','in_review','resolved','declined','canceled')),
    outcome text CHECK (outcome IN ('approved','denied')),
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT affiliate_support_request_subject_shape CHECK (
        (kind='enrollment_appeal' AND commission_entry_id IS NULL) OR
        (kind='commission_review' AND commission_entry_id IS NOT NULL)
    ),
    CONSTRAINT affiliate_support_request_resolution_shape CHECK (
        (state IN ('submitted','in_review','canceled') AND outcome IS NULL) OR
        (state='resolved' AND outcome='approved') OR
        (state='declined' AND outcome='denied')
    ),
    CONSTRAINT affiliate_support_request_time_order CHECK (updated_at >= created_at)
);
CREATE UNIQUE INDEX affiliate_support_requests_open_subject
    ON affiliate_support_requests (affiliate_id,kind,commission_entry_id) NULLS NOT DISTINCT
    WHERE state IN ('submitted','in_review');
CREATE INDEX affiliate_support_requests_user_history
    ON affiliate_support_requests (user_id,created_at DESC,request_id DESC);

CREATE TABLE affiliate_support_request_events (
    event_id uuid PRIMARY KEY,
    request_id uuid NOT NULL REFERENCES affiliate_support_requests (request_id),
    version bigint NOT NULL CHECK (version > 0),
    action text NOT NULL CHECK (action IN ('submitted','canceled','inspected','review_started','resolved')),
    state text NOT NULL CHECK (state IN ('submitted','in_review','resolved','declined','canceled')),
    outcome text CHECK (outcome IN ('approved','denied')),
    actor text NOT NULL CHECK (length(actor) BETWEEN 3 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (length(reason) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    occurred_at timestamptz NOT NULL,
    CONSTRAINT affiliate_support_event_resolution_shape CHECK (
        (state IN ('submitted','in_review','canceled') AND outcome IS NULL) OR
        (state='resolved' AND outcome='approved') OR
        (state='declined' AND outcome='denied')
    )
);
CREATE UNIQUE INDEX affiliate_support_events_transition_version
    ON affiliate_support_request_events (request_id,version)
    WHERE action <> 'inspected';
CREATE INDEX affiliate_support_events_history
    ON affiliate_support_request_events (request_id,occurred_at,event_id);

CREATE TRIGGER affiliate_support_request_events_immutable
BEFORE UPDATE OR DELETE ON affiliate_support_request_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_affiliate_ledger_mutation();

CREATE FUNCTION spyglass_inspect_affiliate_support_request(
    p_event_id uuid,
    p_request_id uuid,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS TABLE(
    request_id uuid,affiliate_id uuid,user_id uuid,kind text,commission_entry_id text,
    state text,outcome text,version bigint,created_at timestamptz,updated_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    current_row public.affiliate_support_requests%ROWTYPE;
BEGIN
    IF p_event_id IS NULL OR p_request_id IS NULL OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION 'invalid Affiliate support inspection' USING ERRCODE='22023';
    END IF;
    SELECT * INTO current_row FROM public.affiliate_support_requests r WHERE r.request_id=p_request_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Affiliate support request not found' USING ERRCODE='P0002';
    END IF;
    INSERT INTO public.affiliate_support_request_events
        (event_id,request_id,version,action,state,outcome,actor,reason,environment,occurred_at)
    VALUES (p_event_id,current_row.request_id,current_row.version,'inspected',current_row.state,current_row.outcome,
            btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());
    RETURN QUERY SELECT current_row.request_id,current_row.affiliate_id,current_row.user_id,current_row.kind,
        current_row.commission_entry_id::text,current_row.state,current_row.outcome,current_row.version,
        current_row.created_at,current_row.updated_at;
END;
$$;

CREATE FUNCTION spyglass_transition_affiliate_support_request(
    p_event_id uuid,
    p_request_id uuid,
    p_expected_version bigint,
    p_action text,
    p_state text,
    p_outcome text,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS TABLE(
    request_id uuid,affiliate_id uuid,user_id uuid,kind text,commission_entry_id text,
    state text,outcome text,version bigint,created_at timestamptz,updated_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    current_row public.affiliate_support_requests%ROWTYPE;
BEGIN
    IF p_event_id IS NULL OR p_request_id IS NULL OR p_expected_version < 1 OR
       p_action NOT IN ('review_started','resolved') OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR
       (p_action='review_started' AND (p_state<>'in_review' OR p_outcome IS NOT NULL)) OR
       (p_action='resolved' AND NOT ((p_state='resolved' AND p_outcome='approved') OR (p_state='declined' AND p_outcome='denied'))) THEN
        RAISE EXCEPTION 'invalid Affiliate support transition' USING ERRCODE='22023';
    END IF;
    SELECT * INTO current_row FROM public.affiliate_support_requests r WHERE r.request_id=p_request_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Affiliate support request not found' USING ERRCODE='P0002';
    END IF;
    IF current_row.version <> p_expected_version OR
       (p_action='review_started' AND current_row.state<>'submitted') OR
       (p_action='resolved' AND current_row.state<>'in_review') THEN
        RAISE EXCEPTION 'Affiliate support request state changed' USING ERRCODE='P0001';
    END IF;
    UPDATE public.affiliate_support_requests r
       SET state=p_state,outcome=p_outcome,version=r.version+1,updated_at=statement_timestamp()
     WHERE r.request_id=p_request_id
     RETURNING r.* INTO current_row;
    INSERT INTO public.affiliate_support_request_events
        (event_id,request_id,version,action,state,outcome,actor,reason,environment,occurred_at)
    VALUES (p_event_id,current_row.request_id,current_row.version,p_action,current_row.state,current_row.outcome,
            btrim(p_actor),btrim(p_reason),p_environment,current_row.updated_at);
    RETURN QUERY SELECT current_row.request_id,current_row.affiliate_id,current_row.user_id,current_row.kind,
        current_row.commission_entry_id::text,current_row.state,current_row.outcome,current_row.version,
        current_row.created_at,current_row.updated_at;
END;
$$;

REVOKE ALL ON TABLE affiliate_support_requests,affiliate_support_request_events FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_inspect_affiliate_support_request(uuid,uuid,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_transition_affiliate_support_request(uuid,uuid,bigint,text,text,text,text,text,text) FROM PUBLIC;

COMMIT;
