BEGIN;

-- Operator access to release failures is deliberately separate from customer
-- Work data. Audit rows remain after technical queue retention removes jobs.
CREATE TABLE spyglass.work_capacity_release_operator_events (
    batch_id uuid NOT NULL,
    event_sequence integer NOT NULL CHECK (event_sequence >= 0),
    action text NOT NULL CHECK (action IN ('inspected','requeued')),
    account_id uuid,
    work_item_id uuid,
    reservation_id uuid,
    actor text NOT NULL CHECK (char_length(actor) BETWEEN 1 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (char_length(environment) BETWEEN 1 AND 100 AND environment ~ '^[a-z][a-z0-9-]*$'),
    previous_attempt_count integer CHECK (previous_attempt_count IS NULL OR previous_attempt_count >= 0),
    previous_error_code text CHECK (previous_error_code IS NULL OR char_length(previous_error_code) BETWEEN 1 AND 100),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (batch_id,event_sequence),
    CHECK ((account_id IS NULL AND work_item_id IS NULL AND reservation_id IS NULL) OR
           (account_id IS NOT NULL AND work_item_id IS NOT NULL AND reservation_id IS NOT NULL)),
    CHECK (action <> 'requeued' OR account_id IS NOT NULL)
);
CREATE INDEX work_capacity_release_operator_events_target
    ON spyglass.work_capacity_release_operator_events (account_id,work_item_id,reservation_id,created_at DESC);
CREATE INDEX work_capacity_release_operator_events_time
    ON spyglass.work_capacity_release_operator_events (created_at DESC,batch_id,event_sequence);

CREATE FUNCTION spyglass.reject_work_capacity_release_operator_event_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Work capacity release operator events are immutable';
END;
$$;

CREATE TRIGGER work_capacity_release_operator_events_immutable
BEFORE UPDATE OR DELETE ON spyglass.work_capacity_release_operator_events
FOR EACH ROW EXECUTE FUNCTION spyglass.reject_work_capacity_release_operator_event_change();

CREATE FUNCTION public.spyglass_inspect_work_capacity_release_dead_letters(
    p_batch_id uuid,
    p_actor text,
    p_reason text,
    p_environment text,
    p_limit integer
) RETURNS TABLE (
    account_id uuid,
    work_item_id uuid,
    reservation_id uuid,
    attempt_count integer,
    last_error_code text,
    queued_at timestamptz,
    last_attempt_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    target record;
    target_sequence integer := 0;
BEGIN
    IF p_batch_id IS NULL OR p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR
       p_limit NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid Work release inspection';
    END IF;

    FOR target IN
        SELECT q.account_id,q.work_item_id,q.reservation_id,q.attempt_count,q.last_error_code,q.queued_at,q.last_attempt_at
        FROM spyglass.work_capacity_release_queue q
        WHERE q.processing_state = 'dead_letter'
        ORDER BY q.queued_at,q.account_id,q.work_item_id,q.reservation_id
        LIMIT p_limit
        FOR SHARE
    LOOP
        target_sequence := target_sequence + 1;
        INSERT INTO spyglass.work_capacity_release_operator_events
            (batch_id,event_sequence,action,account_id,work_item_id,reservation_id,actor,reason,environment,
             previous_attempt_count,previous_error_code,created_at)
        VALUES
            (p_batch_id,target_sequence,'inspected',target.account_id,target.work_item_id,target.reservation_id,
             btrim(p_actor),btrim(p_reason),p_environment,target.attempt_count,target.last_error_code,statement_timestamp());
        account_id := target.account_id;
        work_item_id := target.work_item_id;
        reservation_id := target.reservation_id;
        attempt_count := target.attempt_count;
        last_error_code := target.last_error_code;
        queued_at := target.queued_at;
        last_attempt_at := target.last_attempt_at;
        RETURN NEXT;
    END LOOP;

    IF target_sequence = 0 THEN
        INSERT INTO spyglass.work_capacity_release_operator_events
            (batch_id,event_sequence,action,actor,reason,environment,created_at)
        VALUES (p_batch_id,0,'inspected',btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());
    END IF;
END;
$$;

CREATE FUNCTION public.spyglass_requeue_work_capacity_release_dead_letter(
    p_batch_id uuid,
    p_account_id uuid,
    p_work_item_id uuid,
    p_reservation_id uuid,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS TABLE (
    account_id uuid,
    work_item_id uuid,
    reservation_id uuid,
    previous_attempt_count integer,
    previous_error_code text,
    queued_at timestamptz,
    last_attempt_at timestamptz,
    next_attempt_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    target record;
BEGIN
    IF p_batch_id IS NULL OR p_account_id IS NULL OR p_work_item_id IS NULL OR p_reservation_id IS NULL OR
       p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid Work release requeue';
    END IF;

    SELECT q.processing_state,q.attempt_count,q.last_error_code,q.queued_at,q.last_attempt_at
    INTO target
    FROM spyglass.work_capacity_release_queue q
    WHERE q.account_id=p_account_id AND q.work_item_id=p_work_item_id AND q.reservation_id=p_reservation_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = 'P0002', MESSAGE = 'Work release job not found';
    END IF;
    IF target.processing_state <> 'dead_letter' THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'Work release job is not dead letter';
    END IF;

    INSERT INTO spyglass.work_capacity_release_operator_events
        (batch_id,event_sequence,action,account_id,work_item_id,reservation_id,actor,reason,environment,
         previous_attempt_count,previous_error_code,created_at)
    VALUES
        (p_batch_id,0,'requeued',p_account_id,p_work_item_id,p_reservation_id,btrim(p_actor),btrim(p_reason),
         p_environment,target.attempt_count,target.last_error_code,statement_timestamp());

    UPDATE spyglass.work_capacity_release_queue AS q SET
        processing_state='pending',attempt_count=0,next_attempt_at=statement_timestamp(),
        lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL,completed_at=NULL
    WHERE q.account_id=p_account_id AND q.work_item_id=p_work_item_id AND q.reservation_id=p_reservation_id;

    RETURN QUERY SELECT p_account_id,p_work_item_id,p_reservation_id,target.attempt_count,target.last_error_code,
                        target.queued_at,target.last_attempt_at,statement_timestamp();
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_inspect_work_capacity_release_dead_letters(uuid,text,text,text,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_requeue_work_capacity_release_dead_letter(uuid,uuid,uuid,uuid,text,text,text) FROM PUBLIC;

COMMIT;
