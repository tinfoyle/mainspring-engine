BEGIN;

ALTER TABLE public.account_closure_requests
    ADD CONSTRAINT account_closure_requests_account_id_id_unique UNIQUE (account_id,id);

CREATE TABLE public.account_erasure_requests (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES public.accounts (id),
    closure_request_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('prepared','approved','canceled')),
    cell_id text NOT NULL REFERENCES public.cells (id),
    placement_generation bigint NOT NULL CHECK (placement_generation > 0),
    account_version bigint NOT NULL CHECK (account_version > 0),
    policy_version bigint NOT NULL CHECK (policy_version > 0),
    export_disposition text NOT NULL CHECK (export_disposition IN ('artifact','not_applicable')),
    export_reference text,
    export_sha256 bytea,
    export_expires_at timestamptz,
    export_reason text,
    backup_expires_at timestamptz NOT NULL,
    cell_namespace_state text NOT NULL CHECK (cell_namespace_state IN ('frozen','disabled')),
    cell_attested_at timestamptz NOT NULL,
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    requested_by text NOT NULL CHECK (char_length(requested_by) BETWEEN 1 AND 200 AND requested_by !~ E'[\r\n]'),
    request_reason text NOT NULL CHECK (char_length(request_reason) BETWEEN 8 AND 500 AND request_reason !~ E'[\r\n]'),
    requested_at timestamptz NOT NULL,
    approved_by text,
    approve_reason text,
    approved_at timestamptz,
    canceled_by text,
    cancel_reason text,
    canceled_at timestamptz,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (account_id,id),
    CONSTRAINT account_erasure_closure_scope FOREIGN KEY (account_id,closure_request_id) REFERENCES public.account_closure_requests (account_id,id),
    CONSTRAINT account_erasure_export_shape CHECK (
        (export_disposition='artifact' AND export_reference IS NOT NULL AND char_length(export_reference) BETWEEN 1 AND 2048 AND export_reference !~ E'[\r\n]' AND octet_length(export_sha256)=32 AND export_expires_at IS NOT NULL AND export_reason IS NULL)
        OR
        (export_disposition='not_applicable' AND export_reference IS NULL AND export_sha256 IS NULL AND export_expires_at IS NULL AND export_reason IS NOT NULL AND char_length(export_reason) BETWEEN 8 AND 500 AND export_reason !~ E'[\r\n]')
    ),
    CONSTRAINT account_erasure_state_shape CHECK (
        (state='prepared' AND approved_by IS NULL AND approve_reason IS NULL AND approved_at IS NULL AND canceled_by IS NULL AND cancel_reason IS NULL AND canceled_at IS NULL)
        OR
        (state='approved' AND approved_by IS NOT NULL AND approve_reason IS NOT NULL AND approved_at IS NOT NULL AND canceled_by IS NULL AND cancel_reason IS NULL AND canceled_at IS NULL)
        OR
        (state='canceled' AND canceled_by IS NOT NULL AND cancel_reason IS NOT NULL AND canceled_at IS NOT NULL)
    )
);
CREATE UNIQUE INDEX account_erasure_one_current
    ON public.account_erasure_requests (account_id) WHERE state IN ('prepared','approved');
CREATE INDEX account_erasure_requests_state_time
    ON public.account_erasure_requests (state,requested_at,id);

CREATE TABLE public.account_erasure_operator_events (
    id uuid PRIMARY KEY,
    request_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES public.accounts (id),
    action text NOT NULL CHECK (action IN ('prepared','inspected','approved','canceled')),
    actor text NOT NULL CHECK (char_length(actor) BETWEEN 1 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    request_version bigint NOT NULL CHECK (request_version > 0),
    created_at timestamptz NOT NULL,
    CONSTRAINT account_erasure_operator_event_scope FOREIGN KEY (account_id,request_id) REFERENCES public.account_erasure_requests (account_id,id) ON DELETE CASCADE
);
CREATE INDEX account_erasure_operator_events_request_time
    ON public.account_erasure_operator_events (request_id,created_at,id);

CREATE FUNCTION public.reject_account_erasure_operator_event_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Account erasure operator events are immutable';
END;
$$;

CREATE TRIGGER account_erasure_operator_events_immutable
BEFORE UPDATE OR DELETE ON public.account_erasure_operator_events
FOR EACH ROW EXECUTE FUNCTION public.reject_account_erasure_operator_event_change();

CREATE FUNCTION public.spyglass_assert_account_erasure_eligible(p_account_id uuid)
RETURNS TABLE (
    closure_request_id uuid,
    cell_id text,
    placement_generation bigint,
    account_version bigint
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    target record;
BEGIN
    SELECT cr.id,a.cell_id,a.placement_generation,a.version,a.state AS account_state,
           cr.delete_after,d.cell_id AS directory_cell_id,d.placement_generation AS directory_generation,
           d.state AS directory_state
    INTO target
    FROM accounts a
    JOIN account_directory d ON d.account_id=a.id
    JOIN account_closure_requests cr ON cr.account_id=a.id AND cr.state='closed'
    WHERE a.id=p_account_id
    ORDER BY cr.closed_at DESC
    LIMIT 1
    FOR UPDATE OF a,d,cr;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = 'P0002', MESSAGE = 'closed Account erasure target not found';
    END IF;
    IF target.account_state <> 'closed' OR target.delete_after > statement_timestamp() OR
       target.cell_id <> target.directory_cell_id OR target.placement_generation <> target.directory_generation OR
       target.directory_state NOT IN ('frozen','disabled') OR
       EXISTS (SELECT 1 FROM subscriptions s WHERE s.account_id=p_account_id AND s.state NOT IN ('canceled','incomplete_expired')) OR
       EXISTS (SELECT 1 FROM billing_checkout_attempts b WHERE b.account_id=p_account_id AND b.state='active' AND b.expires_at>statement_timestamp()) OR
       EXISTS (SELECT 1 FROM entitlement_usage_reservations r WHERE r.account_id=p_account_id AND r.state='active') OR
       EXISTS (SELECT 1 FROM entitlement_usage_counters c WHERE c.account_id=p_account_id AND c.current_value<>0) THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'Account is not eligible for erasure preparation';
    END IF;
    closure_request_id := target.id;
    cell_id := target.cell_id;
    placement_generation := target.placement_generation;
    account_version := target.version;
    RETURN NEXT;
END;
$$;

CREATE FUNCTION public.spyglass_prepare_account_erasure(
    p_request_id uuid,
    p_event_id uuid,
    p_account_id uuid,
    p_policy_version bigint,
    p_export_disposition text,
    p_export_reference text,
    p_export_sha256 bytea,
    p_export_expires_at timestamptz,
    p_export_reason text,
    p_backup_expires_at timestamptz,
    p_cell_placement_generation bigint,
    p_cell_namespace_state text,
    p_unfinished_release_count bigint,
    p_cell_attested_at timestamptz,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS SETOF public.account_erasure_requests
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    eligible record;
    result public.account_erasure_requests%ROWTYPE;
BEGIN
    IF p_request_id IS NULL OR p_event_id IS NULL OR p_account_id IS NULL OR p_policy_version IS NULL OR p_policy_version<=0 OR
       p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR
       p_backup_expires_at IS NULL OR p_backup_expires_at<=statement_timestamp() OR
       p_cell_placement_generation IS NULL OR p_cell_placement_generation<=0 OR
       p_cell_namespace_state NOT IN ('frozen','disabled') OR p_unfinished_release_count<>0 OR
       p_cell_attested_at IS NULL OR p_cell_attested_at<statement_timestamp()-interval '5 minutes' OR p_cell_attested_at>statement_timestamp()+interval '1 minute' OR
       NOT ((p_export_disposition='artifact' AND p_export_reference IS NOT NULL AND char_length(p_export_reference) BETWEEN 1 AND 2048 AND p_export_reference !~ E'[\r\n]' AND octet_length(p_export_sha256)=32 AND p_export_expires_at>statement_timestamp() AND p_export_reason IS NULL)
            OR (p_export_disposition='not_applicable' AND p_export_reference IS NULL AND p_export_sha256 IS NULL AND p_export_expires_at IS NULL AND p_export_reason IS NOT NULL AND char_length(btrim(p_export_reason)) BETWEEN 8 AND 500 AND p_export_reason !~ E'[\r\n]')) THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid Account erasure preparation';
    END IF;

    SELECT * INTO eligible FROM public.spyglass_assert_account_erasure_eligible(p_account_id);
    IF eligible.placement_generation<>p_cell_placement_generation THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'cell attestation does not match Account placement';
    END IF;

    INSERT INTO public.account_erasure_requests
        (id,account_id,closure_request_id,state,cell_id,placement_generation,account_version,policy_version,
         export_disposition,export_reference,export_sha256,export_expires_at,export_reason,backup_expires_at,
         cell_namespace_state,cell_attested_at,environment,requested_by,request_reason,requested_at,version)
    VALUES
        (p_request_id,p_account_id,eligible.closure_request_id,'prepared',eligible.cell_id,eligible.placement_generation,
         eligible.account_version,p_policy_version,p_export_disposition,p_export_reference,p_export_sha256,p_export_expires_at,
         CASE WHEN p_export_reason IS NULL THEN NULL ELSE btrim(p_export_reason) END,p_backup_expires_at,p_cell_namespace_state,
         p_cell_attested_at,p_environment,btrim(p_actor),btrim(p_reason),statement_timestamp(),1)
    RETURNING * INTO result;
    INSERT INTO public.account_erasure_operator_events
        (id,request_id,account_id,action,actor,reason,environment,request_version,created_at)
    VALUES (p_event_id,result.id,result.account_id,'prepared',btrim(p_actor),btrim(p_reason),p_environment,result.version,statement_timestamp());
    RETURN NEXT result;
END;
$$;

CREATE FUNCTION public.spyglass_inspect_account_erasure(
    p_request_id uuid,
    p_event_id uuid,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS SETOF public.account_erasure_requests
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    result public.account_erasure_requests%ROWTYPE;
BEGIN
    IF p_request_id IS NULL OR p_event_id IS NULL OR
       p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid Account erasure inspection';
    END IF;
    SELECT * INTO result FROM public.account_erasure_requests WHERE id=p_request_id FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = 'P0002', MESSAGE = 'Account erasure request not found';
    END IF;
    IF result.environment<>p_environment THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'Account erasure environment mismatch';
    END IF;
    INSERT INTO public.account_erasure_operator_events
        (id,request_id,account_id,action,actor,reason,environment,request_version,created_at)
    VALUES (p_event_id,result.id,result.account_id,'inspected',btrim(p_actor),btrim(p_reason),p_environment,result.version,statement_timestamp());
    RETURN NEXT result;
END;
$$;

CREATE FUNCTION public.spyglass_approve_account_erasure(
    p_request_id uuid,
    p_event_id uuid,
    p_expected_version bigint,
    p_cell_placement_generation bigint,
    p_cell_namespace_state text,
    p_unfinished_release_count bigint,
    p_cell_attested_at timestamptz,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS SETOF public.account_erasure_requests
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    current_request public.account_erasure_requests%ROWTYPE;
    eligible record;
BEGIN
    IF p_request_id IS NULL OR p_event_id IS NULL OR p_expected_version IS NULL OR p_expected_version<=0 OR
       p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR
       p_cell_placement_generation IS NULL OR p_cell_placement_generation<=0 OR
       p_cell_namespace_state NOT IN ('frozen','disabled') OR p_unfinished_release_count<>0 OR
       p_cell_attested_at IS NULL OR p_cell_attested_at<statement_timestamp()-interval '5 minutes' OR p_cell_attested_at>statement_timestamp()+interval '1 minute' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid Account erasure approval';
    END IF;
    SELECT * INTO current_request FROM public.account_erasure_requests WHERE id=p_request_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = 'P0002', MESSAGE = 'Account erasure request not found';
    END IF;
    IF current_request.state<>'prepared' OR current_request.version<>p_expected_version OR current_request.environment<>p_environment OR current_request.requested_by=btrim(p_actor) OR
       current_request.backup_expires_at<=statement_timestamp() OR
       (current_request.export_disposition='artifact' AND current_request.export_expires_at<=statement_timestamp()) THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'Account erasure request cannot be approved';
    END IF;
    SELECT * INTO eligible FROM public.spyglass_assert_account_erasure_eligible(current_request.account_id);
    IF eligible.closure_request_id<>current_request.closure_request_id OR eligible.cell_id<>current_request.cell_id OR
       eligible.placement_generation<>current_request.placement_generation OR eligible.account_version<>current_request.account_version OR
       p_cell_placement_generation<>current_request.placement_generation THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'Account erasure eligibility changed after preparation';
    END IF;
    UPDATE public.account_erasure_requests SET
        state='approved',approved_by=btrim(p_actor),approve_reason=btrim(p_reason),approved_at=statement_timestamp(),
        cell_namespace_state=p_cell_namespace_state,cell_attested_at=p_cell_attested_at,version=version+1
    WHERE id=p_request_id
    RETURNING * INTO current_request;
    INSERT INTO public.account_erasure_operator_events
        (id,request_id,account_id,action,actor,reason,environment,request_version,created_at)
    VALUES (p_event_id,current_request.id,current_request.account_id,'approved',btrim(p_actor),btrim(p_reason),p_environment,current_request.version,statement_timestamp());
    RETURN NEXT current_request;
END;
$$;

CREATE FUNCTION public.spyglass_cancel_account_erasure(
    p_request_id uuid,
    p_event_id uuid,
    p_expected_version bigint,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS SETOF public.account_erasure_requests
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    result public.account_erasure_requests%ROWTYPE;
BEGIN
    IF p_request_id IS NULL OR p_event_id IS NULL OR p_expected_version IS NULL OR p_expected_version<=0 OR
       p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid Account erasure cancellation';
    END IF;
    UPDATE public.account_erasure_requests SET
        state='canceled',canceled_by=btrim(p_actor),cancel_reason=btrim(p_reason),canceled_at=statement_timestamp(),version=version+1
    WHERE id=p_request_id AND state IN ('prepared','approved') AND version=p_expected_version AND environment=p_environment
    RETURNING * INTO result;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'Account erasure request cannot be canceled';
    END IF;
    INSERT INTO public.account_erasure_operator_events
        (id,request_id,account_id,action,actor,reason,environment,request_version,created_at)
    VALUES (p_event_id,result.id,result.account_id,'canceled',btrim(p_actor),btrim(p_reason),p_environment,result.version,statement_timestamp());
    RETURN NEXT result;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_assert_account_erasure_eligible(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_prepare_account_erasure(uuid,uuid,uuid,bigint,text,text,bytea,timestamptz,text,timestamptz,bigint,text,bigint,timestamptz,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_inspect_account_erasure(uuid,uuid,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_approve_account_erasure(uuid,uuid,bigint,bigint,text,bigint,timestamptz,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_cancel_account_erasure(uuid,uuid,bigint,text,text,text) FROM PUBLIC;

COMMIT;
