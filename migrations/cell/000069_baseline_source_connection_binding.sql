BEGIN;

-- A source grant is useful only when its declared read scope is authorized by
-- the currently active Integration connection. The grant remains the narrower
-- immutable authority; connector revision/credential changes are rechecked by
-- capture workers rather than silently widening it.
ALTER TABLE spyglass.baseline_source_grants
  ADD CONSTRAINT baseline_google_drive_folders_canonical CHECK (
    source_kind<>'google_drive' OR spyglass.valid_google_drive_folder_ids(folders)
  );

CREATE FUNCTION spyglass.validate_baseline_source_connection() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE connection_row record; revision_row record;
BEGIN
  IF current_setting('spyglass.account_movement',true)='on' AND
     spyglass.account_movement_write_allowed(NEW.account_id,'INSERT') THEN
    RETURN NEW;
  END IF;
  SELECT state,connector_kind,current_revision INTO connection_row
  FROM spyglass.integration_connections
  WHERE account_id=NEW.account_id AND id=NEW.connection_id FOR SHARE;
  IF NOT FOUND OR connection_row.state<>'active' THEN
    RAISE EXCEPTION 'Baseline source connection is unavailable';
  END IF;
  SELECT capabilities,drive_folder_ids INTO revision_row
  FROM spyglass.integration_connection_revisions
  WHERE account_id=NEW.account_id AND connection_id=NEW.connection_id AND revision=connection_row.current_revision FOR SHARE;
  IF NOT FOUND OR
     (NEW.source_kind='email' AND
      (connection_row.connector_kind<>'email' OR NOT revision_row.capabilities @> ARRAY['email.read']::text[])) OR
     (NEW.source_kind='google_drive' AND
      (connection_row.connector_kind<>'google_drive' OR revision_row.capabilities<>ARRAY['google_drive.read']::text[] OR
       NOT NEW.folders <@ revision_row.drive_folder_ids)) THEN
    RAISE EXCEPTION 'Baseline source scope is not authorized by the Integration connection';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER baseline_source_connection_binding
BEFORE INSERT ON spyglass.baseline_source_grants
FOR EACH ROW EXECUTE FUNCTION spyglass.validate_baseline_source_connection();

REVOKE ALL ON FUNCTION spyglass.validate_baseline_source_connection() FROM PUBLIC;

COMMIT;
