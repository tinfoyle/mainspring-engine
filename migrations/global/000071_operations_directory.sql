BEGIN;

ALTER TABLE operations_access_events DROP CONSTRAINT operations_access_events_action_check;
ALTER TABLE operations_access_events ADD CONSTRAINT operations_access_events_action_check CHECK (action IN (
 'staff_authenticated','staff_logged_out','lookup_performed','support_grant_created',
 'support_view_opened','support_grant_revoked','analytics_viewed','billing_failures_viewed',
 'billing_event_replayed','subscription_refresh_queued','traffic_report_requested','directory_viewed'
));

CREATE INDEX users_operations_directory_order ON users (created_at DESC,id DESC);
CREATE INDEX accounts_operations_directory_order ON accounts (created_at DESC,id DESC);

-- Administrator-only, audited projections. The runtime role never gets table SELECT.
CREATE FUNCTION spyglass_operations_directory(
 p_event_id uuid,p_staff_user_id uuid,p_kind text,p_page integer,p_page_size integer,
 p_ticket text,p_reason text,p_environment text
) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE result jsonb;
BEGIN
 IF p_event_id IS NULL OR p_staff_user_id IS NULL OR
    p_kind IS NULL OR p_kind NOT IN ('users','teams') OR
    p_page IS NULL OR p_page NOT BETWEEN 1 AND 1000000 OR
    p_page_size IS NULL OR p_page_size NOT BETWEEN 1 AND 100 OR
    p_ticket IS NULL OR p_ticket !~ '^[A-Za-z0-9][A-Za-z0-9._/-]{2,79}$' OR
    p_reason IS NULL OR length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
    p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,79}$' THEN
  RAISE EXCEPTION 'invalid directory request' USING ERRCODE='22023';
 END IF;
 IF NOT EXISTS (
  SELECT 1 FROM public.operations_staff s
  JOIN public.users u ON u.id=s.user_id AND u.state='active'
  JOIN public.operations_staff_role_assignments r ON r.staff_user_id=s.user_id AND r.revoked_at IS NULL
  WHERE s.user_id=p_staff_user_id AND s.state='active' AND r.role='operations_administrator'
 ) THEN
  RAISE EXCEPTION 'directory requires an administrator' USING ERRCODE='42501';
 END IF;

 IF p_kind='users' THEN
  WITH page AS (
   SELECT u.id,u.display_name,u.primary_email AS email,u.state,
          (u.email_verified_at IS NOT NULL) AS email_verified,u.created_at,
          (SELECT count(*) FROM public.memberships m WHERE m.user_id=u.id AND m.state='active') AS team_count
   FROM public.users u ORDER BY u.created_at DESC,u.id DESC LIMIT p_page_size OFFSET (p_page::bigint-1)*p_page_size
  )
  SELECT jsonb_build_object('total',(SELECT count(*) FROM public.users),
    'users',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.created_at DESC,p.id DESC) FROM page p),'[]'::jsonb),
    'teams','[]'::jsonb) INTO result;
 ELSE
  WITH page AS (
   SELECT a.id,a.display_name,a.slug,a.state,a.account_type,a.created_at,
          (SELECT count(*) FROM public.memberships m WHERE m.account_id=a.id AND m.state='active') AS member_count
   FROM public.accounts a ORDER BY a.created_at DESC,a.id DESC LIMIT p_page_size OFFSET (p_page::bigint-1)*p_page_size
  )
  SELECT jsonb_build_object('total',(SELECT count(*) FROM public.accounts),
    'teams',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.created_at DESC,p.id DESC) FROM page p),'[]'::jsonb),
    'users','[]'::jsonb) INTO result;
 END IF;
 -- Results cannot leave this statement if the immutable audit insert fails.
 INSERT INTO public.operations_access_events(id,staff_user_id,action,ticket,reason,environment,details,occurred_at)
 VALUES(p_event_id,p_staff_user_id,'directory_viewed',p_ticket,btrim(p_reason),p_environment,
        jsonb_build_object('kind',p_kind,'page',p_page,'page_size',p_page_size,
          'result_count',jsonb_array_length(result->p_kind)),statement_timestamp());
 RETURN result || jsonb_build_object('kind',p_kind,'page',p_page,'page_size',p_page_size);
END;
$$;
REVOKE ALL ON FUNCTION spyglass_operations_directory(uuid,uuid,text,integer,integer,text,text,text) FROM PUBLIC;
COMMIT;
