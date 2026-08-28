BEGIN;

CREATE FUNCTION spyglass_operations_account_view(
    p_event_id uuid,p_staff_user_id uuid,p_grant_id uuid,p_ticket text,p_reason text,p_environment text,p_viewed_at timestamptz
) RETURNS TABLE(
    user_id uuid,user_display_name text,user_email text,user_state text,user_email_verified_at timestamptz,user_created_at timestamptz,
    passkey_count bigint,recovery_codes_remaining bigint,
    account_id uuid,account_display_name text,account_state text,account_type text,cell_id text,
    placement_generation bigint,entitlement_version bigint,account_created_at timestamptz,
    membership_role text,membership_state text,membership_version bigint,
    stripe_customer_id text,billing_email text,subscription_id text,provider_mode text,subscription_state text,
    offer_code text,offer_version bigint,current_period_end timestamptz,cancel_at timestamptz,last_synced_at timestamptz,
    snapshot_version bigint,catalog_version bigint,evaluated_at timestamptz,effective_packages jsonb,
    ai_tokens_available bigint,ai_tokens_reserved bigint,ai_tokens_consumed bigint,
    closure_request_id text,closure_state text,closure_execute_after timestamptz,closure_delete_after timestamptz,closure_blocker_code text
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE support_grant public.operations_support_grants%ROWTYPE;
BEGIN
    IF p_event_id IS NULL OR p_staff_user_id IS NULL OR p_grant_id IS NULL OR
       p_ticket !~ '^[A-Za-z0-9][A-Za-z0-9._/-]{2,79}$' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,79}$' THEN
        RAISE EXCEPTION 'invalid support view request' USING ERRCODE='22023';
    END IF;
    SELECT * INTO support_grant FROM public.operations_support_grants g
    WHERE g.id=p_grant_id AND g.staff_user_id=p_staff_user_id FOR SHARE;
    IF NOT FOUND OR support_grant.state<>'active' OR support_grant.revoked_at IS NOT NULL OR
       support_grant.expires_at<=p_viewed_at OR support_grant.ticket<>p_ticket OR
       NOT EXISTS (
           SELECT 1 FROM public.operations_staff s
           JOIN public.operations_staff_role_assignments r ON r.staff_user_id=s.user_id AND r.revoked_at IS NULL
           WHERE s.user_id=p_staff_user_id AND s.state='active' AND r.role IN ('operations_administrator','support')
       ) THEN
        RAISE EXCEPTION 'support view grant is unavailable' USING ERRCODE='42501';
    END IF;
    INSERT INTO public.operations_access_events
        (id,staff_user_id,action,support_grant_id,target_user_id,account_id,ticket,reason,environment,occurred_at)
    VALUES (p_event_id,p_staff_user_id,'support_view_opened',support_grant.id,support_grant.target_user_id,
            support_grant.account_id,p_ticket,btrim(p_reason),p_environment,p_viewed_at);
    RETURN QUERY
    SELECT u.id,u.display_name,u.primary_email,u.state,u.email_verified_at,u.created_at,
           (SELECT count(*) FROM public.passkey_credentials pc WHERE pc.user_id=u.id),
           (SELECT count(*) FROM public.user_recovery_codes rc WHERE rc.user_id=u.id AND rc.used_at IS NULL),
           a.id,a.display_name,a.state,a.account_type,a.cell_id,a.placement_generation,a.entitlement_version,a.created_at,
           m.role,m.state,m.version,
           COALESCE(bp.stripe_customer_id,''),COALESCE(bp.billing_email,''),
           COALESCE(subscription.provider_subscription_id,''),COALESCE(subscription.provider_mode,''),COALESCE(subscription.state,''),
           COALESCE(subscription.offer_code,''),COALESCE(subscription.offer_version,0),subscription.current_period_end,
           subscription.cancel_at,subscription.last_synced_at,
           snapshot.version,snapshot.catalog_version,snapshot.evaluated_at,snapshot.effective_packages,
           COALESCE(tokens.available,0),COALESCE(tokens.reserved,0),COALESCE(tokens.consumed,0),
           COALESCE(closure.id::text,''),COALESCE(closure.state,''),closure.execute_after,closure.delete_after,COALESCE(closure.blocker_code,'')
    FROM public.users u
    JOIN public.memberships m ON m.user_id=u.id AND m.account_id=support_grant.account_id AND m.state IN ('active','suspended')
    JOIN public.accounts a ON a.id=m.account_id
    LEFT JOIN public.billing_profiles bp ON bp.account_id=a.id
    LEFT JOIN LATERAL (
        SELECT s.provider_subscription_id,s.provider_mode,s.state,s.offer_code,s.offer_version,s.current_period_end,
               s.cancel_at,s.last_synced_at,s.id
        FROM public.subscriptions s WHERE s.account_id=a.id AND s.provider='stripe'
        ORDER BY s.updated_at DESC,s.id DESC LIMIT 1
    ) subscription ON true
    JOIN LATERAL (
        SELECT es.version,es.catalog_version,es.evaluated_at,es.effective_packages
        FROM public.entitlement_snapshots es WHERE es.account_id=a.id ORDER BY es.version DESC LIMIT 1
    ) snapshot ON true
    LEFT JOIN LATERAL (
        SELECT sum(g.available)::bigint AS available,sum(g.reserved)::bigint AS reserved,sum(g.consumed)::bigint AS consumed
        FROM public.ai_token_grants g WHERE g.account_id=a.id
    ) tokens ON true
    LEFT JOIN LATERAL (
        SELECT c.id,c.state,c.execute_after,c.delete_after,c.blocker_code
        FROM public.account_closure_requests c WHERE c.account_id=a.id
        ORDER BY c.requested_at DESC,c.id DESC LIMIT 1
    ) closure ON true
    WHERE u.id=support_grant.target_user_id AND a.id=support_grant.account_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'support view target no longer exists' USING ERRCODE='P0002';
    END IF;
END;
$$;

CREATE FUNCTION spyglass_operations_analytics_report(
    p_event_id uuid,p_staff_user_id uuid,p_from timestamptz,p_to timestamptz,p_bucket text,p_dimension text,
    p_minimum_cohort integer,p_ticket text,p_reason text,p_environment text,p_viewed_at timestamptz
) RETURNS TABLE(
    bucket_start timestamptz,event_name text,surface text,dimension_value text,event_count bigint,unique_subjects bigint
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE result_count bigint;
BEGIN
    IF p_event_id IS NULL OR p_staff_user_id IS NULL OR
       p_ticket !~ '^[A-Za-z0-9][A-Za-z0-9._/-]{2,79}$' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,79}$' OR
       NOT EXISTS (
           SELECT 1 FROM public.operations_staff s
           JOIN public.operations_staff_role_assignments r ON r.staff_user_id=s.user_id AND r.revoked_at IS NULL
           WHERE s.user_id=p_staff_user_id AND s.state='active' AND r.role IN ('operations_administrator','analytics')
       ) THEN
        RAISE EXCEPTION 'analytics report is unauthorized' USING ERRCODE='42501';
    END IF;
    RETURN QUERY SELECT * FROM public.spyglass_analytics_funnel_report(
        p_from,p_to,p_bucket,p_dimension,p_minimum_cohort
    );
    GET DIAGNOSTICS result_count=ROW_COUNT;
    INSERT INTO public.operations_access_events
        (id,staff_user_id,action,ticket,reason,environment,details,occurred_at)
    VALUES (p_event_id,p_staff_user_id,'analytics_viewed',p_ticket,btrim(p_reason),p_environment,
            jsonb_build_object('from',p_from,'to',p_to,'bucket',p_bucket,'dimension',p_dimension,
                               'minimum_cohort',p_minimum_cohort,'result_count',result_count),p_viewed_at);
END;
$$;

CREATE FUNCTION spyglass_operations_support_history(p_staff_user_id uuid,p_grant_id uuid,p_limit integer,p_viewed_at timestamptz)
RETURNS TABLE(id uuid,staff_display_name text,action text,ticket text,reason text,occurred_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE support_grant public.operations_support_grants%ROWTYPE;
BEGIN
    IF p_staff_user_id IS NULL OR p_grant_id IS NULL OR p_limit NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION 'support history request is invalid' USING ERRCODE='22023';
    END IF;
    SELECT * INTO support_grant FROM public.operations_support_grants g
    WHERE g.id=p_grant_id AND g.staff_user_id=p_staff_user_id;
    IF NOT FOUND OR support_grant.state<>'active' OR support_grant.revoked_at IS NOT NULL OR support_grant.expires_at<=p_viewed_at THEN
        RAISE EXCEPTION 'support history grant is unavailable' USING ERRCODE='42501';
    END IF;
    RETURN QUERY
    SELECT e.id,u.display_name,e.action,e.ticket,e.reason,e.occurred_at
    FROM public.operations_access_events e
    JOIN public.users u ON u.id=e.staff_user_id
    WHERE e.target_user_id=support_grant.target_user_id AND e.account_id=support_grant.account_id
      AND e.action IN ('support_grant_created','support_view_opened','support_grant_revoked')
    ORDER BY e.occurred_at DESC,e.id DESC LIMIT p_limit;
END;
$$;

REVOKE ALL ON FUNCTION spyglass_operations_account_view(uuid,uuid,uuid,text,text,text,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_operations_analytics_report(uuid,uuid,timestamptz,timestamptz,text,text,integer,text,text,text,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_operations_support_history(uuid,uuid,integer,timestamptz) FROM PUBLIC;

COMMIT;
