BEGIN;

ALTER TABLE analytics_events
    ADD CONSTRAINT analytics_events_conversion_source
    UNIQUE (event_id,subject_id,consent_decision_id);

CREATE TABLE analytics_conversion_events (
    receipt_event_id uuid NOT NULL,
    source_subject_id uuid NOT NULL,
    source_consent_decision_id uuid NOT NULL,
    event_name text NOT NULL CHECK (event_name IN (
        'registration_started','verification_completed','account_created',
        'security_enrollment_completed','checkout_reviewed','checkout_redirected',
        'subscription_projected','application_entered'
    )),
    occurred_at timestamptz NOT NULL,
    fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    ingested_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (receipt_event_id,event_name),
    FOREIGN KEY (receipt_event_id,source_subject_id,source_consent_decision_id)
        REFERENCES analytics_events (event_id,subject_id,consent_decision_id) ON DELETE CASCADE,
    CONSTRAINT analytics_conversion_events_fields_object CHECK (
        jsonb_typeof(fields) = 'object' AND
        jsonb_array_length(jsonb_path_query_array(fields,'$.keyvalue()')) <= 8
    )
);
CREATE INDEX analytics_conversion_events_funnel ON analytics_conversion_events (event_name,occurred_at);

CREATE TRIGGER analytics_conversion_events_no_update
BEFORE UPDATE ON analytics_conversion_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_analytics_event_update();

REVOKE ALL ON TABLE analytics_conversion_events FROM PUBLIC;

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
    WITH report_events AS (
        SELECT e.occurred_at,e.event_name,e.surface,e.fields,e.subject_id
        FROM public.analytics_events e
        UNION ALL
        SELECT c.occurred_at,c.event_name,'conversion'::text,c.fields,c.source_subject_id
        FROM public.analytics_conversion_events c
    )
    SELECT
        date_trunc(p_bucket, e.occurred_at, 'UTC') AS bucket_start,
        e.event_name,
        e.surface,
        CASE WHEN p_dimension = 'none' THEN 'all'
             ELSE COALESCE(e.fields ->> p_dimension, '(none)') END AS dimension_value,
        count(*)::bigint AS event_count,
        count(DISTINCT e.subject_id)::bigint AS unique_subjects
    FROM report_events e
    WHERE e.occurred_at >= p_from_at AND e.occurred_at < p_to_at
    GROUP BY 1,2,3,4
    HAVING count(DISTINCT e.subject_id) >= p_minimum_cohort
    ORDER BY 1,2,3,4;
END;
$$;

REVOKE ALL ON FUNCTION spyglass_analytics_funnel_report(timestamptz,timestamptz,text,text,integer) FROM PUBLIC;

COMMIT;
