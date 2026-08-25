BEGIN;

CREATE OR REPLACE FUNCTION spyglass_analytics_funnel_report(
    p_from_at timestamptz,
    p_to_at timestamptz,
    p_bucket text,
    p_dimension text,
    p_minimum_cohort integer
) RETURNS TABLE (
    bucket_start timestamptz,
    event_name text,
    surface text,
    dimension_value text,
    event_count bigint,
    unique_subjects bigint
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    IF p_from_at IS NULL OR p_to_at IS NULL OR p_from_at >= p_to_at OR
       p_to_at - p_from_at > interval '395 days' OR
       p_to_at > statement_timestamp() + interval '5 minutes' THEN
        RAISE EXCEPTION 'analytics report window is invalid';
    END IF;
    IF p_bucket NOT IN ('hour','day') OR
       (p_bucket = 'hour' AND p_to_at - p_from_at > interval '31 days') THEN
        RAISE EXCEPTION 'analytics report bucket is invalid';
    END IF;
    IF p_dimension NOT IN (
        'none','device_class','locale','route_name','cta_code','feature_code','package_code',
        'offer_code','campaign_code','method','referral_present','entry_method','result',
        'entry_point','queue_state','task_category','duration_bucket'
    ) THEN
        RAISE EXCEPTION 'analytics report dimension is invalid';
    END IF;
    IF p_minimum_cohort < 5 OR p_minimum_cohort > 100 THEN
        RAISE EXCEPTION 'analytics report minimum cohort is invalid';
    END IF;

    RETURN QUERY
    SELECT
        date_trunc(p_bucket, e.occurred_at, 'UTC') AS bucket_start,
        e.event_name,
        e.surface,
        CASE WHEN p_dimension = 'none' THEN 'all'
             ELSE COALESCE(e.fields ->> p_dimension, '(none)') END AS dimension_value,
        count(*)::bigint AS event_count,
        count(DISTINCT e.subject_id)::bigint AS unique_subjects
    FROM public.analytics_events e
    WHERE e.occurred_at >= p_from_at AND e.occurred_at < p_to_at
    GROUP BY 1,2,3,4
    HAVING count(DISTINCT e.subject_id) >= p_minimum_cohort
    ORDER BY 1,2,3,4;
END;
$$;

REVOKE ALL ON FUNCTION spyglass_analytics_funnel_report(timestamptz,timestamptz,text,text,integer) FROM PUBLIC;

COMMIT;
