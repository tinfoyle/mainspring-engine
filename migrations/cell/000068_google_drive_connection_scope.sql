BEGIN;

-- A Drive connection freezes only opaque canonical folder identities. Names,
-- file metadata, OAuth material and sync cursors belong to later capture and
-- credential boundaries and must not enter the customer-visible scope row.
ALTER TABLE spyglass.integration_connection_revisions
  ADD COLUMN drive_folder_ids text[] NOT NULL DEFAULT ARRAY[]::text[];

CREATE FUNCTION spyglass.valid_google_drive_folder_ids(p_values text[]) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE STRICT
SET search_path=pg_catalog
AS $$
DECLARE
  item text;
  prior text;
BEGIN
  IF cardinality(p_values) NOT BETWEEN 1 AND 50 OR array_position(p_values,NULL) IS NOT NULL THEN
    RETURN false;
  END IF;
  FOREACH item IN ARRAY p_values LOOP
    IF octet_length(item) NOT BETWEEN 1 AND 200 OR item !~ '^[A-Za-z0-9_-]+$' OR
       (prior IS NOT NULL AND (item COLLATE "C") <= (prior COLLATE "C")) THEN
      RETURN false;
    END IF;
    prior := item;
  END LOOP;
  RETURN true;
END;
$$;

ALTER TABLE spyglass.integration_connection_revisions
  DROP CONSTRAINT integration_connection_revisions_check,
  ADD CONSTRAINT integration_connection_revisions_check CHECK (
    (capabilities IN (ARRAY['email.read']::text[],ARRAY['email.send']::text[],ARRAY['email.read','email.send']::text[]) AND
     email_address ~ '^[^[:space:]@]+@[^[:space:]@]+$' AND
     (NOT capabilities @> ARRAY['email.send']::text[] OR octet_length(audience_reference) BETWEEN 1 AND 500) AND
     https_origin='' AND path_prefix='' AND cardinality(drive_folder_ids)=0) OR
    (capabilities=ARRAY['google_drive.read']::text[] AND email_address='' AND audience_reference='' AND
     https_origin='' AND path_prefix='' AND spyglass.valid_google_drive_folder_ids(drive_folder_ids)) OR
    (capabilities=ARRAY['web.publish']::text[] AND email_address='' AND audience_reference='' AND
     https_origin ~ '^https://[^/?#]+$' AND path_prefix ~ '^/([^/]|$)' AND path_prefix !~ '/\.\.?(/|$)' AND
     cardinality(drive_folder_ids)=0)
  );

CREATE OR REPLACE FUNCTION spyglass.validate_integration_revision_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE connection_row record; prior_revision bigint;
BEGIN
  IF current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(NEW.account_id,'INSERT') THEN RETURN NEW; END IF;
  SELECT state,current_revision,connector_kind INTO connection_row FROM spyglass.integration_connections
  WHERE account_id=NEW.account_id AND id=NEW.connection_id FOR UPDATE;
  IF NOT FOUND OR connection_row.state='revoked' THEN RAISE EXCEPTION 'Integration connection is unavailable'; END IF;
  IF (connection_row.connector_kind='email' AND NOT (NEW.capabilities <@ ARRAY['email.read','email.send']::text[])) OR
     (connection_row.connector_kind='google_drive' AND NEW.capabilities<>ARRAY['google_drive.read']::text[]) OR
     (connection_row.connector_kind='web_publish' AND NEW.capabilities<>ARRAY['web.publish']::text[]) OR
     connection_row.connector_kind='web_research' THEN
    RAISE EXCEPTION 'Integration connection capability does not match connector kind';
  END IF;
  SELECT max(revision) INTO prior_revision FROM spyglass.integration_connection_revisions WHERE account_id=NEW.account_id AND connection_id=NEW.connection_id;
  IF NEW.revision<>COALESCE(prior_revision,0)+1 OR NEW.revision NOT IN (connection_row.current_revision,connection_row.current_revision+1) THEN
    RAISE EXCEPTION 'Integration connection revision is not sequential';
  END IF;
  RETURN NEW;
END;
$$;

COMMIT;
