BEGIN;

-- Route retries receive a fresh server timestamp. Bind replay to the stable
-- command identity and actors, not to that incidental timestamp.
CREATE FUNCTION public.spyglass_request_runner_action_resolution_v2(
    p_account_id uuid,p_operation_id uuid,p_resolution_id uuid,p_requested_outcome text,p_reason_sha256 bytea,
    p_requested_by_user_id uuid,p_requested_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE existing record;
BEGIN
    IF p_account_id IS NULL OR p_operation_id IS NULL OR p_resolution_id IS NULL OR p_requested_outcome NOT IN ('succeeded','failed') OR
       p_reason_sha256 IS NULL OR octet_length(p_reason_sha256)<>32 OR p_requested_by_user_id IS NULL OR p_requested_at IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner action resolution request';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT r.* INTO existing FROM spyglass.runner_action_manual_resolutions r
    WHERE r.account_id=p_account_id AND r.resolution_id=p_resolution_id;
    IF FOUND THEN
        IF existing.operation_id=p_operation_id AND existing.requested_outcome=p_requested_outcome AND
           existing.reason_sha256=p_reason_sha256 AND existing.requested_by_user_id=p_requested_by_user_id THEN RETURN false; END IF;
        RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action resolution request conflicts';
    END IF;
    RETURN public.spyglass_request_runner_action_resolution(p_account_id,p_operation_id,p_resolution_id,p_requested_outcome,
        p_reason_sha256,p_requested_by_user_id,p_requested_at);
END;
$$;

CREATE FUNCTION public.spyglass_confirm_runner_action_resolution_v2(
    p_account_id uuid,p_operation_id uuid,p_resolution_id uuid,p_confirmed_by_user_id uuid,p_confirmed_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE existing record;
BEGIN
    IF p_account_id IS NULL OR p_operation_id IS NULL OR p_resolution_id IS NULL OR p_confirmed_by_user_id IS NULL OR p_confirmed_at IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner action resolution confirmation';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT r.* INTO existing FROM spyglass.runner_action_manual_resolutions r
    WHERE r.account_id=p_account_id AND r.resolution_id=p_resolution_id;
    IF FOUND AND existing.state='applied' THEN
        IF existing.operation_id=p_operation_id AND existing.confirmed_by_user_id=p_confirmed_by_user_id THEN RETURN false; END IF;
        RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action resolution confirmation conflicts';
    END IF;
    RETURN public.spyglass_confirm_runner_action_resolution(p_account_id,p_operation_id,p_resolution_id,p_confirmed_by_user_id,p_confirmed_at);
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_request_runner_action_resolution_v2(uuid,uuid,uuid,text,bytea,uuid,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_confirm_runner_action_resolution_v2(uuid,uuid,uuid,uuid,timestamptz) FROM PUBLIC;

COMMIT;
