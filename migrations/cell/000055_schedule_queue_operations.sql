BEGIN;

CREATE TABLE spyglass.schedule_queue_operator_events (
    batch_id uuid NOT NULL,
    event_sequence integer NOT NULL CHECK (event_sequence>=0),
    action text NOT NULL CHECK (action IN ('inspected','requeued')),
    queue_name text NOT NULL CHECK (queue_name IN ('recurring','trigger')),
    account_id uuid,
    schedule_id uuid,
    trigger_id uuid,
    actor text NOT NULL CHECK (char_length(actor) BETWEEN 1 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    previous_attempt_count integer CHECK (previous_attempt_count IS NULL OR previous_attempt_count>=0),
    previous_error_code text CHECK (previous_error_code IS NULL OR previous_error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (batch_id,event_sequence),
    FOREIGN KEY (account_id,schedule_id) REFERENCES spyglass.schedules(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,trigger_id) REFERENCES spyglass.schedule_triggers(account_id,id) ON DELETE CASCADE,
    CHECK ((account_id IS NULL AND schedule_id IS NULL AND trigger_id IS NULL) OR
           (account_id IS NOT NULL AND schedule_id IS NOT NULL AND
            ((queue_name='recurring' AND trigger_id IS NULL) OR (queue_name='trigger' AND trigger_id IS NOT NULL)))),
    CHECK (action<>'requeued' OR account_id IS NOT NULL)
);

CREATE INDEX schedule_queue_operator_events_target
    ON spyglass.schedule_queue_operator_events(account_id,schedule_id,trigger_id,created_at DESC);
CREATE INDEX schedule_queue_operator_events_time
    ON spyglass.schedule_queue_operator_events(created_at DESC,batch_id,event_sequence);

CREATE FUNCTION spyglass.reject_schedule_queue_operator_event_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text;
BEGIN
    IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
    SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner
    FROM pg_class c WHERE c.oid='spyglass.account_erasure_tombstones'::regclass;
    IF TG_OP='DELETE' AND OLD.account_id IS NOT NULL AND current_user=tombstone_owner AND
       current_setting('spyglass.erasure_request_id',true) IS NOT NULL AND
       current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        RETURN OLD;
    END IF;
    IF TG_OP='DELETE' AND OLD.account_id IS NOT NULL AND current_setting('spyglass.account_movement',true)='on' AND
       spyglass.account_movement_write_allowed(OLD.account_id,'DELETE') THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'Schedule queue operator events are immutable';
END;
$$;

CREATE TRIGGER schedule_queue_operator_events_immutable
BEFORE UPDATE OR DELETE ON spyglass.schedule_queue_operator_events
FOR EACH ROW EXECUTE FUNCTION spyglass.reject_schedule_queue_operator_event_change();
CREATE TRIGGER schedule_queue_operator_events_erasure_count
BEFORE DELETE ON spyglass.schedule_queue_operator_events
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_schedule_erasure_count();

CREATE FUNCTION public.spyglass_inspect_schedule_queue_dead_letters(
    p_batch_id uuid,p_queue_name text,p_actor text,p_reason text,p_environment text,p_limit integer
) RETURNS TABLE (
    queue_name text,account_id uuid,schedule_id uuid,trigger_id uuid,attempt_count integer,last_error_code text,
    occurrence_at timestamptz,created_at timestamptz,updated_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE target record; target_sequence integer:=0;
BEGIN
    IF p_batch_id IS NULL OR p_queue_name NOT IN ('recurring','trigger') OR
       p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR p_limit NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Schedule queue inspection';
    END IF;
    IF p_queue_name='recurring' THEN
        FOR target IN SELECT q.account_id,q.schedule_id,NULL::uuid AS trigger_id,q.attempt_count,q.last_error_code,
                q.scheduled_for AS occurrence_at,q.created_at,q.updated_at
            FROM spyglass.schedule_dispatch_queue q WHERE q.state='dead_letter'
            ORDER BY q.created_at,q.account_id,q.schedule_id LIMIT p_limit FOR SHARE
        LOOP
            target_sequence:=target_sequence+1;
            INSERT INTO spyglass.schedule_queue_operator_events
                (batch_id,event_sequence,action,queue_name,account_id,schedule_id,trigger_id,actor,reason,environment,previous_attempt_count,previous_error_code,created_at)
            VALUES (p_batch_id,target_sequence,'inspected',p_queue_name,target.account_id,target.schedule_id,target.trigger_id,
                btrim(p_actor),btrim(p_reason),p_environment,target.attempt_count,target.last_error_code,statement_timestamp());
            queue_name:=p_queue_name; account_id:=target.account_id; schedule_id:=target.schedule_id; trigger_id:=target.trigger_id;
            attempt_count:=target.attempt_count; last_error_code:=target.last_error_code; occurrence_at:=target.occurrence_at;
            created_at:=target.created_at; updated_at:=target.updated_at; RETURN NEXT;
        END LOOP;
    ELSE
        FOR target IN SELECT q.account_id,q.schedule_id,q.trigger_id,q.attempt_count,q.last_error_code,
                q.requested_for AS occurrence_at,q.created_at,q.updated_at
            FROM spyglass.schedule_trigger_queue q WHERE q.state='dead_letter'
            ORDER BY q.created_at,q.account_id,q.trigger_id LIMIT p_limit FOR SHARE
        LOOP
            target_sequence:=target_sequence+1;
            INSERT INTO spyglass.schedule_queue_operator_events
                (batch_id,event_sequence,action,queue_name,account_id,schedule_id,trigger_id,actor,reason,environment,previous_attempt_count,previous_error_code,created_at)
            VALUES (p_batch_id,target_sequence,'inspected',p_queue_name,target.account_id,target.schedule_id,target.trigger_id,
                btrim(p_actor),btrim(p_reason),p_environment,target.attempt_count,target.last_error_code,statement_timestamp());
            queue_name:=p_queue_name; account_id:=target.account_id; schedule_id:=target.schedule_id; trigger_id:=target.trigger_id;
            attempt_count:=target.attempt_count; last_error_code:=target.last_error_code; occurrence_at:=target.occurrence_at;
            created_at:=target.created_at; updated_at:=target.updated_at; RETURN NEXT;
        END LOOP;
    END IF;
    IF target_sequence=0 THEN
        INSERT INTO spyglass.schedule_queue_operator_events
            (batch_id,event_sequence,action,queue_name,actor,reason,environment,created_at)
        VALUES (p_batch_id,0,'inspected',p_queue_name,btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());
    END IF;
END;
$$;

CREATE FUNCTION public.spyglass_requeue_schedule_queue_dead_letter(
    p_batch_id uuid,p_queue_name text,p_account_id uuid,p_schedule_id uuid,p_trigger_id uuid,
    p_actor text,p_reason text,p_environment text
) RETURNS TABLE (
    queue_name text,account_id uuid,schedule_id uuid,trigger_id uuid,previous_attempt_count integer,previous_error_code text,
    occurrence_at timestamptz,created_at timestamptz,updated_at timestamptz,next_attempt_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE target record; requeued_at timestamptz:=statement_timestamp();
BEGIN
    IF p_batch_id IS NULL OR p_queue_name NOT IN ('recurring','trigger') OR p_account_id IS NULL OR p_schedule_id IS NULL OR
       (p_queue_name='recurring' AND p_trigger_id IS NOT NULL) OR (p_queue_name='trigger' AND p_trigger_id IS NULL) OR
       p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Schedule queue requeue';
    END IF;
    IF p_queue_name='recurring' THEN
        SELECT q.state,q.attempt_count,q.last_error_code,q.scheduled_for AS occurrence_at,q.created_at,q.updated_at
        INTO target FROM spyglass.schedule_dispatch_queue q
        WHERE q.account_id=p_account_id AND q.schedule_id=p_schedule_id FOR UPDATE;
    ELSE
        SELECT q.state,q.attempt_count,q.last_error_code,q.requested_for AS occurrence_at,q.created_at,q.updated_at
        INTO target FROM spyglass.schedule_trigger_queue q
        WHERE q.account_id=p_account_id AND q.schedule_id=p_schedule_id AND q.trigger_id=p_trigger_id FOR UPDATE;
    END IF;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Schedule queue job not found'; END IF;
    IF target.state<>'dead_letter' THEN RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Schedule queue job is not dead letter'; END IF;

    INSERT INTO spyglass.schedule_queue_operator_events
        (batch_id,event_sequence,action,queue_name,account_id,schedule_id,trigger_id,actor,reason,environment,previous_attempt_count,previous_error_code,created_at)
    VALUES (p_batch_id,0,'requeued',p_queue_name,p_account_id,p_schedule_id,p_trigger_id,btrim(p_actor),btrim(p_reason),
        p_environment,target.attempt_count,target.last_error_code,requeued_at);
    IF p_queue_name='recurring' THEN
        UPDATE spyglass.schedule_dispatch_queue AS q SET state='pending',attempt_count=0,next_attempt_at=requeued_at,
            lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL,updated_at=requeued_at
        WHERE q.account_id=p_account_id AND q.schedule_id=p_schedule_id;
    ELSE
        UPDATE spyglass.schedule_trigger_queue AS q SET state='pending',attempt_count=0,next_attempt_at=requeued_at,
            lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL,updated_at=requeued_at
        WHERE q.account_id=p_account_id AND q.schedule_id=p_schedule_id AND q.trigger_id=p_trigger_id;
    END IF;
    RETURN QUERY SELECT p_queue_name,p_account_id,p_schedule_id,p_trigger_id,target.attempt_count,target.last_error_code,
        target.occurrence_at,target.created_at,target.updated_at,requeued_at;
END;
$$;

REVOKE ALL ON FUNCTION spyglass.reject_schedule_queue_operator_event_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_inspect_schedule_queue_dead_letters(uuid,text,text,text,text,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_requeue_schedule_queue_dead_letter(uuid,text,uuid,uuid,uuid,text,text,text) FROM PUBLIC;

COMMIT;
