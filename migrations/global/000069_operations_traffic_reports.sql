BEGIN;

ALTER TABLE operations_access_events DROP CONSTRAINT operations_access_events_action_check;
ALTER TABLE operations_access_events ADD CONSTRAINT operations_access_events_action_check CHECK (action IN (
    'staff_authenticated','staff_logged_out','lookup_performed','support_grant_created',
    'support_view_opened','support_grant_revoked','analytics_viewed','billing_failures_viewed',
    'billing_event_replayed','subscription_refresh_queued','traffic_report_requested'
));

-- Grant no log-file or customer-table access through SQL. This function only
-- verifies current administrator authority and records the bounded read request.
CREATE FUNCTION spyglass_operations_authorize_traffic_report(
    p_event_id uuid,p_staff_user_id uuid,p_from timestamptz,p_to timestamptz,
    p_ticket text,p_reason text,p_environment text,p_requested_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
    IF p_event_id IS NULL OR p_staff_user_id IS NULL OR p_from IS NULL OR p_to IS NULL OR
       p_requested_at IS NULL OR p_from >= p_to OR p_to-p_from > interval '7 days' OR
       p_from < p_requested_at-interval '8 days' OR p_to > p_requested_at+interval '1 minute' OR
       p_ticket IS NULL OR p_ticket !~ '^[A-Za-z0-9][A-Za-z0-9._/-]{2,79}$' OR
       p_reason IS NULL OR length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,79}$' THEN
        RAISE EXCEPTION 'invalid traffic report request' USING ERRCODE='22023';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.operations_staff s
        JOIN public.users u ON u.id=s.user_id AND u.state='active'
        JOIN public.operations_staff_role_assignments r ON r.staff_user_id=s.user_id AND r.revoked_at IS NULL
        WHERE s.user_id=p_staff_user_id AND s.state='active' AND r.role='operations_administrator'
    ) THEN
        RAISE EXCEPTION 'traffic report is unauthorized' USING ERRCODE='42501';
    END IF;
    INSERT INTO public.operations_access_events
        (id,staff_user_id,action,ticket,reason,environment,details,occurred_at)
    VALUES (p_event_id,p_staff_user_id,'traffic_report_requested',p_ticket,btrim(p_reason),p_environment,
            jsonb_build_object('from',p_from,'to',p_to),p_requested_at);
END;
$$;
REVOKE ALL ON FUNCTION spyglass_operations_authorize_traffic_report(uuid,uuid,timestamptz,timestamptz,text,text,text,timestamptz) FROM PUBLIC;

COMMIT;
