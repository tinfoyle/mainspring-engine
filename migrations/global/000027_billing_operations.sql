BEGIN;

CREATE TABLE billing_operator_events (
    batch_id uuid NOT NULL,
    event_sequence integer NOT NULL CHECK (event_sequence >= 0),
    action text NOT NULL CHECK (action IN ('inspected','event_replayed','subscription_refresh_queued')),
    target_kind text CHECK (target_kind IS NULL OR target_kind IN ('event','reconciliation')),
    target_id text CHECK (target_id IS NULL OR char_length(target_id) BETWEEN 5 AND 200),
    account_id uuid,
    provider_mode text NOT NULL CHECK (provider_mode IN ('test','live')),
    previous_state text,
    previous_attempt_count integer CHECK (previous_attempt_count IS NULL OR previous_attempt_count >= 0),
    previous_error_code text,
    actor text NOT NULL CHECK (char_length(actor) BETWEEN 3 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (batch_id,event_sequence),
    CHECK ((target_kind IS NULL) = (target_id IS NULL)),
    CHECK (action = 'inspected' OR target_id IS NOT NULL)
);
CREATE INDEX billing_operator_events_target ON billing_operator_events (target_kind,target_id,created_at DESC);
CREATE INDEX billing_operator_events_time ON billing_operator_events (created_at DESC,batch_id,event_sequence);

CREATE FUNCTION public.spyglass_reject_billing_operator_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Billing operator events are immutable';
END;
$$;
CREATE TRIGGER billing_operator_events_immutable
BEFORE UPDATE OR DELETE ON billing_operator_events
FOR EACH ROW EXECUTE FUNCTION public.spyglass_reject_billing_operator_event_mutation();

CREATE FUNCTION public.spyglass_inspect_billing_failures(
    p_batch_id uuid, p_actor text, p_reason text, p_environment text, p_mode text, p_limit integer
) RETURNS TABLE (
    target_kind text, target_id text, account_id text, provider_mode text, processing_state text,
    attempt_count integer, last_error_code text, next_attempt_at timestamptz, created_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE target record; target_sequence integer := 0;
BEGIN
    IF p_batch_id IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR p_mode NOT IN ('test','live') OR p_limit NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid billing inspection';
    END IF;
    FOR target IN
        SELECT candidate.* FROM (
            SELECT 'event'::text AS kind,e.provider_event_id AS id,COALESCE(e.account_id::text,'') AS account,
                   e.mode,e.processing_state AS state,e.attempt_count,e.last_error_code,e.next_attempt_at,e.created_at
            FROM public.billing_event_inbox e WHERE e.mode=p_mode AND e.processing_state='failed'
            UNION ALL
            SELECT 'reconciliation',q.provider_subscription_id,s.account_id::text,s.provider_mode,q.processing_state,
                   q.attempt_count,q.last_error_code,q.next_attempt_at,q.requested_at
            FROM public.billing_reconciliation_queue q
            JOIN public.subscriptions s ON s.provider='stripe' AND s.provider_subscription_id=q.provider_subscription_id
            WHERE s.provider_mode=p_mode AND q.processing_state='failed'
        ) candidate ORDER BY candidate.created_at,candidate.kind,candidate.id LIMIT p_limit
    LOOP
        target_sequence := target_sequence + 1;
        INSERT INTO public.billing_operator_events
            (batch_id,event_sequence,action,target_kind,target_id,account_id,provider_mode,previous_state,
             previous_attempt_count,previous_error_code,actor,reason,environment,created_at)
        VALUES (p_batch_id,target_sequence,'inspected',target.kind,target.id,NULLIF(target.account,'')::uuid,p_mode,
                target.state,target.attempt_count,target.last_error_code,btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());
        target_kind:=target.kind; target_id:=target.id; account_id:=target.account; provider_mode:=target.mode;
        processing_state:=target.state; attempt_count:=target.attempt_count; last_error_code:=target.last_error_code;
        next_attempt_at:=target.next_attempt_at; created_at:=target.created_at; RETURN NEXT;
    END LOOP;
    IF target_sequence=0 THEN
        INSERT INTO public.billing_operator_events
            (batch_id,event_sequence,action,provider_mode,actor,reason,environment,created_at)
        VALUES (p_batch_id,0,'inspected',p_mode,btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());
    END IF;
END;
$$;

CREATE FUNCTION public.spyglass_replay_billing_event(
    p_batch_id uuid, p_event_id text, p_actor text, p_reason text, p_environment text, p_mode text
) RETURNS TABLE (
    target_kind text, target_id text, account_id text, provider_mode text, processing_state text,
    attempt_count integer, last_error_code text, next_attempt_at timestamptz, created_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE target record; now_at timestamptz := statement_timestamp();
BEGIN
    IF p_batch_id IS NULL OR p_event_id !~ '^evt_[^[:space:]]+$' OR char_length(p_event_id)>200 OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR p_mode NOT IN ('test','live') THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid billing event replay';
    END IF;
    SELECT e.account_id,e.processing_state,e.attempt_count,e.last_error_code,e.created_at INTO target
    FROM public.billing_event_inbox e WHERE e.provider_event_id=p_event_id AND e.mode=p_mode AND e.signature_verified_at IS NOT NULL FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='billing event not found'; END IF;
    IF target.processing_state NOT IN ('processed','failed') THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='billing event is not replayable';
    END IF;
    INSERT INTO public.billing_operator_events
        (batch_id,event_sequence,action,target_kind,target_id,account_id,provider_mode,previous_state,
         previous_attempt_count,previous_error_code,actor,reason,environment,created_at)
    VALUES (p_batch_id,0,'event_replayed','event',p_event_id,target.account_id,p_mode,target.processing_state,
            target.attempt_count,target.last_error_code,btrim(p_actor),btrim(p_reason),p_environment,now_at);
    UPDATE public.billing_event_inbox SET processing_state='accepted',next_attempt_at=now_at,lease_expires_at=NULL,
        processed_at=NULL,last_error_code=NULL WHERE provider_event_id=p_event_id;
    RETURN QUERY SELECT 'event'::text,p_event_id,COALESCE(target.account_id::text,''),p_mode,'accepted'::text,
        target.attempt_count,target.last_error_code,now_at,target.created_at;
END;
$$;

CREATE FUNCTION public.spyglass_queue_billing_subscription_refresh(
    p_batch_id uuid, p_subscription_id text, p_actor text, p_reason text, p_environment text, p_mode text
) RETURNS TABLE (
    target_kind text, target_id text, account_id text, provider_mode text, processing_state text,
    attempt_count integer, last_error_code text, next_attempt_at timestamptz, created_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE target record; previous_state text; previous_attempt_count integer; previous_error_code text; now_at timestamptz := statement_timestamp();
BEGIN
    IF p_batch_id IS NULL OR p_subscription_id !~ '^sub_[^[:space:]]+$' OR char_length(p_subscription_id)>200 OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR p_mode NOT IN ('test','live') THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid billing subscription refresh';
    END IF;
    SELECT s.account_id,s.created_at INTO target FROM public.subscriptions s
    WHERE s.provider='stripe' AND s.provider_mode=p_mode AND s.provider_subscription_id=p_subscription_id FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='billing subscription not found'; END IF;
    SELECT q.processing_state,q.attempt_count,q.last_error_code INTO previous_state,previous_attempt_count,previous_error_code
    FROM public.billing_reconciliation_queue q WHERE q.provider_subscription_id=p_subscription_id FOR UPDATE;
    INSERT INTO public.billing_operator_events
        (batch_id,event_sequence,action,target_kind,target_id,account_id,provider_mode,previous_state,
         previous_attempt_count,previous_error_code,actor,reason,environment,created_at)
    VALUES (p_batch_id,0,'subscription_refresh_queued','reconciliation',p_subscription_id,target.account_id,p_mode,
            previous_state,previous_attempt_count,previous_error_code,btrim(p_actor),btrim(p_reason),p_environment,now_at);
    INSERT INTO public.billing_reconciliation_queue (provider_subscription_id,reason,requested_at,next_attempt_at)
    VALUES (p_subscription_id,btrim(p_reason),now_at,now_at)
    ON CONFLICT (provider_subscription_id) DO UPDATE SET reason=EXCLUDED.reason,requested_at=EXCLUDED.requested_at,
        next_attempt_at=LEAST(public.billing_reconciliation_queue.next_attempt_at,EXCLUDED.next_attempt_at),
        processing_state='pending',completed_at=NULL,lease_expires_at=NULL,last_error_code=NULL;
    RETURN QUERY SELECT 'reconciliation'::text,p_subscription_id,target.account_id::text,p_mode,'pending'::text,
        COALESCE(previous_attempt_count,0),previous_error_code,now_at,target.created_at;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_reject_billing_operator_event_mutation() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_inspect_billing_failures(uuid,text,text,text,text,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_billing_event(uuid,text,text,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_queue_billing_subscription_refresh(uuid,text,text,text,text,text) FROM PUBLIC;

COMMIT;
