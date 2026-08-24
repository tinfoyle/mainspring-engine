BEGIN;

CREATE FUNCTION spyglass_prune_analytics_events(
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
    IF p_now IS NULL OR p_retention_seconds < 2592000 OR p_retention_seconds > 63072000 OR p_limit < 1 OR p_limit > 5000 THEN
        RAISE EXCEPTION 'analytics retention policy is out of bounds';
    END IF;
    WITH candidates AS (
        SELECT event_id
        FROM public.analytics_events
        WHERE ingested_at <= p_now - make_interval(secs => p_retention_seconds)
        ORDER BY ingested_at,event_id
        LIMIT p_limit
        FOR UPDATE SKIP LOCKED
    )
    DELETE FROM public.analytics_events e
    USING candidates x
    WHERE e.event_id=x.event_id;
    GET DIAGNOSTICS pruned = ROW_COUNT;
    RETURN pruned;
END;
$$;

CREATE FUNCTION spyglass_analytics_retention_stats(
    p_now timestamptz,
    p_retention_seconds bigint
) RETURNS TABLE(total bigint,eligible bigint,oldest_eligible_age_seconds bigint)
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog, public
AS $$
    SELECT count(*)::bigint,
           count(*) FILTER (WHERE ingested_at <= p_now - make_interval(secs => p_retention_seconds))::bigint,
           COALESCE(EXTRACT(epoch FROM p_now-min(ingested_at) FILTER (WHERE ingested_at <= p_now - make_interval(secs => p_retention_seconds)))::bigint,0)
    FROM public.analytics_events
    WHERE p_now IS NOT NULL AND p_retention_seconds BETWEEN 2592000 AND 63072000
$$;

REVOKE ALL ON FUNCTION spyglass_prune_analytics_events(timestamptz,bigint,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_analytics_retention_stats(timestamptz,bigint) FROM PUBLIC;

COMMIT;
