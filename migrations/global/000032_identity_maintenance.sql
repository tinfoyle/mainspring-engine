BEGIN;

CREATE FUNCTION spyglass_prune_passkey_ceremonies(
    p_now timestamptz,
    p_retention_seconds bigint,
    p_limit integer
) RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    pruned bigint;
BEGIN
    IF p_now IS NULL OR p_retention_seconds < 3600 OR p_retention_seconds > 2592000 OR p_limit < 1 OR p_limit > 5000 THEN
        RAISE EXCEPTION 'identity maintenance policy is out of bounds';
    END IF;
    WITH candidates AS (
        SELECT id
        FROM public.passkey_ceremonies
        WHERE COALESCE(consumed_at,expires_at) <= p_now - make_interval(secs => p_retention_seconds)
        ORDER BY COALESCE(consumed_at,expires_at),id
        LIMIT p_limit
        FOR UPDATE SKIP LOCKED
    )
    DELETE FROM public.passkey_ceremonies c
    USING candidates x
    WHERE c.id=x.id;
    GET DIAGNOSTICS pruned = ROW_COUNT;
    RETURN pruned;
END;
$$;

CREATE FUNCTION spyglass_passkey_ceremony_retention_stats(
    p_now timestamptz,
    p_retention_seconds bigint
) RETURNS TABLE(total bigint,eligible bigint,oldest_eligible_age_seconds bigint)
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog, public
AS $$
    SELECT count(*)::bigint,
           count(*) FILTER (WHERE COALESCE(consumed_at,expires_at) <= p_now - make_interval(secs => p_retention_seconds))::bigint,
           COALESCE(EXTRACT(epoch FROM p_now-min(COALESCE(consumed_at,expires_at)) FILTER (WHERE COALESCE(consumed_at,expires_at) <= p_now - make_interval(secs => p_retention_seconds)))::bigint,0)
    FROM public.passkey_ceremonies
    WHERE p_now IS NOT NULL AND p_retention_seconds BETWEEN 3600 AND 2592000
$$;

REVOKE ALL ON FUNCTION spyglass_prune_passkey_ceremonies(timestamptz,bigint,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_passkey_ceremony_retention_stats(timestamptz,bigint) FROM PUBLIC;

COMMIT;
