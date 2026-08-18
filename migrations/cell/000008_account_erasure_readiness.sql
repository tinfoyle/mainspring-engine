BEGIN;

CREATE FUNCTION public.spyglass_attest_account_erasure_readiness(
    p_account_id uuid,
    p_placement_generation bigint
) RETURNS TABLE (
    placement_generation bigint,
    namespace_state text,
    unfinished_release_count bigint,
    observed_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    target record;
BEGIN
    IF p_account_id IS NULL OR p_placement_generation IS NULL OR p_placement_generation <= 0 THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid Account erasure readiness target';
    END IF;

    SELECT n.placement_generation,n.state
    INTO target
    FROM spyglass.account_namespaces n
    WHERE n.account_id=p_account_id
    FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = 'P0002', MESSAGE = 'Account namespace not found';
    END IF;
    IF target.placement_generation <> p_placement_generation OR target.state NOT IN ('frozen','disabled') THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'Account namespace is not ready for erasure preparation';
    END IF;

    placement_generation := target.placement_generation;
    namespace_state := target.state;
    SELECT count(*) INTO unfinished_release_count
    FROM spyglass.work_capacity_release_queue q
    WHERE q.account_id=p_account_id AND q.processing_state <> 'completed';
    observed_at := statement_timestamp();
    RETURN NEXT;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_attest_account_erasure_readiness(uuid,bigint) FROM PUBLIC;

COMMIT;
