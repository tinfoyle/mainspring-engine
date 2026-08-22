BEGIN;

CREATE TABLE spyglass.schedules (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 160),
    timezone text NOT NULL CHECK (char_length(timezone) BETWEEN 1 AND 200 AND timezone<>'Local'),
    recurrence jsonb NOT NULL CHECK (jsonb_typeof(recurrence)='object' AND octet_length(recurrence::text)<=4096),
    missed_run_policy text NOT NULL CHECK (missed_run_policy IN ('skip','catch_up_one')),
    execution_template jsonb NOT NULL CHECK (jsonb_typeof(execution_template)='object' AND octet_length(execution_template::text)<=131072),
    state text NOT NULL CHECK (state IN ('active','paused')),
    version bigint NOT NULL CHECK (version>0),
    next_run_at timestamptz,
    created_by_user_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    CHECK ((state='active' AND next_run_at IS NOT NULL) OR (state='paused' AND next_run_at IS NULL))
);

CREATE TABLE spyglass.schedule_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    schedule_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('created','paused','resumed','occurrence_dispatched')),
    from_version bigint NOT NULL CHECK (from_version>=0),
    to_version bigint NOT NULL CHECK (to_version=from_version+1),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user','workload')),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 200),
    reason text NOT NULL CHECK (char_length(reason)<=500),
    correlation_id text NOT NULL CHECK (char_length(correlation_id) BETWEEN 1 AND 200),
    redacted_payload jsonb NOT NULL CHECK (jsonb_typeof(redacted_payload)='object' AND octet_length(redacted_payload::text)<=4096),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,schedule_id) REFERENCES spyglass.schedules(account_id,id) ON DELETE CASCADE
);

-- Identifier/time-only cross-Account coordination. No customer definition
-- content is copied here and no table grant is issued to a worker.
CREATE TABLE spyglass.schedule_dispatch_queue (
    account_id uuid NOT NULL,
    schedule_id uuid NOT NULL,
    schedule_version bigint NOT NULL CHECK (schedule_version>0),
    scheduled_for timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('pending','leased','retry','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at timestamptz NOT NULL,
    lease_id uuid,
    lease_expires_at timestamptz,
    last_error_code text CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,schedule_id),
    FOREIGN KEY (account_id,schedule_id) REFERENCES spyglass.schedules(account_id,id) ON DELETE CASCADE,
    CHECK ((state='leased' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR
           (state<>'leased' AND lease_id IS NULL AND lease_expires_at IS NULL))
);

CREATE INDEX schedules_list ON spyglass.schedules(account_id,state,updated_at,id);
CREATE INDEX schedule_events_schedule ON spyglass.schedule_events(account_id,schedule_id,occurred_at,id);
CREATE INDEX schedule_dispatch_queue_ready ON spyglass.schedule_dispatch_queue(next_attempt_at,scheduled_for,account_id,schedule_id)
    WHERE state IN ('pending','retry','leased');

ALTER TABLE spyglass.schedules ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.schedules FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.schedule_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.schedule_events FORCE ROW LEVEL SECURITY;
CREATE POLICY schedules_isolation ON spyglass.schedules USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY schedule_events_isolation ON spyglass.schedule_events USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.reject_schedule_event_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' AND pg_trigger_depth()>1 THEN RETURN OLD; END IF;
    IF TG_OP='DELETE' AND current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE') THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Schedule events are immutable';
END;
$$;

CREATE FUNCTION spyglass.protect_schedule_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR
       NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Schedule identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.refresh_schedule_dispatch_queue() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
    IF NEW.state='paused' THEN
        DELETE FROM spyglass.schedule_dispatch_queue WHERE account_id=NEW.account_id AND schedule_id=NEW.id;
        RETURN NEW;
    END IF;
    INSERT INTO spyglass.schedule_dispatch_queue
      (account_id,schedule_id,schedule_version,scheduled_for,state,attempt_count,next_attempt_at,created_at,updated_at)
    VALUES (NEW.account_id,NEW.id,NEW.version,NEW.next_run_at,'pending',0,NEW.next_run_at,NEW.created_at,NEW.updated_at)
    ON CONFLICT (account_id,schedule_id) DO UPDATE SET schedule_version=EXCLUDED.schedule_version,
      scheduled_for=EXCLUDED.scheduled_for,state='pending',attempt_count=0,next_attempt_at=EXCLUDED.next_attempt_at,
      lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL,updated_at=EXCLUDED.updated_at;
    RETURN NEW;
END;
$$;

CREATE TRIGGER schedules_identity_guard BEFORE UPDATE ON spyglass.schedules FOR EACH ROW EXECUTE FUNCTION spyglass.protect_schedule_identity();
CREATE TRIGGER schedule_events_immutable BEFORE UPDATE OR DELETE ON spyglass.schedule_events FOR EACH ROW EXECUTE FUNCTION spyglass.reject_schedule_event_change();
CREATE TRIGGER schedules_refresh_dispatch AFTER INSERT OR UPDATE OF state,version,next_run_at ON spyglass.schedules
    FOR EACH ROW EXECUTE FUNCTION spyglass.refresh_schedule_dispatch_queue();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.schedules
    FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.schedule_events
    FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.schedule_dispatch_queue
    FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

CREATE FUNCTION public.spyglass_claim_schedule_dispatch(p_lease_id uuid,p_now timestamptz,p_lease_seconds integer)
RETURNS TABLE(account_id uuid,schedule_id uuid,schedule_version bigint,scheduled_for timestamptz,lease_id uuid,attempt_count integer)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE candidate record; claimed_attempt integer;
BEGIN
    IF p_lease_id IS NULL OR p_now IS NULL OR p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Schedule dispatch claim';
    END IF;
    SELECT q.account_id,q.schedule_id INTO candidate FROM spyglass.schedule_dispatch_queue q
    WHERE (q.state IN ('pending','retry') AND q.next_attempt_at<=p_now) OR (q.state='leased' AND q.lease_expires_at<=p_now)
    ORDER BY COALESCE(q.lease_expires_at,q.next_attempt_at),q.scheduled_for,q.account_id,q.schedule_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;
    UPDATE spyglass.schedule_dispatch_queue q SET state='leased',lease_id=p_lease_id,
      lease_expires_at=p_now+(p_lease_seconds*interval '1 second'),attempt_count=q.attempt_count+1,last_error_code=NULL,updated_at=p_now
    WHERE q.account_id=candidate.account_id AND q.schedule_id=candidate.schedule_id RETURNING q.attempt_count INTO claimed_attempt;
    RETURN QUERY SELECT q.account_id,q.schedule_id,q.schedule_version,q.scheduled_for,p_lease_id,claimed_attempt
      FROM spyglass.schedule_dispatch_queue q WHERE q.account_id=candidate.account_id AND q.schedule_id=candidate.schedule_id;
END;
$$;

CREATE FUNCTION public.spyglass_fail_schedule_dispatch(
    p_account_id uuid,p_schedule_id uuid,p_lease_id uuid,p_scheduled_for timestamptz,p_retry boolean,
    p_next_attempt_at timestamptz,p_error_code text,p_now timestamptz,p_max_attempts integer
) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE queue_row record; next_state text;
BEGIN
    IF p_account_id IS NULL OR p_schedule_id IS NULL OR p_lease_id IS NULL OR p_scheduled_for IS NULL OR p_retry IS NULL OR
       p_next_attempt_at IS NULL OR p_now IS NULL OR p_next_attempt_at<p_now OR p_error_code IS NULL OR
       p_error_code !~ '^[a-z][a-z0-9_]{0,99}$' OR p_max_attempts IS NULL OR p_max_attempts NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Schedule dispatch failure';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.schedule_dispatch_queue q
      WHERE q.account_id=p_account_id AND q.schedule_id=p_schedule_id FOR UPDATE;
    IF NOT FOUND OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.scheduled_for<>p_scheduled_for OR
       queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Schedule dispatch lease lost';
    END IF;
    next_state:=CASE WHEN p_retry AND queue_row.attempt_count<p_max_attempts THEN 'retry' ELSE 'dead_letter' END;
    UPDATE spyglass.schedule_dispatch_queue SET state=next_state,next_attempt_at=p_next_attempt_at,lease_id=NULL,
      lease_expires_at=NULL,last_error_code=p_error_code,updated_at=p_now
      WHERE account_id=p_account_id AND schedule_id=p_schedule_id;
    RETURN next_state;
END;
$$;

CREATE FUNCTION public.spyglass_schedule_dispatch_stats(p_now timestamptz)
RETURNS TABLE(pending bigint,ready bigint,leased bigint,retrying bigint,dead_letter bigint,oldest_ready_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
    SELECT count(*) FILTER (WHERE state='pending'),
      count(*) FILTER (WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now)),
      count(*) FILTER (WHERE state='leased' AND lease_expires_at>p_now),count(*) FILTER (WHERE state='retry'),
      count(*) FILTER (WHERE state='dead_letter'),
      min(COALESCE(lease_expires_at,next_attempt_at)) FILTER (WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now))
    FROM spyglass.schedule_dispatch_queue
$$;

CREATE FUNCTION spyglass.capture_schedule_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts jsonb; current_count bigint;
BEGIN
    IF current_setting('spyglass.erasure_request_id',true)<>'' AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
      counts:=COALESCE(NULLIF(current_setting('spyglass.schedule_erasure_counts',true),'')::jsonb,'{}'::jsonb);
      current_count:=COALESCE((counts->>TG_TABLE_NAME)::bigint,0)+1;
      PERFORM set_config('spyglass.schedule_erasure_counts',(counts||jsonb_build_object(TG_TABLE_NAME,current_count))::text,true);
    END IF;
    RETURN OLD;
END;
$$;
CREATE FUNCTION spyglass.add_schedule_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.schedule_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER schedules_erasure_count BEFORE DELETE ON spyglass.schedules FOR EACH ROW EXECUTE FUNCTION spyglass.capture_schedule_erasure_count();
CREATE TRIGGER schedule_events_erasure_count BEFORE DELETE ON spyglass.schedule_events FOR EACH ROW EXECUTE FUNCTION spyglass.capture_schedule_erasure_count();
CREATE TRIGGER schedule_dispatch_queue_erasure_count BEFORE DELETE ON spyglass.schedule_dispatch_queue FOR EACH ROW EXECUTE FUNCTION spyglass.capture_schedule_erasure_count();
CREATE TRIGGER account_erasure_schedule_counts BEFORE INSERT ON spyglass.account_erasure_tombstones FOR EACH ROW EXECUTE FUNCTION spyglass.add_schedule_erasure_counts();

REVOKE ALL ON FUNCTION spyglass.refresh_schedule_dispatch_queue() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_schedule_dispatch(uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_fail_schedule_dispatch(uuid,uuid,uuid,timestamptz,boolean,timestamptz,text,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_schedule_dispatch_stats(timestamptz) FROM PUBLIC;

COMMIT;
