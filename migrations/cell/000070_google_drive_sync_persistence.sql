BEGIN;

-- Incremental provider cursors are operational secrets. Only ciphertext and a
-- plaintext digest enter this private, regenerated queue. Immutable capture
-- receipts retain exact provenance without provider identities or content.
CREATE TABLE spyglass.integration_source_sync_queue (
    account_id uuid NOT NULL,
    grant_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    available_at timestamptz NOT NULL,
    cursor_ciphertext bytea NOT NULL DEFAULT ''::bytea CHECK (octet_length(cursor_ciphertext)<=8192),
    cursor_sha256 bytea CHECK (cursor_sha256 IS NULL OR octet_length(cursor_sha256)=32),
    lease_id uuid,
    lease_expires_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,grant_id),
    FOREIGN KEY (account_id,grant_id) REFERENCES spyglass.baseline_source_grants(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_id) REFERENCES spyglass.integration_connections(account_id,id) ON DELETE CASCADE,
    CHECK ((cursor_ciphertext=''::bytea)=(cursor_sha256 IS NULL)),
    CHECK ((lease_id IS NULL AND lease_expires_at IS NULL) OR (lease_id IS NOT NULL AND lease_expires_at>updated_at))
);
CREATE INDEX integration_source_sync_queue_due
    ON spyglass.integration_source_sync_queue(available_at,grant_id);

CREATE TABLE spyglass.integration_source_captures (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    grant_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_revision_id uuid NOT NULL,
    connection_revision bigint NOT NULL CHECK (connection_revision>0),
    credential_id uuid NOT NULL,
    credential_generation bigint NOT NULL CHECK (credential_generation>0),
    folder_id text NOT NULL CHECK (octet_length(folder_id) BETWEEN 1 AND 255 AND folder_id ~ '^[!-~]+$'),
    provider_object_sha256 bytea NOT NULL CHECK (octet_length(provider_object_sha256)=32),
    provider_revision_sha256 bytea NOT NULL CHECK (octet_length(provider_revision_sha256)=32),
    operation text NOT NULL CHECK (operation IN ('admitted','deleted')),
    document_id uuid NOT NULL,
    document_revision_id uuid NOT NULL,
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256)=32),
    captured_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,grant_id,provider_object_sha256,provider_revision_sha256),
    FOREIGN KEY (account_id,grant_id) REFERENCES spyglass.baseline_source_grants(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_id,connection_revision_id,connection_revision)
      REFERENCES spyglass.integration_connection_revisions(account_id,connection_id,id,revision) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_id,credential_id,credential_generation)
      REFERENCES spyglass.integration_credentials(account_id,connection_id,id,generation) ON DELETE CASCADE,
    FOREIGN KEY (account_id,document_id) REFERENCES spyglass.knowledge_documents(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,document_revision_id) REFERENCES spyglass.knowledge_document_revisions(account_id,id) ON DELETE CASCADE
);
CREATE INDEX integration_source_captures_document
    ON spyglass.integration_source_captures(account_id,document_id,captured_at,id);

ALTER TABLE spyglass.integration_source_captures ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.integration_source_captures FORCE ROW LEVEL SECURITY;
CREATE POLICY integration_source_captures_isolation ON spyglass.integration_source_captures
  USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
  WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.validate_integration_source_capture() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE grant_row record;
BEGIN
  IF current_setting('spyglass.account_movement',true)='on' AND
     spyglass.account_movement_write_allowed(NEW.account_id,'INSERT') THEN
    RETURN NEW;
  END IF;
  SELECT connection_id,source_kind,folders INTO grant_row
  FROM spyglass.baseline_source_grants
  WHERE account_id=NEW.account_id AND id=NEW.grant_id FOR SHARE;
  IF NOT FOUND OR grant_row.connection_id<>NEW.connection_id OR grant_row.source_kind<>'google_drive' OR
     NOT NEW.folder_id=ANY(grant_row.folders) OR NOT EXISTS (
       SELECT 1 FROM spyglass.knowledge_document_revisions revision
       WHERE revision.account_id=NEW.account_id AND revision.document_id=NEW.document_id AND revision.id=NEW.document_revision_id
         AND revision.content_sha256=NEW.content_sha256
     ) THEN
    RAISE EXCEPTION 'Integration source capture binding is invalid';
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.sync_google_drive_source_queue_from_grant() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
  IF NEW.source_kind='google_drive' AND NEW.state='active' THEN
    INSERT INTO spyglass.integration_source_sync_queue(account_id,grant_id,connection_id,available_at,updated_at)
    VALUES(NEW.account_id,NEW.id,NEW.connection_id,NEW.updated_at,NEW.updated_at)
    ON CONFLICT(account_id,grant_id) DO UPDATE SET connection_id=EXCLUDED.connection_id,available_at=EXCLUDED.available_at,
      cursor_ciphertext=''::bytea,cursor_sha256=NULL,lease_id=NULL,lease_expires_at=NULL,updated_at=EXCLUDED.updated_at;
  ELSE
    DELETE FROM spyglass.integration_source_sync_queue WHERE account_id=NEW.account_id AND grant_id=NEW.id;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER google_drive_source_queue_grant_sync
AFTER INSERT OR UPDATE OF state,updated_at ON spyglass.baseline_source_grants
FOR EACH ROW EXECUTE FUNCTION spyglass.sync_google_drive_source_queue_from_grant();

CREATE FUNCTION spyglass.sync_google_drive_source_queue_from_connection() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
  IF NEW.connector_kind='google_drive' AND NEW.state='active' THEN
    INSERT INTO spyglass.integration_source_sync_queue(account_id,grant_id,connection_id,available_at,updated_at)
    SELECT source_grant.account_id,source_grant.id,source_grant.connection_id,NEW.updated_at,NEW.updated_at
    FROM spyglass.baseline_source_grants source_grant
    WHERE source_grant.account_id=NEW.account_id AND source_grant.connection_id=NEW.id AND source_grant.source_kind='google_drive' AND source_grant.state='active'
    ON CONFLICT(account_id,grant_id) DO UPDATE SET available_at=EXCLUDED.available_at,cursor_ciphertext=''::bytea,
      cursor_sha256=NULL,lease_id=NULL,lease_expires_at=NULL,updated_at=EXCLUDED.updated_at;
  ELSE
    DELETE FROM spyglass.integration_source_sync_queue WHERE account_id=NEW.account_id AND connection_id=NEW.id;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER google_drive_source_queue_connection_sync
AFTER UPDATE OF state,current_revision,credential_id,credential_generation,updated_at ON spyglass.integration_connections
FOR EACH ROW EXECUTE FUNCTION spyglass.sync_google_drive_source_queue_from_connection();

INSERT INTO spyglass.integration_source_sync_queue(account_id,grant_id,connection_id,available_at,updated_at)
SELECT source_grant.account_id,source_grant.id,source_grant.connection_id,source_grant.updated_at,source_grant.updated_at
FROM spyglass.baseline_source_grants source_grant JOIN spyglass.integration_connections connection
  ON connection.account_id=source_grant.account_id AND connection.id=source_grant.connection_id
WHERE source_grant.source_kind='google_drive' AND source_grant.state='active' AND connection.state='active'
ON CONFLICT(account_id,grant_id) DO NOTHING;

CREATE FUNCTION public.spyglass_claim_integration_source_sync(
  p_sync_id uuid,p_now timestamptz,p_lease_expires_at timestamptz
) RETURNS TABLE (
  account_id uuid,sync_id uuid,grant_id uuid,connection_id uuid,connection_revision_id uuid,connection_revision bigint,
  credential_id uuid,credential_generation bigint,credential_provider text,credential_reference_sha256 bytea,
  folder_ids text[],cursor_ciphertext bytea,cursor_sha256 bytea,lease_expires_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE queue_row record; grant_row record; connection_row record; revision_row record; credential_row record;
BEGIN
  IF p_sync_id IS NULL OR p_now IS NULL OR p_lease_expires_at IS NULL OR p_lease_expires_at<=p_now OR
     p_lease_expires_at>p_now+interval '5 minutes' THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Integration source sync claim';
  END IF;
  SELECT queue.account_id,queue.grant_id,queue.connection_id,queue.cursor_ciphertext,queue.cursor_sha256 INTO queue_row
  FROM spyglass.integration_source_sync_queue queue
  WHERE queue.available_at<=p_now AND (queue.lease_expires_at IS NULL OR queue.lease_expires_at<=p_now)
  ORDER BY queue.available_at,queue.grant_id FOR UPDATE SKIP LOCKED LIMIT 1;
  IF NOT FOUND THEN RETURN; END IF;
  PERFORM set_config('app.account_id',queue_row.account_id::text,true);
  SELECT source_grant.state,source_grant.source_kind,source_grant.folders,source_grant.connection_id INTO grant_row FROM spyglass.baseline_source_grants source_grant
  WHERE source_grant.account_id=queue_row.account_id AND source_grant.id=queue_row.grant_id FOR SHARE;
  SELECT connection.state,connection.connector_kind,connection.current_revision,connection.credential_id,connection.credential_generation INTO connection_row
  FROM spyglass.integration_connections connection
  WHERE connection.account_id=queue_row.account_id AND connection.id=queue_row.connection_id FOR SHARE;
  IF grant_row.state IS DISTINCT FROM 'active' OR grant_row.source_kind IS DISTINCT FROM 'google_drive' OR
     grant_row.connection_id IS DISTINCT FROM queue_row.connection_id OR connection_row.state IS DISTINCT FROM 'active' OR
     connection_row.connector_kind IS DISTINCT FROM 'google_drive' THEN
    DELETE FROM spyglass.integration_source_sync_queue queue
    WHERE queue.account_id=queue_row.account_id AND queue.grant_id=queue_row.grant_id;
    RETURN;
  END IF;
  SELECT revision.id,revision.revision,revision.capabilities,revision.drive_folder_ids INTO revision_row
  FROM spyglass.integration_connection_revisions revision
  WHERE revision.account_id=queue_row.account_id AND revision.connection_id=queue_row.connection_id AND
    revision.revision=connection_row.current_revision FOR SHARE;
  SELECT credential.id,credential.generation,credential.provider,credential.reference_sha256,credential.state,credential.expires_at INTO credential_row
  FROM spyglass.integration_credentials credential
  WHERE credential.account_id=queue_row.account_id AND credential.connection_id=queue_row.connection_id AND
    credential.id=connection_row.credential_id AND credential.generation=connection_row.credential_generation FOR SHARE;
  IF revision_row.id IS NULL OR revision_row.capabilities<>ARRAY['google_drive.read']::text[] OR
     NOT grant_row.folders <@ revision_row.drive_folder_ids OR credential_row.id IS NULL OR credential_row.state<>'active' OR
     (credential_row.expires_at IS NOT NULL AND credential_row.expires_at<=p_now) THEN
    UPDATE spyglass.integration_source_sync_queue queue SET available_at=p_now+interval '5 minutes',lease_id=NULL,
      lease_expires_at=NULL,updated_at=p_now WHERE queue.account_id=queue_row.account_id AND queue.grant_id=queue_row.grant_id;
    RETURN;
  END IF;
  UPDATE spyglass.integration_source_sync_queue queue SET lease_id=p_sync_id,lease_expires_at=p_lease_expires_at,updated_at=p_now
  WHERE queue.account_id=queue_row.account_id AND queue.grant_id=queue_row.grant_id;
  RETURN QUERY SELECT queue_row.account_id,p_sync_id,queue_row.grant_id,queue_row.connection_id,revision_row.id,
    revision_row.revision,credential_row.id,credential_row.generation,credential_row.provider,credential_row.reference_sha256,
    grant_row.folders,queue_row.cursor_ciphertext,queue_row.cursor_sha256,p_lease_expires_at;
END;
$$;

CREATE FUNCTION public.spyglass_complete_integration_source_sync(
  p_account_id uuid,p_grant_id uuid,p_sync_id uuid,p_connection_revision_id uuid,p_connection_revision bigint,
  p_credential_id uuid,p_credential_generation bigint,p_cursor_ciphertext bytea,p_cursor_sha256 bytea,p_has_more boolean,
  p_capture_ids uuid[],p_folder_ids text[],p_provider_object_sha256 bytea[],p_provider_revision_sha256 bytea[],p_operations text[],
  p_document_ids uuid[],p_document_revision_ids uuid[],p_content_sha256 bytea[],p_completed_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE queue_row record; grant_row record; connection_row record; capture_count integer; item integer; inserted_count integer;
BEGIN
  capture_count:=COALESCE(cardinality(p_capture_ids),0);
  IF p_account_id IS NULL OR p_grant_id IS NULL OR p_sync_id IS NULL OR p_connection_revision_id IS NULL OR
     p_connection_revision<=0 OR p_credential_id IS NULL OR p_credential_generation<=0 OR p_cursor_ciphertext IS NULL OR
     octet_length(p_cursor_ciphertext) NOT BETWEEN 1 AND 8192 OR p_cursor_sha256 IS NULL OR octet_length(p_cursor_sha256)<>32 OR
     p_has_more IS NULL OR p_completed_at IS NULL OR capture_count>100 OR
     capture_count<>COALESCE(cardinality(p_folder_ids),0) OR capture_count<>COALESCE(cardinality(p_provider_object_sha256),0) OR
     capture_count<>COALESCE(cardinality(p_provider_revision_sha256),0) OR capture_count<>COALESCE(cardinality(p_operations),0) OR
     capture_count<>COALESCE(cardinality(p_document_ids),0) OR capture_count<>COALESCE(cardinality(p_document_revision_ids),0) OR
     capture_count<>COALESCE(cardinality(p_content_sha256),0) THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Integration source sync completion';
  END IF;
  PERFORM set_config('app.account_id',p_account_id::text,true);
  SELECT queue.connection_id,queue.lease_id,queue.lease_expires_at,queue.updated_at INTO queue_row
  FROM spyglass.integration_source_sync_queue queue
  WHERE queue.account_id=p_account_id AND queue.grant_id=p_grant_id FOR UPDATE;
  SELECT state,source_kind,folders,connection_id INTO grant_row FROM spyglass.baseline_source_grants source_grant
  WHERE source_grant.account_id=p_account_id AND source_grant.id=p_grant_id FOR SHARE;
  SELECT state,current_revision,credential_id,credential_generation INTO connection_row
  FROM spyglass.integration_connections connection
  WHERE connection.account_id=p_account_id AND connection.id=queue_row.connection_id FOR SHARE;
  IF queue_row.lease_id IS DISTINCT FROM p_sync_id OR queue_row.lease_expires_at IS NULL OR p_completed_at<queue_row.updated_at OR
     p_completed_at>queue_row.lease_expires_at OR grant_row.state IS DISTINCT FROM 'active' OR
     grant_row.source_kind IS DISTINCT FROM 'google_drive' OR grant_row.connection_id IS DISTINCT FROM queue_row.connection_id OR
     connection_row.state IS DISTINCT FROM 'active' OR connection_row.current_revision IS DISTINCT FROM p_connection_revision OR
     connection_row.credential_id IS DISTINCT FROM p_credential_id OR connection_row.credential_generation IS DISTINCT FROM p_credential_generation OR
     NOT EXISTS (SELECT 1 FROM spyglass.integration_connection_revisions revision WHERE revision.account_id=p_account_id AND
       revision.connection_id=queue_row.connection_id AND revision.id=p_connection_revision_id AND revision.revision=p_connection_revision AND
       revision.capabilities=ARRAY['google_drive.read']::text[] AND grant_row.folders <@ revision.drive_folder_ids) THEN
    RAISE EXCEPTION USING ERRCODE='P2005',MESSAGE='Integration source sync lease or binding changed';
  END IF;
  FOR item IN 1..capture_count LOOP
    IF p_capture_ids[item] IS NULL OR p_folder_ids[item] IS NULL OR NOT p_folder_ids[item]=ANY(grant_row.folders) OR
       p_provider_object_sha256[item] IS NULL OR octet_length(p_provider_object_sha256[item])<>32 OR
       p_provider_revision_sha256[item] IS NULL OR octet_length(p_provider_revision_sha256[item])<>32 OR
       p_operations[item] NOT IN ('admitted','deleted') OR p_document_ids[item] IS NULL OR p_document_revision_ids[item] IS NULL OR
       p_content_sha256[item] IS NULL OR octet_length(p_content_sha256[item])<>32 THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Integration source capture receipt';
    END IF;
    INSERT INTO spyglass.integration_source_captures(account_id,id,grant_id,connection_id,connection_revision_id,
      connection_revision,credential_id,credential_generation,folder_id,provider_object_sha256,provider_revision_sha256,
      operation,document_id,document_revision_id,content_sha256,captured_at)
    VALUES(p_account_id,p_capture_ids[item],p_grant_id,queue_row.connection_id,p_connection_revision_id,p_connection_revision,
      p_credential_id,p_credential_generation,p_folder_ids[item],p_provider_object_sha256[item],p_provider_revision_sha256[item],
      p_operations[item],p_document_ids[item],p_document_revision_ids[item],p_content_sha256[item],p_completed_at)
    ON CONFLICT (account_id,grant_id,provider_object_sha256,provider_revision_sha256) DO NOTHING;
    GET DIAGNOSTICS inserted_count=ROW_COUNT;
    IF inserted_count=0 AND NOT EXISTS (SELECT 1 FROM spyglass.integration_source_captures capture
      WHERE capture.account_id=p_account_id AND capture.id=p_capture_ids[item] AND capture.grant_id=p_grant_id AND
        capture.connection_id=queue_row.connection_id AND capture.connection_revision_id=p_connection_revision_id AND
        capture.connection_revision=p_connection_revision AND capture.credential_id=p_credential_id AND
        capture.credential_generation=p_credential_generation AND capture.folder_id=p_folder_ids[item] AND
        capture.provider_object_sha256=p_provider_object_sha256[item] AND capture.provider_revision_sha256=p_provider_revision_sha256[item] AND
        capture.operation=p_operations[item] AND capture.document_id=p_document_ids[item] AND
        capture.document_revision_id=p_document_revision_ids[item] AND capture.content_sha256=p_content_sha256[item]) THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='Integration source capture replay conflicts';
    END IF;
  END LOOP;
  UPDATE spyglass.integration_source_sync_queue queue SET cursor_ciphertext=p_cursor_ciphertext,cursor_sha256=p_cursor_sha256,
    available_at=p_completed_at+CASE WHEN p_has_more THEN interval '0 seconds' ELSE interval '5 minutes' END,
    lease_id=NULL,lease_expires_at=NULL,updated_at=p_completed_at
  WHERE queue.account_id=p_account_id AND queue.grant_id=p_grant_id;
END;
$$;

CREATE TRIGGER integration_source_captures_binding BEFORE INSERT ON spyglass.integration_source_captures
FOR EACH ROW EXECUTE FUNCTION spyglass.validate_integration_source_capture();
CREATE TRIGGER integration_source_captures_immutable BEFORE UPDATE OR DELETE ON spyglass.integration_source_captures
FOR EACH ROW EXECUTE FUNCTION spyglass.reject_integration_immutable_change();
CREATE TRIGGER integration_source_captures_erasure_count BEFORE DELETE ON spyglass.integration_source_captures
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_integration_erasure_count();
CREATE TRIGGER integration_source_sync_queue_erasure_count BEFORE DELETE ON spyglass.integration_source_sync_queue
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_integration_erasure_count();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.integration_source_captures
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

REVOKE ALL ON TABLE spyglass.integration_source_sync_queue FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.validate_integration_source_capture() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.sync_google_drive_source_queue_from_grant() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.sync_google_drive_source_queue_from_connection() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_integration_source_sync(uuid,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_complete_integration_source_sync(uuid,uuid,uuid,uuid,bigint,uuid,bigint,bytea,bytea,boolean,uuid[],text[],bytea[],bytea[],text[],uuid[],uuid[],bytea[],timestamptz) FROM PUBLIC;

COMMIT;
