BEGIN;

CREATE TABLE operations_staff (
    user_id uuid PRIMARY KEY REFERENCES users (id),
    state text NOT NULL CHECK (state IN ('active','suspended')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (updated_at >= created_at)
);

CREATE TABLE operations_staff_role_assignments (
    id uuid PRIMARY KEY,
    staff_user_id uuid NOT NULL REFERENCES operations_staff (user_id),
    role text NOT NULL CHECK (role IN ('operations_administrator','support','billing','analytics','privacy','affiliate')),
    granted_by text NOT NULL CHECK (length(btrim(granted_by)) BETWEEN 3 AND 200 AND granted_by !~ E'[\r\n]'),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,79}$'),
    granted_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CHECK (revoked_at IS NULL OR revoked_at >= granted_at)
);
CREATE UNIQUE INDEX operations_staff_one_active_role
    ON operations_staff_role_assignments (staff_user_id,role) WHERE revoked_at IS NULL;

CREATE TABLE operations_sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES operations_staff (user_id),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
    security_version bigint NOT NULL CHECK (security_version > 0),
    authenticated_at timestamptz NOT NULL,
    reauthenticated_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    rotated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    client_label text NOT NULL CHECK (length(client_label) BETWEEN 1 AND 160 AND client_label !~ E'[\r\n]'),
    authentication_method text NOT NULL CHECK (authentication_method='passkey'),
    reauthentication_method text NOT NULL CHECK (reauthentication_method='passkey'),
    CHECK (expires_at > authenticated_at AND reauthenticated_at >= authenticated_at AND last_seen_at >= authenticated_at)
);
CREATE INDEX operations_sessions_staff_active
    ON operations_sessions (user_id,expires_at) WHERE revoked_at IS NULL;

CREATE TABLE operations_support_grants (
    id uuid PRIMARY KEY,
    staff_user_id uuid NOT NULL REFERENCES operations_staff (user_id),
    target_user_id uuid NOT NULL REFERENCES users (id),
    account_id uuid NOT NULL REFERENCES accounts (id),
    state text NOT NULL CHECK (state IN ('active','revoked')),
    ticket text NOT NULL CHECK (ticket ~ '^[A-Za-z0-9][A-Za-z0-9._/-]{2,79}$'),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    version bigint NOT NULL CHECK (version > 0),
    CHECK (staff_user_id <> target_user_id),
    CHECK (expires_at >= created_at + interval '5 minutes' AND expires_at <= created_at + interval '1 hour'),
    CHECK ((state='active' AND revoked_at IS NULL) OR (state='revoked' AND revoked_at IS NOT NULL AND revoked_at >= created_at))
);
CREATE INDEX operations_support_grants_staff_active
    ON operations_support_grants (staff_user_id,expires_at,id) WHERE state='active';
CREATE INDEX operations_support_grants_customer_history
    ON operations_support_grants (target_user_id,account_id,created_at DESC,id DESC);

CREATE TABLE operations_access_events (
    id uuid PRIMARY KEY,
    staff_user_id uuid NOT NULL REFERENCES operations_staff (user_id),
    session_id uuid REFERENCES operations_sessions (id),
    action text NOT NULL CHECK (action IN (
        'staff_authenticated','staff_logged_out','lookup_performed','support_grant_created',
        'support_view_opened','support_grant_revoked','analytics_viewed','billing_failures_viewed',
        'billing_event_replayed','subscription_refresh_queued'
    )),
    support_grant_id uuid REFERENCES operations_support_grants (id),
    target_user_id uuid REFERENCES users (id),
    account_id uuid REFERENCES accounts (id),
    ticket text NOT NULL CHECK (ticket ~ '^[A-Za-z0-9][A-Za-z0-9._/-]{2,79}$'),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,79}$'),
    details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details)='object' AND octet_length(details::text) <= 2048),
    occurred_at timestamptz NOT NULL,
    CHECK (
        (action IN ('support_grant_created','support_view_opened','support_grant_revoked') AND support_grant_id IS NOT NULL AND target_user_id IS NOT NULL AND account_id IS NOT NULL)
        OR
        (action NOT IN ('support_grant_created','support_view_opened','support_grant_revoked') AND support_grant_id IS NULL)
    )
);
CREATE INDEX operations_access_events_staff_time
    ON operations_access_events (staff_user_id,occurred_at DESC,id DESC);
CREATE INDEX operations_access_events_customer_time
    ON operations_access_events (target_user_id,account_id,occurred_at DESC,id DESC)
    WHERE target_user_id IS NOT NULL AND account_id IS NOT NULL;

CREATE TABLE operations_staff_governance_events (
    id uuid PRIMARY KEY,
    staff_user_id uuid NOT NULL REFERENCES users (id),
    role text NOT NULL CHECK (role IN ('operations_administrator','support','billing','analytics','privacy','affiliate')),
    action text NOT NULL CHECK (action IN ('role_granted','role_revoked','staff_suspended','staff_reactivated')),
    actor text NOT NULL CHECK (length(btrim(actor)) BETWEEN 3 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,79}$'),
    occurred_at timestamptz NOT NULL
);
CREATE INDEX operations_staff_governance_events_staff
    ON operations_staff_governance_events (staff_user_id,occurred_at DESC,id DESC);

CREATE FUNCTION spyglass_reject_operations_immutable_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'operations audit evidence is immutable' USING ERRCODE='42501';
END;
$$;
CREATE TRIGGER operations_access_events_immutable
BEFORE UPDATE OR DELETE ON operations_access_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_operations_immutable_mutation();
CREATE TRIGGER operations_staff_governance_events_immutable
BEFORE UPDATE OR DELETE ON operations_staff_governance_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_operations_immutable_mutation();

CREATE FUNCTION spyglass_guard_operations_role_assignment() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL OR
       NEW.id IS DISTINCT FROM OLD.id OR NEW.staff_user_id IS DISTINCT FROM OLD.staff_user_id OR
       NEW.role IS DISTINCT FROM OLD.role OR NEW.granted_by IS DISTINCT FROM OLD.granted_by OR
       NEW.reason IS DISTINCT FROM OLD.reason OR NEW.environment IS DISTINCT FROM OLD.environment OR
       NEW.granted_at IS DISTINCT FROM OLD.granted_at THEN
        RAISE EXCEPTION 'operations staff role assignment is append-only' USING ERRCODE='42501';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER operations_staff_role_assignments_guard
BEFORE UPDATE ON operations_staff_role_assignments
FOR EACH ROW EXECUTE FUNCTION spyglass_guard_operations_role_assignment();
CREATE TRIGGER operations_staff_role_assignments_no_delete
BEFORE DELETE ON operations_staff_role_assignments
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_operations_immutable_mutation();

CREATE FUNCTION spyglass_guard_operations_support_grant() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.state <> 'active' OR NEW.state <> 'revoked' OR OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL OR
       NEW.version <> OLD.version+1 OR NEW.id IS DISTINCT FROM OLD.id OR
       NEW.staff_user_id IS DISTINCT FROM OLD.staff_user_id OR NEW.target_user_id IS DISTINCT FROM OLD.target_user_id OR
       NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.ticket IS DISTINCT FROM OLD.ticket OR
       NEW.reason IS DISTINCT FROM OLD.reason OR NEW.created_at IS DISTINCT FROM OLD.created_at OR
       NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
        RAISE EXCEPTION 'operations support grant transition is invalid' USING ERRCODE='42501';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER operations_support_grants_guard
BEFORE UPDATE ON operations_support_grants
FOR EACH ROW EXECUTE FUNCTION spyglass_guard_operations_support_grant();
CREATE TRIGGER operations_support_grants_no_delete
BEFORE DELETE ON operations_support_grants
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_operations_immutable_mutation();

CREATE FUNCTION spyglass_operations_current_staff(p_user_id uuid)
RETURNS TABLE(user_id uuid,display_name text,staff_state text,roles text[])
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
    SELECT s.user_id,u.display_name,s.state,
           COALESCE(array_agg(a.role ORDER BY a.role) FILTER (WHERE a.revoked_at IS NULL),'{}'::text[])
    FROM public.operations_staff s
    JOIN public.users u ON u.id=s.user_id AND u.state='active'
    LEFT JOIN public.operations_staff_role_assignments a ON a.staff_user_id=s.user_id AND a.revoked_at IS NULL
    WHERE s.user_id=p_user_id
    GROUP BY s.user_id,u.display_name,s.state
$$;

CREATE FUNCTION spyglass_operations_assign_staff_role(
    p_event_id uuid,p_assignment_id uuid,p_staff_user_id uuid,p_role text,
    p_actor text,p_reason text,p_environment text
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE now_at timestamptz := statement_timestamp();
BEGIN
    IF p_event_id IS NULL OR p_assignment_id IS NULL OR p_staff_user_id IS NULL OR
       p_role NOT IN ('operations_administrator','support','billing','analytics','privacy','affiliate') OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,79}$' THEN
        RAISE EXCEPTION 'invalid operations staff role assignment' USING ERRCODE='22023';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM public.users WHERE id=p_staff_user_id AND state='active') THEN
        RAISE EXCEPTION 'operations staff User not found' USING ERRCODE='P0002';
    END IF;
    INSERT INTO public.operations_staff(user_id,state,version,created_at,updated_at)
    VALUES (p_staff_user_id,'active',1,now_at,now_at)
    ON CONFLICT (user_id) DO UPDATE SET state='active',version=public.operations_staff.version+1,updated_at=now_at
        WHERE public.operations_staff.state='suspended';
    INSERT INTO public.operations_staff_role_assignments
        (id,staff_user_id,role,granted_by,reason,environment,granted_at)
    VALUES (p_assignment_id,p_staff_user_id,p_role,btrim(p_actor),btrim(p_reason),p_environment,now_at);
    INSERT INTO public.operations_staff_governance_events
        (id,staff_user_id,role,action,actor,reason,environment,occurred_at)
    VALUES (p_event_id,p_staff_user_id,p_role,'role_granted',btrim(p_actor),btrim(p_reason),p_environment,now_at);
END;
$$;

CREATE FUNCTION spyglass_operations_revoke_staff_role(
    p_event_id uuid,p_staff_user_id uuid,p_role text,p_actor text,p_reason text,p_environment text
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE now_at timestamptz := statement_timestamp(); affected bigint;
BEGIN
    IF p_event_id IS NULL OR p_staff_user_id IS NULL OR
       p_role NOT IN ('operations_administrator','support','billing','analytics','privacy','affiliate') OR
       length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,79}$' THEN
        RAISE EXCEPTION 'invalid operations staff role revocation' USING ERRCODE='22023';
    END IF;
    UPDATE public.operations_staff_role_assignments SET revoked_at=now_at
    WHERE staff_user_id=p_staff_user_id AND role=p_role AND revoked_at IS NULL;
    GET DIAGNOSTICS affected=ROW_COUNT;
    IF affected <> 1 THEN RAISE EXCEPTION 'operations staff role not found' USING ERRCODE='P0002'; END IF;
    INSERT INTO public.operations_staff_governance_events
        (id,staff_user_id,role,action,actor,reason,environment,occurred_at)
    VALUES (p_event_id,p_staff_user_id,p_role,'role_revoked',btrim(p_actor),btrim(p_reason),p_environment,now_at);
    IF NOT EXISTS (SELECT 1 FROM public.operations_staff_role_assignments WHERE staff_user_id=p_staff_user_id AND revoked_at IS NULL) THEN
        UPDATE public.operations_staff SET state='suspended',version=version+1,updated_at=now_at WHERE user_id=p_staff_user_id;
        UPDATE public.operations_sessions SET revoked_at=now_at WHERE user_id=p_staff_user_id AND revoked_at IS NULL;
    END IF;
END;
$$;

CREATE FUNCTION spyglass_operations_lookup(
    p_event_id uuid,p_staff_user_id uuid,p_kind text,p_value text,p_ticket text,p_reason text,p_environment text
) RETURNS TABLE(
    user_id uuid,display_name text,email text,user_state text,email_verified boolean,
    account_id uuid,account_name text,account_state text,account_type text,
    membership_role text,membership_state text,stripe_customer_id text,stripe_subscription_id text
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE lookup_id uuid; match_count bigint;
BEGIN
    IF p_event_id IS NULL OR p_staff_user_id IS NULL OR
       p_kind NOT IN ('user_id','email','account_id','stripe_customer_id','stripe_subscription_id') OR
       length(p_value) NOT BETWEEN 1 AND 320 OR p_value ~ E'[\r\n\t ]' OR
       p_ticket !~ '^[A-Za-z0-9][A-Za-z0-9._/-]{2,79}$' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,79}$' OR
       NOT EXISTS (
           SELECT 1 FROM public.operations_staff s
           JOIN public.operations_staff_role_assignments r ON r.staff_user_id=s.user_id AND r.revoked_at IS NULL
           WHERE s.user_id=p_staff_user_id AND s.state='active' AND r.role IN ('operations_administrator','support','billing','privacy','affiliate')
       ) THEN
        RAISE EXCEPTION 'invalid or unauthorized operations lookup' USING ERRCODE='42501';
    END IF;
    IF p_kind IN ('user_id','account_id') THEN
        BEGIN lookup_id := p_value::uuid; EXCEPTION WHEN invalid_text_representation THEN
            RAISE EXCEPTION 'invalid operations lookup identifier' USING ERRCODE='22023';
        END;
    END IF;
    IF p_kind='email' AND (p_value<>lower(p_value) OR position('@' in p_value)=0) THEN
        RAISE EXCEPTION 'invalid operations email lookup' USING ERRCODE='22023';
    END IF;
    IF p_kind='stripe_customer_id' AND p_value NOT LIKE 'cus\_%' ESCAPE '\' THEN
        RAISE EXCEPTION 'invalid Stripe customer lookup' USING ERRCODE='22023';
    END IF;
    IF p_kind='stripe_subscription_id' AND p_value NOT LIKE 'sub\_%' ESCAPE '\' THEN
        RAISE EXCEPTION 'invalid Stripe subscription lookup' USING ERRCODE='22023';
    END IF;
    RETURN QUERY
    SELECT u.id,u.display_name,u.primary_email,u.state,(u.email_verified_at IS NOT NULL),
           a.id,a.display_name,a.state,a.account_type,m.role,m.state,
           COALESCE(bp.stripe_customer_id,''),COALESCE(subscription.provider_subscription_id,'')
    FROM public.users u
    JOIN public.memberships m ON m.user_id=u.id AND m.state IN ('active','suspended')
    JOIN public.accounts a ON a.id=m.account_id
    LEFT JOIN public.billing_profiles bp ON bp.account_id=a.id
    LEFT JOIN LATERAL (
        SELECT s.provider_subscription_id FROM public.subscriptions s
        WHERE s.account_id=a.id AND s.provider='stripe'
        ORDER BY s.updated_at DESC,s.id DESC LIMIT 1
    ) subscription ON true
    WHERE (p_kind='user_id' AND u.id=lookup_id) OR
          (p_kind='email' AND u.primary_email=p_value) OR
          (p_kind='account_id' AND a.id=lookup_id) OR
          (p_kind='stripe_customer_id' AND bp.stripe_customer_id=p_value) OR
          (p_kind='stripe_subscription_id' AND subscription.provider_subscription_id=p_value)
    ORDER BY lower(a.display_name),a.id,u.id LIMIT 100;
    GET DIAGNOSTICS match_count=ROW_COUNT;
    INSERT INTO public.operations_access_events
        (id,staff_user_id,action,ticket,reason,environment,details,occurred_at)
    VALUES (p_event_id,p_staff_user_id,'lookup_performed',p_ticket,btrim(p_reason),p_environment,
            jsonb_build_object('lookup_kind',p_kind,'result_count',match_count),statement_timestamp());
END;
$$;

CREATE FUNCTION spyglass_operations_record_session_event(
    p_event_id uuid,p_staff_user_id uuid,p_session_id uuid,p_action text,p_environment text,p_occurred_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
    IF p_event_id IS NULL OR p_staff_user_id IS NULL OR p_session_id IS NULL OR
       p_action NOT IN ('staff_authenticated','staff_logged_out') OR
       p_environment !~ '^[a-z][a-z0-9-]{0,79}$' OR
       NOT EXISTS (
           SELECT 1 FROM public.operations_sessions s
           WHERE s.id=p_session_id AND s.user_id=p_staff_user_id
       ) THEN
        RAISE EXCEPTION 'invalid operations session event' USING ERRCODE='42501';
    END IF;
    INSERT INTO public.operations_access_events
        (id,staff_user_id,session_id,action,ticket,reason,environment,occurred_at)
    VALUES (p_event_id,p_staff_user_id,p_session_id,p_action,'AUTH',
            CASE WHEN p_action='staff_authenticated' THEN 'Passkey-authenticated operations session started.'
                 ELSE 'Operations session ended by the staff user.' END,
            p_environment,p_occurred_at);
END;
$$;

CREATE FUNCTION spyglass_operations_create_support_grant(
    p_event_id uuid,p_grant_id uuid,p_staff_user_id uuid,p_target_user_id uuid,p_account_id uuid,
    p_ticket text,p_reason text,p_environment text,p_created_at timestamptz,p_expires_at timestamptz
) RETURNS TABLE(
    id uuid,staff_user_id uuid,target_user_id uuid,account_id uuid,state text,ticket text,reason text,
    created_at timestamptz,expires_at timestamptz,revoked_at timestamptz,version bigint
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
    IF p_event_id IS NULL OR p_grant_id IS NULL OR p_staff_user_id IS NULL OR p_target_user_id IS NULL OR p_account_id IS NULL OR
       p_staff_user_id=p_target_user_id OR p_ticket !~ '^[A-Za-z0-9][A-Za-z0-9._/-]{2,79}$' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,79}$' OR
       p_expires_at < p_created_at+interval '5 minutes' OR p_expires_at > p_created_at+interval '1 hour' OR
       NOT EXISTS (
           SELECT 1 FROM public.operations_staff s
           JOIN public.operations_staff_role_assignments r ON r.staff_user_id=s.user_id AND r.revoked_at IS NULL
           WHERE s.user_id=p_staff_user_id AND s.state='active' AND r.role IN ('operations_administrator','support')
       ) OR
       NOT EXISTS (
           SELECT 1 FROM public.memberships m
           WHERE m.user_id=p_target_user_id AND m.account_id=p_account_id AND m.state IN ('active','suspended')
       ) THEN
        RAISE EXCEPTION 'invalid or unauthorized support grant' USING ERRCODE='42501';
    END IF;
    INSERT INTO public.operations_support_grants
        (id,staff_user_id,target_user_id,account_id,state,ticket,reason,created_at,expires_at,version)
    VALUES (p_grant_id,p_staff_user_id,p_target_user_id,p_account_id,'active',p_ticket,btrim(p_reason),p_created_at,p_expires_at,1);
    INSERT INTO public.operations_access_events
        (id,staff_user_id,action,support_grant_id,target_user_id,account_id,ticket,reason,environment,occurred_at)
    VALUES (p_event_id,p_staff_user_id,'support_grant_created',p_grant_id,p_target_user_id,p_account_id,
            p_ticket,btrim(p_reason),p_environment,p_created_at);
    RETURN QUERY SELECT g.id,g.staff_user_id,g.target_user_id,g.account_id,g.state,g.ticket,g.reason,
        g.created_at,g.expires_at,g.revoked_at,g.version FROM public.operations_support_grants g WHERE g.id=p_grant_id;
END;
$$;

CREATE FUNCTION spyglass_operations_get_support_grant(p_staff_user_id uuid,p_grant_id uuid)
RETURNS TABLE(
    id uuid,staff_user_id uuid,target_user_id uuid,account_id uuid,state text,ticket text,reason text,
    created_at timestamptz,expires_at timestamptz,revoked_at timestamptz,version bigint
)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
    SELECT g.id,g.staff_user_id,g.target_user_id,g.account_id,g.state,g.ticket,g.reason,
           g.created_at,g.expires_at,g.revoked_at,g.version
    FROM public.operations_support_grants g
    JOIN public.operations_staff s ON s.user_id=g.staff_user_id AND s.state='active'
    WHERE g.id=p_grant_id AND g.staff_user_id=p_staff_user_id
$$;

CREATE FUNCTION spyglass_operations_revoke_support_grant(
    p_event_id uuid,p_staff_user_id uuid,p_grant_id uuid,p_expected_version bigint,
    p_ticket text,p_reason text,p_environment text,p_revoked_at timestamptz
) RETURNS TABLE(
    id uuid,staff_user_id uuid,target_user_id uuid,account_id uuid,state text,ticket text,reason text,
    created_at timestamptz,expires_at timestamptz,revoked_at timestamptz,version bigint
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE current_grant public.operations_support_grants%ROWTYPE;
BEGIN
    IF p_event_id IS NULL OR p_staff_user_id IS NULL OR p_grant_id IS NULL OR p_expected_version < 1 OR
       p_ticket !~ '^[A-Za-z0-9][A-Za-z0-9._/-]{2,79}$' OR
       length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,79}$' THEN
        RAISE EXCEPTION 'invalid support grant revocation' USING ERRCODE='22023';
    END IF;
    SELECT * INTO current_grant FROM public.operations_support_grants g
    WHERE g.id=p_grant_id AND g.staff_user_id=p_staff_user_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'support grant not found' USING ERRCODE='P0002'; END IF;
    IF current_grant.state<>'active' OR current_grant.version<>p_expected_version THEN
        RAISE EXCEPTION 'support grant state changed' USING ERRCODE='P0001';
    END IF;
    UPDATE public.operations_support_grants AS g
    SET state='revoked',revoked_at=p_revoked_at,version=g.version+1
    WHERE g.id=p_grant_id
    RETURNING * INTO current_grant;
    INSERT INTO public.operations_access_events
        (id,staff_user_id,action,support_grant_id,target_user_id,account_id,ticket,reason,environment,occurred_at)
    VALUES (p_event_id,p_staff_user_id,'support_grant_revoked',current_grant.id,current_grant.target_user_id,current_grant.account_id,
            p_ticket,btrim(p_reason),p_environment,p_revoked_at);
    RETURN QUERY SELECT current_grant.id,current_grant.staff_user_id,current_grant.target_user_id,current_grant.account_id,
        current_grant.state,current_grant.ticket,current_grant.reason,current_grant.created_at,current_grant.expires_at,
        current_grant.revoked_at,current_grant.version;
END;
$$;

CREATE FUNCTION spyglass_operations_customer_access_history(p_user_id uuid,p_account_id uuid,p_limit integer)
RETURNS TABLE(id uuid,staff_display_name text,action text,ticket text,reason text,occurred_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
    IF p_user_id IS NULL OR p_account_id IS NULL OR p_limit NOT BETWEEN 1 AND 100 OR
       NOT EXISTS (
           SELECT 1 FROM public.memberships m
           WHERE m.user_id=p_user_id AND m.account_id=p_account_id AND m.state='active'
       ) THEN
        RAISE EXCEPTION 'customer access history is unavailable' USING ERRCODE='42501';
    END IF;
    RETURN QUERY
    SELECT e.id,u.display_name,e.action,e.ticket,e.reason,e.occurred_at
    FROM public.operations_access_events e
    JOIN public.users u ON u.id=e.staff_user_id
    WHERE e.target_user_id=p_user_id AND e.account_id=p_account_id
      AND e.action IN ('support_grant_created','support_view_opened','support_grant_revoked')
    ORDER BY e.occurred_at DESC,e.id DESC LIMIT p_limit;
END;
$$;

REVOKE ALL ON TABLE operations_staff,operations_staff_role_assignments,operations_sessions,
    operations_support_grants,operations_access_events,operations_staff_governance_events FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_reject_operations_immutable_mutation() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_guard_operations_role_assignment() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_guard_operations_support_grant() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_operations_current_staff(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_operations_assign_staff_role(uuid,uuid,uuid,text,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_operations_revoke_staff_role(uuid,uuid,text,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_operations_lookup(uuid,uuid,text,text,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_operations_record_session_event(uuid,uuid,uuid,text,text,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_operations_create_support_grant(uuid,uuid,uuid,uuid,uuid,text,text,text,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_operations_get_support_grant(uuid,uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_operations_revoke_support_grant(uuid,uuid,uuid,bigint,text,text,text,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_operations_customer_access_history(uuid,uuid,integer) FROM PUBLIC;

COMMIT;
