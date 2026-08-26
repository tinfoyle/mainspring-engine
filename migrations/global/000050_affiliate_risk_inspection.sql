BEGIN;

ALTER TABLE affiliate_enrollment_events
    DROP CONSTRAINT affiliate_enrollment_events_action_check;
ALTER TABLE affiliate_enrollment_events
    ADD CONSTRAINT affiliate_enrollment_events_action_check
    CHECK (action IN ('enrolled','inspected','risk_inspected','activated','suspended','closed'));
DROP INDEX affiliate_enrollment_events_transition_version;
CREATE UNIQUE INDEX affiliate_enrollment_events_transition_version
    ON affiliate_enrollment_events (affiliate_id,version)
    WHERE action NOT IN ('inspected','risk_inspected');

-- Return only content-free aggregates for a human Affiliate review. The
-- security-definer boundary intentionally withholds referred Account/User IDs,
-- public codes, checkout IDs and provider identifiers from the operator role.
CREATE FUNCTION spyglass_inspect_affiliate_risk(
    p_event_id uuid,
    p_affiliate_id uuid,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS TABLE(
    affiliate_id uuid,
    enrollment_state text,
    enrollment_version bigint,
    observed_at timestamptz,
    reservation_window_started_at timestamptz,
    valid_reservations bigint,
    distinct_referred_accounts bigint,
    repeated_referred_accounts bigint,
    maximum_reservations_per_account bigint,
    cross_affiliate_code_cycle_accounts bigint,
    locked_attributions bigint,
    largest_account_share_basis_points bigint,
    code_replacement_window_started_at timestamptz,
    code_replacements bigint
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    current_row public.affiliate_enrollments%ROWTYPE;
    observed timestamptz := statement_timestamp();
    reservation_window timestamptz;
    replacement_window timestamptz;
BEGIN
    IF p_event_id IS NULL OR p_affiliate_id IS NULL OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION 'invalid Affiliate risk inspection' USING ERRCODE='22023';
    END IF;

    SELECT * INTO current_row
      FROM public.affiliate_enrollments e
     WHERE e.affiliate_id=p_affiliate_id
     FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Affiliate enrollment not found' USING ERRCODE='P0002';
    END IF;

    reservation_window := observed - interval '24 hours';
    replacement_window := observed - interval '30 days';

    INSERT INTO public.affiliate_enrollment_events
        (event_id,affiliate_id,version,action,state,actor,reason,environment,occurred_at)
    VALUES (p_event_id,current_row.affiliate_id,current_row.version,'risk_inspected',current_row.state,
            btrim(p_actor),btrim(p_reason),p_environment,observed);

    RETURN QUERY
    WITH target AS (
        SELECT a.referred_account_id,a.state
          FROM public.affiliate_attributions a
         WHERE a.affiliate_id=p_affiliate_id
           AND a.created_at >= reservation_window
           AND a.created_at <= observed
    ), per_account AS (
        SELECT t.referred_account_id,count(*)::bigint AS reservation_count
          FROM target t
         GROUP BY t.referred_account_id
    ), aggregate_counts AS (
        SELECT count(*)::bigint AS valid_reservation_count,
               count(DISTINCT t.referred_account_id)::bigint AS distinct_account_count,
               count(*) FILTER (WHERE t.state='locked')::bigint AS locked_count
          FROM target t
    ), account_counts AS (
        SELECT count(*) FILTER (WHERE p.reservation_count >= 3)::bigint AS repeated_count,
               COALESCE(max(p.reservation_count),0)::bigint AS maximum_count,
               count(*) FILTER (WHERE EXISTS (
                   SELECT 1
                     FROM public.affiliate_attributions other
                    WHERE other.referred_account_id=p.referred_account_id
                      AND other.affiliate_id<>p_affiliate_id
                      AND other.created_at >= reservation_window
                      AND other.created_at <= observed
               ))::bigint AS cross_affiliate_count
          FROM per_account p
    )
    SELECT current_row.affiliate_id,current_row.state,current_row.version,observed,reservation_window,
           a.valid_reservation_count,a.distinct_account_count,c.repeated_count,c.maximum_count,
           c.cross_affiliate_count,a.locked_count,
           CASE WHEN a.valid_reservation_count=0 THEN 0::bigint
                ELSE floor(c.maximum_count::numeric * 10000 / a.valid_reservation_count)::bigint END,
           replacement_window,
           (SELECT count(*)::bigint
              FROM public.affiliate_public_code_history h
             WHERE h.affiliate_id=p_affiliate_id
               AND h.replaced_at >= replacement_window
               AND h.replaced_at <= observed)
      FROM aggregate_counts a CROSS JOIN account_counts c;
END;
$$;

REVOKE ALL ON FUNCTION spyglass_inspect_affiliate_risk(uuid,uuid,text,text,text) FROM PUBLIC;

COMMIT;
