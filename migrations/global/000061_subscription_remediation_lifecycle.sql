BEGIN;

ALTER TABLE account_closure_requests DROP CONSTRAINT account_closure_request_terminal_shape;
ALTER TABLE account_closure_requests ADD CONSTRAINT account_closure_request_terminal_shape CHECK (
    (state IN ('cooling_off','processing') AND blocker_code IS NULL AND canceled_at IS NULL AND closed_at IS NULL AND delete_after IS NULL)
    OR (state='blocked' AND blocker_code IS NOT NULL AND canceled_at IS NULL AND closed_at IS NULL AND delete_after IS NULL)
    OR (state='canceled' AND blocker_code IS NULL AND canceled_by_user_id IS NOT NULL AND cancel_reason IS NOT NULL AND canceled_at IS NOT NULL AND closed_at IS NULL AND delete_after IS NULL)
    OR (state='closed' AND blocker_code IS NULL AND canceled_at IS NULL AND closed_at IS NOT NULL AND delete_after>=closed_at)
);

ALTER TABLE identity_notification_outbox DROP CONSTRAINT identity_notification_outbox_kind_check;
ALTER TABLE identity_notification_outbox ADD CONSTRAINT identity_notification_outbox_kind_check CHECK (kind IN (
    'verification','invitation','recovery','discard','ownership_transfer','contact_change','subscription_lifecycle'
));

CREATE TABLE account_subscription_lifecycles (
    lifecycle_id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    provider_subscription_id text NOT NULL CHECK (provider_subscription_id ~ '^sub_[A-Za-z0-9_]+$'),
    trigger_kind text NOT NULL CHECK (trigger_kind IN ('payment_failure','cancellation')),
    state text NOT NULL CHECK (state IN ('cancellation_scheduled','grace_read_only','restricted','termination_pending','recovered','closed')),
    effective_at timestamptz NOT NULL,
    restriction_at timestamptz NOT NULL,
    delete_at timestamptz NOT NULL,
    next_attempt_at timestamptz NOT NULL,
    version bigint NOT NULL CHECK (version>0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    recovered_at timestamptz,
    closed_at timestamptz,
    lease_id uuid,
    lease_expires_at timestamptz,
    CONSTRAINT account_subscription_lifecycle_time_order CHECK (
        restriction_at>=effective_at AND delete_at=effective_at+interval '30 days' AND updated_at>=created_at
    ),
    CONSTRAINT account_subscription_lifecycle_terminal_shape CHECK (
        (state NOT IN ('recovered','closed') AND recovered_at IS NULL AND closed_at IS NULL)
        OR (state='recovered' AND recovered_at IS NOT NULL AND closed_at IS NULL)
        OR (state='closed' AND recovered_at IS NULL AND closed_at IS NOT NULL)
    ),
    CONSTRAINT account_subscription_lifecycle_lease_shape CHECK (
        (lease_id IS NULL AND lease_expires_at IS NULL) OR (lease_id IS NOT NULL AND lease_expires_at IS NOT NULL)
    )
);
CREATE UNIQUE INDEX account_subscription_lifecycle_one_open
    ON account_subscription_lifecycles (account_id)
    WHERE state NOT IN ('recovered','closed');
CREATE INDEX account_subscription_lifecycle_due
    ON account_subscription_lifecycles (next_attempt_at,lifecycle_id)
    WHERE state NOT IN ('recovered','closed');

CREATE TABLE account_subscription_lifecycle_events (
    event_id uuid PRIMARY KEY,
    lifecycle_id uuid NOT NULL REFERENCES account_subscription_lifecycles (lifecycle_id),
    version bigint NOT NULL CHECK (version>0),
    action text NOT NULL CHECK (action IN ('cancellation_scheduled','payment_failed','restricted','termination_queued','recovered','closed')),
    state text NOT NULL CHECK (state IN ('cancellation_scheduled','grace_read_only','restricted','termination_pending','recovered','closed')),
    occurred_at timestamptz NOT NULL,
    UNIQUE (lifecycle_id,version)
);

CREATE TABLE account_subscription_lifecycle_notices (
    notice_id uuid PRIMARY KEY,
    lifecycle_id uuid NOT NULL REFERENCES account_subscription_lifecycles (lifecycle_id),
    kind text NOT NULL CHECK (kind IN (
        'payment_failed','payment_restricted','payment_day23','payment_day29',
        'cancellation_scheduled','cancellation_effective','cancellation_day23','cancellation_day29'
    )),
    due_at timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('scheduled','processing','emitted','canceled','failed','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at timestamptz NOT NULL,
    lease_id uuid,
    lease_expires_at timestamptz,
    last_error_code text,
    emitted_at timestamptz,
    created_at timestamptz NOT NULL,
    UNIQUE (lifecycle_id,kind),
    CONSTRAINT account_subscription_lifecycle_notice_lease_shape CHECK (
        (state='processing' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (state<>'processing' AND lease_id IS NULL AND lease_expires_at IS NULL)
    ),
    CONSTRAINT account_subscription_lifecycle_notice_emitted_shape CHECK (
        (state='emitted' AND emitted_at IS NOT NULL) OR (state<>'emitted' AND emitted_at IS NULL)
    )
);
CREATE INDEX account_subscription_lifecycle_notice_due
    ON account_subscription_lifecycle_notices (next_attempt_at,notice_id)
    WHERE state IN ('scheduled','processing','failed');

CREATE TABLE account_subscription_termination_jobs (
    lifecycle_id uuid PRIMARY KEY REFERENCES account_subscription_lifecycles (lifecycle_id),
    provider_subscription_id text NOT NULL CHECK (provider_subscription_id ~ '^sub_[A-Za-z0-9_]+$'),
    state text NOT NULL CHECK (state IN ('pending','processing','failed','completed','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at timestamptz NOT NULL,
    lease_id uuid,
    lease_expires_at timestamptz,
    last_error_code text,
    requested_at timestamptz NOT NULL,
    completed_at timestamptz,
    CONSTRAINT account_subscription_termination_lease_shape CHECK (
        (state='processing' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (state<>'processing' AND lease_id IS NULL AND lease_expires_at IS NULL)
    ),
    CONSTRAINT account_subscription_termination_completion_shape CHECK (
        (state='completed' AND completed_at IS NOT NULL) OR (state<>'completed' AND completed_at IS NULL)
    )
);
CREATE INDEX account_subscription_termination_due
    ON account_subscription_termination_jobs (next_attempt_at,lifecycle_id)
    WHERE state IN ('pending','processing','failed');

CREATE FUNCTION spyglass_reject_subscription_lifecycle_evidence_mutation() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,public
AS $$
DECLARE evidence_owner text;
BEGIN
    SELECT pg_get_userbyid(c.relowner) INTO evidence_owner
      FROM pg_class c WHERE c.oid='public.account_subscription_lifecycle_events'::regclass;
    IF TG_OP='DELETE' AND current_user=evidence_owner AND
       EXISTS (
           SELECT 1 FROM public.account_subscription_lifecycles lifecycle
            WHERE lifecycle.lifecycle_id=OLD.lifecycle_id
              AND current_setting('spyglass.erasure_account_id',true)=lifecycle.account_id::text
       ) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'subscription lifecycle evidence is immutable';
END;
$$;
CREATE TRIGGER account_subscription_lifecycle_events_immutable
BEFORE UPDATE OR DELETE ON account_subscription_lifecycle_events
FOR EACH ROW EXECUTE FUNCTION spyglass_reject_subscription_lifecycle_evidence_mutation();

CREATE FUNCTION spyglass_erase_subscription_lifecycle_graph() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,public
AS $$
DECLARE evidence_owner text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.account_subscription_lifecycles lifecycle WHERE lifecycle.account_id=OLD.id) THEN
        RETURN OLD;
    END IF;
    SELECT pg_get_userbyid(c.relowner) INTO evidence_owner
      FROM pg_class c WHERE c.oid='public.account_subscription_lifecycle_events'::regclass;
    IF current_user<>evidence_owner OR current_setting('spyglass.erasure_account_id',true) IS DISTINCT FROM OLD.id::text THEN
        RAISE EXCEPTION 'subscription lifecycle graph may only be removed by Account erasure';
    END IF;
    DELETE FROM public.account_subscription_termination_jobs job
     USING public.account_subscription_lifecycles lifecycle
     WHERE job.lifecycle_id=lifecycle.lifecycle_id AND lifecycle.account_id=OLD.id;
    DELETE FROM public.account_subscription_lifecycle_notices notice
     USING public.account_subscription_lifecycles lifecycle
     WHERE notice.lifecycle_id=lifecycle.lifecycle_id AND lifecycle.account_id=OLD.id;
    DELETE FROM public.account_subscription_lifecycle_events event
     USING public.account_subscription_lifecycles lifecycle
     WHERE event.lifecycle_id=lifecycle.lifecycle_id AND lifecycle.account_id=OLD.id;
    DELETE FROM public.account_subscription_lifecycles lifecycle WHERE lifecycle.account_id=OLD.id;
    RETURN OLD;
END;
$$;
CREATE TRIGGER accounts_erase_subscription_lifecycle_graph
BEFORE DELETE ON accounts
FOR EACH ROW EXECUTE FUNCTION spyglass_erase_subscription_lifecycle_graph();

CREATE FUNCTION spyglass_schedule_subscription_lifecycle_notices(
    p_lifecycle_id uuid,p_trigger_kind text,p_now timestamptz,p_effective_at timestamptz
) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,public
AS $$
BEGIN
    IF p_trigger_kind='payment_failure' THEN
        INSERT INTO public.account_subscription_lifecycle_notices
            (notice_id,lifecycle_id,kind,due_at,state,next_attempt_at,created_at)
        VALUES
            (gen_random_uuid(),p_lifecycle_id,'payment_failed',p_now,'scheduled',p_now,p_now),
            (gen_random_uuid(),p_lifecycle_id,'payment_restricted',p_effective_at+interval '7 days','scheduled',p_effective_at+interval '7 days',p_now),
            (gen_random_uuid(),p_lifecycle_id,'payment_day23',p_effective_at+interval '23 days','scheduled',p_effective_at+interval '23 days',p_now),
            (gen_random_uuid(),p_lifecycle_id,'payment_day29',p_effective_at+interval '29 days','scheduled',p_effective_at+interval '29 days',p_now);
    ELSE
        INSERT INTO public.account_subscription_lifecycle_notices
            (notice_id,lifecycle_id,kind,due_at,state,next_attempt_at,created_at)
        VALUES
            (gen_random_uuid(),p_lifecycle_id,'cancellation_scheduled',p_now,'scheduled',p_now,p_now),
            (gen_random_uuid(),p_lifecycle_id,'cancellation_effective',p_effective_at,'scheduled',p_effective_at,p_now),
            (gen_random_uuid(),p_lifecycle_id,'cancellation_day23',p_effective_at+interval '23 days','scheduled',p_effective_at+interval '23 days',p_now),
            (gen_random_uuid(),p_lifecycle_id,'cancellation_day29',p_effective_at+interval '29 days','scheduled',p_effective_at+interval '29 days',p_now);
    END IF;
END;
$$;

CREATE FUNCTION spyglass_recover_subscription_lifecycle(p_lifecycle_id uuid,p_now timestamptz) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,public
AS $$
DECLARE current_row public.account_subscription_lifecycles%ROWTYPE;
BEGIN
    SELECT * INTO current_row FROM public.account_subscription_lifecycles lifecycle
     WHERE lifecycle.lifecycle_id=p_lifecycle_id FOR UPDATE;
    IF NOT FOUND OR current_row.state IN ('recovered','closed') THEN RETURN; END IF;
    UPDATE public.account_subscription_lifecycles lifecycle
       SET state='recovered',version=lifecycle.version+1,updated_at=p_now,recovered_at=p_now,
           next_attempt_at=p_now,lease_id=NULL,lease_expires_at=NULL
     WHERE lifecycle.lifecycle_id=p_lifecycle_id RETURNING * INTO current_row;
    UPDATE public.account_subscription_lifecycle_notices
       SET state='canceled',lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL
     WHERE lifecycle_id=p_lifecycle_id AND state IN ('scheduled','processing','failed');
    UPDATE public.account_subscription_termination_jobs
       SET state='dead_letter',lease_id=NULL,lease_expires_at=NULL,last_error_code='lifecycle_recovered'
     WHERE lifecycle_id=p_lifecycle_id AND state IN ('pending','processing','failed');
    INSERT INTO public.account_subscription_lifecycle_events(event_id,lifecycle_id,version,action,state,occurred_at)
    VALUES (gen_random_uuid(),p_lifecycle_id,current_row.version,'recovered','recovered',p_now);
END;
$$;

CREATE FUNCTION spyglass_project_subscription_lifecycle(
    p_account_id uuid,p_provider_subscription_id text,p_subscription_state text,
    p_current_period_end timestamptz,p_cancel_at timestamptz,p_now timestamptz
) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,public
AS $$
DECLARE
    account_row public.accounts%ROWTYPE;
    current_row public.account_subscription_lifecycles%ROWTYPE;
    lifecycle_id_value uuid;
    effective_at_value timestamptz;
BEGIN
    IF p_account_id IS NULL OR p_provider_subscription_id !~ '^sub_[A-Za-z0-9_]+$' OR
       p_subscription_state NOT IN ('active','trialing','past_due','unpaid','paused','incomplete','incomplete_expired','canceled') OR p_now IS NULL THEN
        RAISE EXCEPTION 'invalid subscription lifecycle projection' USING ERRCODE='22023';
    END IF;
    SELECT * INTO account_row FROM public.accounts account WHERE account.id=p_account_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'subscription lifecycle Account not found' USING ERRCODE='P0002'; END IF;
    IF account_row.state='closed' THEN
        RAISE EXCEPTION 'subscription lifecycle cannot project onto a closed Account' USING ERRCODE='P0001';
    END IF;
    SELECT * INTO current_row FROM public.account_subscription_lifecycles lifecycle
     WHERE lifecycle.account_id=p_account_id AND lifecycle.state NOT IN ('recovered','closed') FOR UPDATE;

    IF p_subscription_state IN ('active','trialing') THEN
        IF FOUND THEN
            IF current_row.trigger_kind='payment_failure' OR p_cancel_at IS NULL OR
               current_row.provider_subscription_id<>p_provider_subscription_id OR
               (current_row.trigger_kind='cancellation' AND current_row.effective_at<>GREATEST(p_cancel_at,p_now)) THEN
                PERFORM public.spyglass_recover_subscription_lifecycle(current_row.lifecycle_id,p_now);
                IF account_row.state='restricted' THEN
                    UPDATE public.accounts account SET state='active',version=account.version+1 WHERE account.id=p_account_id;
                    account_row.state := 'active';
                END IF;
                current_row.lifecycle_id := NULL;
            END IF;
        END IF;
        IF p_cancel_at IS NOT NULL AND current_row.lifecycle_id IS NULL THEN
            effective_at_value := GREATEST(p_cancel_at,p_now);
            lifecycle_id_value := gen_random_uuid();
            INSERT INTO public.account_subscription_lifecycles
                (lifecycle_id,account_id,provider_subscription_id,trigger_kind,state,effective_at,restriction_at,
                 delete_at,next_attempt_at,version,created_at,updated_at)
            VALUES (lifecycle_id_value,p_account_id,p_provider_subscription_id,'cancellation','cancellation_scheduled',
                    effective_at_value,effective_at_value,effective_at_value+interval '30 days',effective_at_value,1,p_now,p_now);
            INSERT INTO public.account_subscription_lifecycle_events(event_id,lifecycle_id,version,action,state,occurred_at)
            VALUES (gen_random_uuid(),lifecycle_id_value,1,'cancellation_scheduled','cancellation_scheduled',p_now);
            PERFORM public.spyglass_schedule_subscription_lifecycle_notices(lifecycle_id_value,'cancellation',p_now,effective_at_value);
        END IF;
        RETURN;
    END IF;

    IF p_subscription_state IN ('past_due','unpaid') THEN
        IF current_row.lifecycle_id IS NOT NULL AND current_row.trigger_kind='payment_failure' THEN RETURN; END IF;
        IF current_row.lifecycle_id IS NOT NULL THEN
            PERFORM public.spyglass_recover_subscription_lifecycle(current_row.lifecycle_id,p_now);
        END IF;
        lifecycle_id_value := gen_random_uuid();
        INSERT INTO public.account_subscription_lifecycles
            (lifecycle_id,account_id,provider_subscription_id,trigger_kind,state,effective_at,restriction_at,
             delete_at,next_attempt_at,version,created_at,updated_at)
        VALUES (lifecycle_id_value,p_account_id,p_provider_subscription_id,'payment_failure','grace_read_only',
                p_now,p_now+interval '7 days',p_now+interval '30 days',p_now+interval '7 days',1,p_now,p_now);
        INSERT INTO public.account_subscription_lifecycle_events(event_id,lifecycle_id,version,action,state,occurred_at)
        VALUES (gen_random_uuid(),lifecycle_id_value,1,'payment_failed','grace_read_only',p_now);
        PERFORM public.spyglass_schedule_subscription_lifecycle_notices(lifecycle_id_value,'payment_failure',p_now,p_now);
        RETURN;
    END IF;

    IF p_subscription_state='canceled' THEN
        effective_at_value := CASE WHEN p_current_period_end IS NOT NULL AND p_current_period_end<=p_now THEN p_current_period_end ELSE p_now END;
        IF current_row.lifecycle_id IS NULL THEN
            lifecycle_id_value := gen_random_uuid();
            INSERT INTO public.account_subscription_lifecycles
                (lifecycle_id,account_id,provider_subscription_id,trigger_kind,state,effective_at,restriction_at,
                 delete_at,next_attempt_at,version,created_at,updated_at)
            VALUES (lifecycle_id_value,p_account_id,p_provider_subscription_id,'cancellation','restricted',
                    effective_at_value,effective_at_value,effective_at_value+interval '30 days',effective_at_value+interval '30 days',1,p_now,p_now);
            INSERT INTO public.account_subscription_lifecycle_events(event_id,lifecycle_id,version,action,state,occurred_at)
            VALUES (gen_random_uuid(),lifecycle_id_value,1,'restricted','restricted',p_now);
            PERFORM public.spyglass_schedule_subscription_lifecycle_notices(lifecycle_id_value,'cancellation',p_now,effective_at_value);
            UPDATE public.account_subscription_lifecycle_notices
               SET state='canceled'
             WHERE lifecycle_id=lifecycle_id_value AND kind='cancellation_scheduled';
        ELSE
            lifecycle_id_value := current_row.lifecycle_id;
            IF current_row.state IN ('cancellation_scheduled','grace_read_only') THEN
                UPDATE public.account_subscription_lifecycles lifecycle
                   SET state='restricted',version=lifecycle.version+1,updated_at=p_now,
                       next_attempt_at=lifecycle.delete_at,lease_id=NULL,lease_expires_at=NULL
                 WHERE lifecycle.lifecycle_id=lifecycle_id_value RETURNING * INTO current_row;
                INSERT INTO public.account_subscription_lifecycle_events(event_id,lifecycle_id,version,action,state,occurred_at)
                VALUES (gen_random_uuid(),lifecycle_id_value,current_row.version,'restricted','restricted',p_now);
            END IF;
        END IF;
        IF account_row.state='active' THEN
            UPDATE public.accounts account SET state='restricted',version=account.version+1 WHERE account.id=p_account_id;
        END IF;
    END IF;
END;
$$;

CREATE FUNCTION spyglass_claim_subscription_lifecycle(p_now timestamptz,p_lease_seconds bigint)
RETURNS TABLE(lifecycle_id uuid,account_id uuid,state text,trigger_kind text,effective_at timestamptz,restriction_at timestamptz,delete_at timestamptz,lease_id uuid)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,public
AS $$
DECLARE claimed public.account_subscription_lifecycles%ROWTYPE;
BEGIN
    IF p_now IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION 'invalid subscription lifecycle claim' USING ERRCODE='22023';
    END IF;
    WITH candidate AS (
        SELECT lifecycle.lifecycle_id FROM public.account_subscription_lifecycles lifecycle
         WHERE lifecycle.state NOT IN ('recovered','closed') AND lifecycle.next_attempt_at<=p_now
           AND (lifecycle.lease_expires_at IS NULL OR lifecycle.lease_expires_at<=p_now)
         ORDER BY lifecycle.next_attempt_at,lifecycle.lifecycle_id FOR UPDATE SKIP LOCKED LIMIT 1
    )
    UPDATE public.account_subscription_lifecycles lifecycle
       SET lease_id=gen_random_uuid(),lease_expires_at=p_now+(p_lease_seconds*interval '1 second')
      FROM candidate WHERE lifecycle.lifecycle_id=candidate.lifecycle_id RETURNING lifecycle.* INTO claimed;
    IF NOT FOUND THEN RETURN; END IF;
    RETURN QUERY SELECT claimed.lifecycle_id,claimed.account_id,claimed.state,claimed.trigger_kind,
                        claimed.effective_at,claimed.restriction_at,claimed.delete_at,claimed.lease_id;
END;
$$;

CREATE FUNCTION spyglass_advance_subscription_lifecycle(
    p_lifecycle_id uuid,p_lease_id uuid,p_now timestamptz,p_retry_seconds bigint
) RETURNS text
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,public
AS $$
DECLARE
    current_row public.account_subscription_lifecycles%ROWTYPE;
    account_row public.accounts%ROWTYPE;
    subscription_state text;
    blocker boolean;
    closure_id uuid;
BEGIN
    IF p_lifecycle_id IS NULL OR p_lease_id IS NULL OR p_now IS NULL OR p_retry_seconds NOT BETWEEN 60 AND 604800 THEN
        RAISE EXCEPTION 'invalid subscription lifecycle advancement' USING ERRCODE='22023';
    END IF;
    SELECT * INTO current_row FROM public.account_subscription_lifecycles lifecycle
     WHERE lifecycle.lifecycle_id=p_lifecycle_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'subscription lifecycle not found' USING ERRCODE='P0002'; END IF;
    IF current_row.lease_id<>p_lease_id OR current_row.lease_expires_at<=p_now OR current_row.state IN ('recovered','closed') THEN
        RAISE EXCEPTION 'subscription lifecycle lease changed' USING ERRCODE='P0001';
    END IF;
    SELECT * INTO account_row FROM public.accounts account WHERE account.id=current_row.account_id FOR UPDATE;
    SELECT subscription.state INTO subscription_state FROM public.subscriptions subscription
     WHERE subscription.account_id=current_row.account_id
       AND subscription.provider_subscription_id=current_row.provider_subscription_id FOR SHARE;

    IF current_row.state IN ('cancellation_scheduled','grace_read_only') THEN
        IF (current_row.state='cancellation_scheduled' AND current_row.effective_at>p_now) OR
           (current_row.state='grace_read_only' AND current_row.restriction_at>p_now) THEN
            UPDATE public.account_subscription_lifecycles lifecycle
               SET next_attempt_at=CASE WHEN lifecycle.state='cancellation_scheduled' THEN lifecycle.effective_at ELSE lifecycle.restriction_at END,
                   lease_id=NULL,lease_expires_at=NULL
             WHERE lifecycle.lifecycle_id=p_lifecycle_id;
            RETURN current_row.state;
        END IF;
        UPDATE public.account_subscription_lifecycles lifecycle
           SET state='restricted',version=lifecycle.version+1,updated_at=p_now,next_attempt_at=lifecycle.delete_at,
               lease_id=NULL,lease_expires_at=NULL
         WHERE lifecycle.lifecycle_id=p_lifecycle_id RETURNING * INTO current_row;
        IF account_row.state='active' THEN
            UPDATE public.accounts account SET state='restricted',version=account.version+1 WHERE account.id=current_row.account_id;
        END IF;
        INSERT INTO public.account_subscription_lifecycle_events(event_id,lifecycle_id,version,action,state,occurred_at)
        VALUES (gen_random_uuid(),p_lifecycle_id,current_row.version,'restricted','restricted',p_now);
        RETURN current_row.state;
    END IF;

    IF current_row.state='restricted' AND current_row.delete_at<=p_now AND COALESCE(subscription_state,'') NOT IN ('canceled','incomplete_expired') THEN
        INSERT INTO public.account_subscription_termination_jobs
            (lifecycle_id,provider_subscription_id,state,next_attempt_at,requested_at)
        VALUES (p_lifecycle_id,current_row.provider_subscription_id,'pending',p_now,p_now)
        ON CONFLICT (lifecycle_id) DO NOTHING;
        UPDATE public.account_subscription_lifecycles lifecycle
           SET state='termination_pending',version=lifecycle.version+1,updated_at=p_now,next_attempt_at=p_now+(p_retry_seconds*interval '1 second'),
               lease_id=NULL,lease_expires_at=NULL
         WHERE lifecycle.lifecycle_id=p_lifecycle_id RETURNING * INTO current_row;
        INSERT INTO public.account_subscription_lifecycle_events(event_id,lifecycle_id,version,action,state,occurred_at)
        VALUES (gen_random_uuid(),p_lifecycle_id,current_row.version,'termination_queued','termination_pending',p_now);
        RETURN current_row.state;
    END IF;

    IF current_row.state IN ('restricted','termination_pending') AND current_row.delete_at<=p_now THEN
        SELECT EXISTS (
            SELECT 1 FROM public.billing_checkout_attempts checkout
             WHERE checkout.account_id=current_row.account_id AND checkout.state='active' AND checkout.expires_at>p_now
        ) OR EXISTS (
            SELECT 1 FROM public.entitlement_usage_reservations reservation
             WHERE reservation.account_id=current_row.account_id AND reservation.state='active'
        ) OR EXISTS (
            SELECT 1 FROM public.entitlement_usage_counters counter
             WHERE counter.account_id=current_row.account_id AND counter.current_value<>0
        ) INTO blocker;
        IF blocker OR COALESCE(subscription_state,'') NOT IN ('canceled','incomplete_expired') THEN
            UPDATE public.account_subscription_lifecycles lifecycle
               SET next_attempt_at=p_now+(p_retry_seconds*interval '1 second'),lease_id=NULL,lease_expires_at=NULL
             WHERE lifecycle.lifecycle_id=p_lifecycle_id;
            RETURN current_row.state;
        END IF;
        IF account_row.state<>'restricted' THEN
            RAISE EXCEPTION 'subscription lifecycle Account state changed' USING ERRCODE='P0001';
        END IF;
        closure_id := gen_random_uuid();
        UPDATE public.accounts account SET state='closed',version=account.version+1
         WHERE account.id=current_row.account_id RETURNING * INTO account_row;
        INSERT INTO public.account_closure_requests
            (id,account_id,state,requested_by_user_id,reason,account_version,requested_at,execute_after,next_attempt_at,
             closed_at,delete_after)
        VALUES (closure_id,current_row.account_id,'closed',account_row.created_by_user_id,
                'Subscription lifecycle deletion deadline reached',account_row.version,current_row.created_at,
                current_row.delete_at,current_row.delete_at,p_now,p_now);
        UPDATE public.account_subscription_lifecycles lifecycle
           SET state='closed',version=lifecycle.version+1,updated_at=p_now,closed_at=p_now,next_attempt_at=p_now,
               lease_id=NULL,lease_expires_at=NULL
         WHERE lifecycle.lifecycle_id=p_lifecycle_id RETURNING * INTO current_row;
        INSERT INTO public.account_subscription_lifecycle_events(event_id,lifecycle_id,version,action,state,occurred_at)
        VALUES (gen_random_uuid(),p_lifecycle_id,current_row.version,'closed','closed',p_now);
        RETURN current_row.state;
    END IF;

    UPDATE public.account_subscription_lifecycles lifecycle
       SET next_attempt_at=GREATEST(lifecycle.next_attempt_at,p_now+(p_retry_seconds*interval '1 second')),
           lease_id=NULL,lease_expires_at=NULL
     WHERE lifecycle.lifecycle_id=p_lifecycle_id;
    RETURN current_row.state;
END;
$$;

CREATE FUNCTION spyglass_subscription_lifecycle_stats(p_now timestamptz)
RETURNS TABLE(open bigint,due bigint,restricted bigint,termination_pending bigint,notices_due bigint)
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path=pg_catalog,public
AS $$
    SELECT
      (SELECT count(*) FROM public.account_subscription_lifecycles lifecycle WHERE lifecycle.state NOT IN ('recovered','closed')),
      (SELECT count(*) FROM public.account_subscription_lifecycles lifecycle WHERE lifecycle.state NOT IN ('recovered','closed') AND lifecycle.next_attempt_at<=p_now),
      (SELECT count(*) FROM public.account_subscription_lifecycles lifecycle WHERE lifecycle.state='restricted'),
      (SELECT count(*) FROM public.account_subscription_lifecycles lifecycle WHERE lifecycle.state='termination_pending'),
      (SELECT count(*) FROM public.account_subscription_lifecycle_notices notice WHERE notice.state IN ('scheduled','failed') AND notice.next_attempt_at<=p_now)
$$;

REVOKE ALL ON TABLE account_subscription_lifecycles,account_subscription_lifecycle_events,
    account_subscription_lifecycle_notices,account_subscription_termination_jobs FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_reject_subscription_lifecycle_evidence_mutation() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_erase_subscription_lifecycle_graph() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_schedule_subscription_lifecycle_notices(uuid,text,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_recover_subscription_lifecycle(uuid,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_project_subscription_lifecycle(uuid,text,text,timestamptz,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_claim_subscription_lifecycle(timestamptz,bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_advance_subscription_lifecycle(uuid,uuid,timestamptz,bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_subscription_lifecycle_stats(timestamptz) FROM PUBLIC;

COMMIT;
