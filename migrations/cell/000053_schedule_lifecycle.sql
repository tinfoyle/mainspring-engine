BEGIN;

-- The original cross-column checks were automatically named
-- schedules_check (updated_at) and schedules_check1 (state/next_run_at).
-- Replace both with explicit names so later lifecycle migrations do not
-- depend on PostgreSQL's generated-name ordering.
ALTER TABLE spyglass.schedules DROP CONSTRAINT schedules_check;
ALTER TABLE spyglass.schedules DROP CONSTRAINT schedules_check1;
ALTER TABLE spyglass.schedules DROP CONSTRAINT schedules_state_check;
ALTER TABLE spyglass.schedules ADD CONSTRAINT schedules_updated_at_check CHECK (updated_at>=created_at);
ALTER TABLE spyglass.schedules ADD CONSTRAINT schedules_state_check CHECK (state IN ('active','paused','deleted'));
ALTER TABLE spyglass.schedules ADD CONSTRAINT schedules_state_next_run_check
    CHECK ((state='active' AND next_run_at IS NOT NULL) OR (state IN ('paused','deleted') AND next_run_at IS NULL));

ALTER TABLE spyglass.schedule_events DROP CONSTRAINT schedule_events_event_type_check;
ALTER TABLE spyglass.schedule_events ADD CONSTRAINT schedule_events_event_type_check
    CHECK (event_type IN ('created','updated','paused','resumed','deleted','occurrence_dispatched','occurrence_skipped'));

CREATE OR REPLACE FUNCTION spyglass.refresh_schedule_dispatch_queue() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
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

REVOKE ALL ON FUNCTION spyglass.refresh_schedule_dispatch_queue() FROM PUBLIC;

COMMIT;
