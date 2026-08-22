BEGIN;

CREATE TABLE spyglass.schedule_triggers (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    schedule_id uuid NOT NULL,
    schedule_version bigint NOT NULL CHECK (schedule_version>0),
    requested_by_user_id uuid NOT NULL,
    requested_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,schedule_id) REFERENCES spyglass.schedules(account_id,id) ON DELETE CASCADE
);

CREATE INDEX schedule_triggers_schedule ON spyglass.schedule_triggers(account_id,schedule_id,requested_at DESC,id DESC);
ALTER TABLE spyglass.schedule_triggers ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.schedule_triggers FORCE ROW LEVEL SECURITY;
CREATE POLICY schedule_triggers_isolation ON spyglass.schedule_triggers
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE TRIGGER schedule_triggers_immutable BEFORE UPDATE OR DELETE ON spyglass.schedule_triggers
    FOR EACH ROW EXECUTE FUNCTION spyglass.reject_schedule_event_change();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.schedule_triggers
    FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

CREATE TABLE spyglass.schedule_trigger_queue (
    account_id uuid NOT NULL,
    trigger_id uuid NOT NULL,
    schedule_id uuid NOT NULL,
    schedule_version bigint NOT NULL CHECK (schedule_version>0),
    requested_for timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('pending','leased','retry','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at timestamptz NOT NULL,
    lease_id uuid,
    lease_expires_at timestamptz,
    last_error_code text CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,trigger_id),
    FOREIGN KEY (account_id,trigger_id) REFERENCES spyglass.schedule_triggers(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,schedule_id) REFERENCES spyglass.schedules(account_id,id) ON DELETE CASCADE,
    CHECK ((state='leased' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR
           (state<>'leased' AND lease_id IS NULL AND lease_expires_at IS NULL))
);

CREATE INDEX schedule_trigger_queue_ready ON spyglass.schedule_trigger_queue(next_attempt_at,requested_for,account_id,trigger_id)
    WHERE state IN ('pending','retry','leased');
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.schedule_trigger_queue
    FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

ALTER TABLE spyglass.schedule_occurrences
    ADD COLUMN occurrence_kind text NOT NULL DEFAULT 'scheduled' CHECK (occurrence_kind IN ('scheduled','triggered')),
    ADD COLUMN trigger_id uuid,
    ADD COLUMN requested_by_user_id uuid;
ALTER TABLE spyglass.schedule_occurrences DROP CONSTRAINT schedule_occurrences_account_id_schedule_id_scheduled_for_key;
ALTER TABLE spyglass.schedule_occurrences ADD CONSTRAINT schedule_occurrences_trigger_fkey
    FOREIGN KEY (account_id,trigger_id) REFERENCES spyglass.schedule_triggers(account_id,id) ON DELETE CASCADE;
ALTER TABLE spyglass.schedule_occurrences ADD CONSTRAINT schedule_occurrences_kind_check
    CHECK ((occurrence_kind='scheduled' AND trigger_id IS NULL AND requested_by_user_id IS NULL) OR
           (occurrence_kind='triggered' AND trigger_id IS NOT NULL AND requested_by_user_id IS NOT NULL AND outcome='dispatched'));
CREATE UNIQUE INDEX schedule_occurrences_scheduled_unique ON spyglass.schedule_occurrences(account_id,schedule_id,scheduled_for)
    WHERE occurrence_kind='scheduled';
CREATE UNIQUE INDEX schedule_occurrences_trigger_unique ON spyglass.schedule_occurrences(account_id,trigger_id)
    WHERE occurrence_kind='triggered';

ALTER TABLE spyglass.schedule_events DROP CONSTRAINT schedule_events_event_type_check;
ALTER TABLE spyglass.schedule_events ADD CONSTRAINT schedule_events_event_type_check
    CHECK (event_type IN ('created','updated','paused','resumed','deleted','trigger_requested','trigger_dispatched','occurrence_dispatched','occurrence_skipped'));
ALTER TABLE spyglass.schedule_events DROP CONSTRAINT schedule_events_check;
ALTER TABLE spyglass.schedule_events ADD CONSTRAINT schedule_events_version_check
    CHECK ((event_type IN ('trigger_requested','trigger_dispatched') AND to_version=from_version) OR
           (event_type NOT IN ('trigger_requested','trigger_dispatched') AND to_version=from_version+1));

CREATE OR REPLACE FUNCTION spyglass.refresh_schedule_dispatch_queue() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
    DELETE FROM spyglass.schedule_trigger_queue
      WHERE account_id=NEW.account_id AND schedule_id=NEW.id
        AND (NEW.state<>'active' OR schedule_version<>NEW.version);
    IF NEW.state<>'active' THEN
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

CREATE FUNCTION public.spyglass_claim_schedule_trigger(p_lease_id uuid,p_now timestamptz,p_lease_seconds integer)
RETURNS TABLE(account_id uuid,trigger_id uuid,schedule_id uuid,schedule_version bigint,requested_for timestamptz,lease_id uuid,attempt_count integer)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE candidate record; claimed_attempt integer;
BEGIN
    IF p_lease_id IS NULL OR p_now IS NULL OR p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Schedule trigger claim';
    END IF;
    SELECT q.account_id,q.trigger_id INTO candidate FROM spyglass.schedule_trigger_queue q
    WHERE (q.state IN ('pending','retry') AND q.next_attempt_at<=p_now) OR (q.state='leased' AND q.lease_expires_at<=p_now)
    ORDER BY COALESCE(q.lease_expires_at,q.next_attempt_at),q.requested_for,q.account_id,q.trigger_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;
    UPDATE spyglass.schedule_trigger_queue q SET state='leased',lease_id=p_lease_id,
      lease_expires_at=p_now+(p_lease_seconds*interval '1 second'),attempt_count=q.attempt_count+1,last_error_code=NULL,updated_at=p_now
    WHERE q.account_id=candidate.account_id AND q.trigger_id=candidate.trigger_id RETURNING q.attempt_count INTO claimed_attempt;
    RETURN QUERY SELECT q.account_id,q.trigger_id,q.schedule_id,q.schedule_version,q.requested_for,p_lease_id,claimed_attempt
      FROM spyglass.schedule_trigger_queue q WHERE q.account_id=candidate.account_id AND q.trigger_id=candidate.trigger_id;
END;
$$;

CREATE FUNCTION public.spyglass_heartbeat_schedule_trigger(
    p_account_id uuid,p_trigger_id uuid,p_lease_id uuid,p_requested_for timestamptz,p_now timestamptz,p_lease_seconds integer
) RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE updated_rows bigint;
BEGIN
    IF p_account_id IS NULL OR p_trigger_id IS NULL OR p_lease_id IS NULL OR p_requested_for IS NULL OR p_now IS NULL OR
       p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Schedule trigger heartbeat';
    END IF;
    UPDATE spyglass.schedule_trigger_queue SET lease_expires_at=p_now+(p_lease_seconds*interval '1 second'),updated_at=p_now
      WHERE account_id=p_account_id AND trigger_id=p_trigger_id AND state='leased' AND lease_id=p_lease_id
        AND requested_for=p_requested_for AND lease_expires_at>=statement_timestamp();
    GET DIAGNOSTICS updated_rows=ROW_COUNT;
    RETURN updated_rows=1;
END;
$$;

CREATE FUNCTION public.spyglass_fail_schedule_trigger(
    p_account_id uuid,p_trigger_id uuid,p_lease_id uuid,p_requested_for timestamptz,p_retry boolean,
    p_next_attempt_at timestamptz,p_error_code text,p_now timestamptz,p_max_attempts integer
) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE queue_row record; next_state text;
BEGIN
    IF p_account_id IS NULL OR p_trigger_id IS NULL OR p_lease_id IS NULL OR p_requested_for IS NULL OR p_retry IS NULL OR
       p_next_attempt_at IS NULL OR p_now IS NULL OR p_next_attempt_at<p_now OR p_error_code IS NULL OR
       p_error_code !~ '^[a-z][a-z0-9_]{0,99}$' OR p_max_attempts IS NULL OR p_max_attempts NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Schedule trigger failure';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.schedule_trigger_queue q
      WHERE q.account_id=p_account_id AND q.trigger_id=p_trigger_id FOR UPDATE;
    IF NOT FOUND OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.requested_for<>p_requested_for OR
       queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Schedule trigger lease lost';
    END IF;
    next_state:=CASE WHEN p_retry AND queue_row.attempt_count<p_max_attempts THEN 'retry' ELSE 'dead_letter' END;
    UPDATE spyglass.schedule_trigger_queue SET state=next_state,next_attempt_at=p_next_attempt_at,lease_id=NULL,
      lease_expires_at=NULL,last_error_code=p_error_code,updated_at=p_now
      WHERE account_id=p_account_id AND trigger_id=p_trigger_id;
    RETURN next_state;
END;
$$;

CREATE FUNCTION public.spyglass_schedule_trigger_stats(p_now timestamptz)
RETURNS TABLE(pending bigint,ready bigint,leased bigint,retrying bigint,dead_letter bigint,oldest_ready_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
    SELECT count(*) FILTER (WHERE state='pending'),
      count(*) FILTER (WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now)),
      count(*) FILTER (WHERE state='leased' AND lease_expires_at>p_now),count(*) FILTER (WHERE state='retry'),
      count(*) FILTER (WHERE state='dead_letter'),
      min(COALESCE(lease_expires_at,next_attempt_at)) FILTER (WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now))
    FROM spyglass.schedule_trigger_queue
$$;

CREATE TRIGGER schedule_triggers_erasure_count BEFORE DELETE ON spyglass.schedule_triggers
    FOR EACH ROW EXECUTE FUNCTION spyglass.capture_schedule_erasure_count();
CREATE TRIGGER schedule_trigger_queue_erasure_count BEFORE DELETE ON spyglass.schedule_trigger_queue
    FOR EACH ROW EXECUTE FUNCTION spyglass.capture_schedule_erasure_count();

REVOKE ALL ON FUNCTION spyglass.refresh_schedule_dispatch_queue() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_schedule_trigger(uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_heartbeat_schedule_trigger(uuid,uuid,uuid,timestamptz,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_fail_schedule_trigger(uuid,uuid,uuid,timestamptz,boolean,timestamptz,text,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_schedule_trigger_stats(timestamptz) FROM PUBLIC;

COMMIT;
