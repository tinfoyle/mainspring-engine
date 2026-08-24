BEGIN;

-- A web-research connection is intentionally one exact HTTPS origin and one
-- canonical path subtree. Broader research is represented by multiple
-- independently revocable connections rather than an arbitrary-URL grant.
ALTER TABLE spyglass.integration_connection_revisions
  DROP CONSTRAINT integration_connection_revisions_check,
  ADD CONSTRAINT integration_connection_revisions_check CHECK (
    (capabilities IN (ARRAY['email.read']::text[],ARRAY['email.send']::text[],ARRAY['email.read','email.send']::text[]) AND
     email_address ~ '^[^[:space:]@]+@[^[:space:]@]+$' AND
     (NOT capabilities @> ARRAY['email.send']::text[] OR octet_length(audience_reference) BETWEEN 1 AND 500) AND
     https_origin='' AND path_prefix='' AND cardinality(drive_folder_ids)=0) OR
    (capabilities=ARRAY['google_drive.read']::text[] AND email_address='' AND audience_reference='' AND
     https_origin='' AND path_prefix='' AND spyglass.valid_google_drive_folder_ids(drive_folder_ids)) OR
    (capabilities IN (ARRAY['web.research']::text[],ARRAY['web.publish']::text[]) AND email_address='' AND audience_reference='' AND
     https_origin ~ '^https://[^/?#:@]+$' AND path_prefix ~ '^/([^/]|$)' AND path_prefix !~ '/\.\.?(/|$)' AND path_prefix !~ '\\' AND
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
     (connection_row.connector_kind='web_research' AND NEW.capabilities<>ARRAY['web.research']::text[]) OR
     (connection_row.connector_kind='web_publish' AND NEW.capabilities<>ARRAY['web.publish']::text[]) THEN
    RAISE EXCEPTION 'Integration connection capability does not match connector kind';
  END IF;
  SELECT max(revision) INTO prior_revision FROM spyglass.integration_connection_revisions WHERE account_id=NEW.account_id AND connection_id=NEW.connection_id;
  IF NEW.revision<>COALESCE(prior_revision,0)+1 OR NEW.revision NOT IN (connection_row.current_revision,connection_row.current_revision+1) THEN
    RAISE EXCEPTION 'Integration connection revision is not sequential';
  END IF;
  RETURN NEW;
END;
$$;

DROP FUNCTION public.spyglass_claim_integration_health_probe(uuid,timestamptz,timestamptz);
CREATE FUNCTION public.spyglass_claim_integration_health_probe(
  p_probe_id uuid,p_now timestamptz,p_lease_expires_at timestamptz
) RETURNS TABLE (
  account_id uuid,probe_id uuid,connection_id uuid,connection_revision_id uuid,connection_revision bigint,
  connector_kind text,capabilities text[],email_address text,audience_reference text,https_origin text,path_prefix text,
  drive_folder_ids text[],credential_id uuid,credential_generation bigint,credential_provider text,
  credential_reference_sha256 bytea,lease_expires_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE queue_row record; connection_row record; revision_row record; credential_row record;
BEGIN
  IF p_probe_id IS NULL OR p_now IS NULL OR p_lease_expires_at IS NULL OR p_lease_expires_at<=p_now OR
     p_lease_expires_at>p_now+interval '5 minutes' THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Integration health probe claim';
  END IF;
  SELECT queue.account_id,queue.connection_id INTO queue_row
  FROM spyglass.integration_health_probe_queue queue
  WHERE queue.available_at<=p_now AND (queue.lease_expires_at IS NULL OR queue.lease_expires_at<=p_now)
  ORDER BY queue.available_at,queue.connection_id FOR UPDATE SKIP LOCKED LIMIT 1;
  IF NOT FOUND THEN RETURN; END IF;
  PERFORM set_config('app.account_id',queue_row.account_id::text,true);
  PERFORM 1 FROM spyglass.account_namespaces namespace
    WHERE namespace.account_id=queue_row.account_id AND namespace.state='active';
  IF NOT FOUND THEN
    UPDATE spyglass.integration_health_probe_queue probe_queue SET available_at=p_now+interval '5 minutes',lease_id=NULL,
      lease_expires_at=NULL,updated_at=p_now WHERE probe_queue.account_id=queue_row.account_id AND probe_queue.connection_id=queue_row.connection_id;
    RETURN;
  END IF;
  SELECT connection.id,connection.state,connection.connector_kind,connection.current_revision,connection.credential_id,
    connection.credential_generation INTO connection_row
  FROM spyglass.integration_connections connection
  WHERE connection.account_id=queue_row.account_id AND connection.id=queue_row.connection_id FOR SHARE;
  IF NOT FOUND OR connection_row.state<>'active' THEN
    DELETE FROM spyglass.integration_health_probe_queue probe_queue WHERE probe_queue.account_id=queue_row.account_id AND probe_queue.connection_id=queue_row.connection_id;
    RETURN;
  END IF;
  SELECT revision.id,revision.revision,revision.capabilities,revision.email_address,revision.audience_reference,
    revision.https_origin,revision.path_prefix,revision.drive_folder_ids INTO revision_row
  FROM spyglass.integration_connection_revisions revision
  WHERE revision.account_id=queue_row.account_id AND revision.connection_id=queue_row.connection_id AND
    revision.revision=connection_row.current_revision FOR SHARE;
  SELECT credential.id,credential.generation,credential.provider,credential.reference_sha256,credential.state,credential.expires_at
    INTO credential_row
  FROM spyglass.integration_credentials credential
  WHERE credential.account_id=queue_row.account_id AND credential.connection_id=queue_row.connection_id AND
    credential.id=connection_row.credential_id AND credential.generation=connection_row.credential_generation FOR SHARE;
  IF revision_row.id IS NULL OR credential_row.id IS NULL OR credential_row.state<>'active' OR
     (credential_row.expires_at IS NOT NULL AND credential_row.expires_at<=p_now) OR NOT (
       (connection_row.connector_kind='email' AND
        (revision_row.capabilities @> ARRAY['email.read']::text[] OR revision_row.capabilities @> ARRAY['email.send']::text[])) OR
       (connection_row.connector_kind='google_drive' AND revision_row.capabilities=ARRAY['google_drive.read']::text[] AND
        cardinality(revision_row.drive_folder_ids) BETWEEN 1 AND 50) OR
       (connection_row.connector_kind='web_research' AND revision_row.capabilities=ARRAY['web.research']::text[] AND
        revision_row.https_origin<>'' AND revision_row.path_prefix<>'') OR
       (connection_row.connector_kind='web_publish' AND revision_row.capabilities=ARRAY['web.publish']::text[])
     ) THEN
    DELETE FROM spyglass.integration_health_probe_queue probe_queue WHERE probe_queue.account_id=queue_row.account_id AND probe_queue.connection_id=queue_row.connection_id;
    RETURN;
  END IF;
  UPDATE spyglass.integration_health_probe_queue probe_queue SET lease_id=p_probe_id,lease_expires_at=p_lease_expires_at,updated_at=p_now
  WHERE probe_queue.account_id=queue_row.account_id AND probe_queue.connection_id=queue_row.connection_id;
  RETURN QUERY SELECT queue_row.account_id,p_probe_id,queue_row.connection_id,revision_row.id,revision_row.revision,
    connection_row.connector_kind,revision_row.capabilities,revision_row.email_address,revision_row.audience_reference,
    revision_row.https_origin,revision_row.path_prefix,revision_row.drive_folder_ids,credential_row.id,credential_row.generation,
    credential_row.provider,credential_row.reference_sha256,p_lease_expires_at;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_claim_integration_health_probe(uuid,timestamptz,timestamptz) FROM PUBLIC;

COMMIT;
