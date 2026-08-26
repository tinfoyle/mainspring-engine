BEGIN;

CREATE TABLE privacy_rights_queue_access_events (
    access_id uuid PRIMARY KEY,
    due_before timestamptz NOT NULL,
    result_limit integer NOT NULL CHECK (result_limit BETWEEN 1 AND 100),
    result_count integer NOT NULL CHECK (result_count BETWEEN 0 AND result_limit),
    actor text NOT NULL CHECK (char_length(btrim(actor)) BETWEEN 3 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    occurred_at timestamptz NOT NULL
);

CREATE FUNCTION public.spyglass_reject_privacy_rights_queue_access_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    RAISE EXCEPTION 'privacy rights queue access events are immutable';
END;
$$;

CREATE TRIGGER privacy_rights_queue_access_events_immutable
BEFORE UPDATE OR DELETE ON privacy_rights_queue_access_events
FOR EACH ROW EXECUTE FUNCTION public.spyglass_reject_privacy_rights_queue_access_mutation();

CREATE FUNCTION public.spyglass_list_open_privacy_rights_requests(
    p_access_id uuid, p_due_before timestamptz, p_limit integer,
    p_actor text, p_reason text, p_environment text
) RETURNS TABLE (
    request_id uuid, version bigint, kind text, scope text, state text,
    requested_at timestamptz, response_due_at timestamptz, updated_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE matched integer;
BEGIN
    IF p_access_id IS NULL OR p_due_before IS NULL OR p_limit NOT BETWEEN 1 AND 100 OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid privacy rights queue access';
    END IF;

    SELECT count(*)::integer INTO matched
    FROM (
        SELECT request.request_id
        FROM public.privacy_rights_requests request
        WHERE request.state IN ('submitted','in_review') AND request.response_due_at <= p_due_before
        ORDER BY request.response_due_at,request.request_id
        LIMIT p_limit
    ) due;

    INSERT INTO public.privacy_rights_queue_access_events
        (access_id,due_before,result_limit,result_count,actor,reason,environment,occurred_at)
    VALUES (p_access_id,p_due_before,p_limit,matched,btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());

    RETURN QUERY
    SELECT request.request_id,request.version,request.kind,request.scope,request.state,
           request.requested_at,request.response_due_at,request.updated_at
    FROM public.privacy_rights_requests request
    WHERE request.state IN ('submitted','in_review') AND request.response_due_at <= p_due_before
    ORDER BY request.response_due_at,request.request_id
    LIMIT p_limit;
END;
$$;

REVOKE ALL ON TABLE privacy_rights_queue_access_events FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_reject_privacy_rights_queue_access_mutation() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_list_open_privacy_rights_requests(uuid,timestamptz,integer,text,text,text) FROM PUBLIC;

COMMIT;
