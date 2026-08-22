BEGIN;

ALTER TABLE spyglass.schedule_events DROP CONSTRAINT schedule_events_event_type_check;
ALTER TABLE spyglass.schedule_events ADD CONSTRAINT schedule_events_event_type_check
    CHECK (event_type IN ('created','paused','resumed','occurrence_dispatched','occurrence_skipped'));

CREATE TABLE spyglass.schedule_occurrences (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    schedule_id uuid NOT NULL,
    schedule_version bigint NOT NULL CHECK (schedule_version>0),
    scheduled_for timestamptz NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('dispatched','skipped')),
    run_id uuid,
    conversation_id uuid,
    created_by_user_id uuid NOT NULL,
    initiated_by_kind text NOT NULL CHECK (initiated_by_kind='workload'),
    initiated_by_id text NOT NULL CHECK (initiated_by_id='schedule-execution-worker'),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,schedule_id,scheduled_for),
    FOREIGN KEY (account_id,schedule_id) REFERENCES spyglass.schedules(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,run_id) REFERENCES spyglass.agent_runs(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,conversation_id) REFERENCES spyglass.agent_conversations(account_id,id) ON DELETE CASCADE,
    CHECK ((outcome='dispatched' AND run_id IS NOT NULL AND conversation_id IS NOT NULL) OR
           (outcome='skipped' AND run_id IS NULL AND conversation_id IS NULL))
);

CREATE INDEX schedule_occurrences_schedule ON spyglass.schedule_occurrences(account_id,schedule_id,scheduled_for DESC,id DESC);

ALTER TABLE spyglass.schedule_occurrences ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.schedule_occurrences FORCE ROW LEVEL SECURITY;
CREATE POLICY schedule_occurrences_isolation ON spyglass.schedule_occurrences
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE TRIGGER schedule_occurrences_immutable BEFORE UPDATE OR DELETE ON spyglass.schedule_occurrences
    FOR EACH ROW EXECUTE FUNCTION spyglass.reject_schedule_event_change();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.schedule_occurrences
    FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

CREATE FUNCTION public.spyglass_heartbeat_schedule_dispatch(
    p_account_id uuid,p_schedule_id uuid,p_lease_id uuid,p_scheduled_for timestamptz,p_now timestamptz,p_lease_seconds integer
) RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE updated_rows bigint;
BEGIN
    IF p_account_id IS NULL OR p_schedule_id IS NULL OR p_lease_id IS NULL OR p_scheduled_for IS NULL OR p_now IS NULL OR
       p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Schedule dispatch heartbeat';
    END IF;
    UPDATE spyglass.schedule_dispatch_queue SET lease_expires_at=p_now+(p_lease_seconds*interval '1 second'),updated_at=p_now
      WHERE account_id=p_account_id AND schedule_id=p_schedule_id AND state='leased' AND lease_id=p_lease_id
        AND scheduled_for=p_scheduled_for AND lease_expires_at>=statement_timestamp();
    GET DIAGNOSTICS updated_rows=ROW_COUNT;
    RETURN updated_rows=1;
END;
$$;

CREATE TRIGGER schedule_occurrences_erasure_count BEFORE DELETE ON spyglass.schedule_occurrences
    FOR EACH ROW EXECUTE FUNCTION spyglass.capture_schedule_erasure_count();

REVOKE ALL ON FUNCTION public.spyglass_heartbeat_schedule_dispatch(uuid,uuid,uuid,timestamptz,timestamptz,integer) FROM PUBLIC;

COMMIT;
