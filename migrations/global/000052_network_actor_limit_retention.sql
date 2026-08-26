BEGIN;

CREATE FUNCTION spyglass_prune_network_actor_limits(
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
        RAISE EXCEPTION 'network actor limit retention policy is out of bounds' USING ERRCODE='22023';
    END IF;
    WITH candidates AS (
        SELECT r.scope,r.actor_hash
          FROM public.network_actor_rate_limits r
         WHERE r.updated_at <= p_now - make_interval(secs => p_retention_seconds)
         ORDER BY r.updated_at,r.scope,r.actor_hash
         LIMIT p_limit
         FOR UPDATE SKIP LOCKED
    )
    DELETE FROM public.network_actor_rate_limits r
     USING candidates c
     WHERE r.scope=c.scope AND r.actor_hash=c.actor_hash;
    GET DIAGNOSTICS pruned = ROW_COUNT;
    RETURN pruned;
END;
$$;

CREATE FUNCTION spyglass_network_actor_limit_retention_stats(
    p_now timestamptz,
    p_retention_seconds bigint
) RETURNS TABLE(total bigint,eligible bigint,oldest_eligible_age_seconds bigint)
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog, public
AS $$
    SELECT count(*)::bigint,
           count(*) FILTER (WHERE r.updated_at <= p_now - make_interval(secs => p_retention_seconds))::bigint,
           COALESCE(EXTRACT(epoch FROM p_now-min(r.updated_at) FILTER (
               WHERE r.updated_at <= p_now - make_interval(secs => p_retention_seconds)))::bigint,0)
      FROM public.network_actor_rate_limits r
     WHERE p_now IS NOT NULL AND p_retention_seconds BETWEEN 3600 AND 2592000
$$;

REVOKE ALL ON FUNCTION spyglass_prune_network_actor_limits(timestamptz,bigint,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_network_actor_limit_retention_stats(timestamptz,bigint) FROM PUBLIC;

COMMIT;
