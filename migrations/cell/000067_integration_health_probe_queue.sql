BEGIN;

-- Provider execution is gated on a fresh immutable health observation. This
-- private queue gives the connector workload a lease-fenced, content-free way
-- to produce those observations without direct table privileges.
CREATE TABLE spyglass.integration_health_probe_queue (
    account_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    available_at timestamptz NOT NULL,
    lease_id uuid,
    lease_expires_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,connection_id),
    FOREIGN KEY (account_id,connection_id) REFERENCES spyglass.integration_connections(account_id,id) ON DELETE CASCADE,
    CHECK ((lease_id IS NULL AND lease_expires_at IS NULL) OR (lease_id IS NOT NULL AND lease_expires_at>updated_at))
);
CREATE INDEX integration_health_probe_queue_due
    ON spyglass.integration_health_probe_queue(available_at,connection_id);

CREATE FUNCTION spyglass.sync_integration_health_probe_queue() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
  IF NEW.state='active' THEN
    INSERT INTO spyglass.integration_health_probe_queue(account_id,connection_id,available_at,updated_at)
    VALUES(NEW.account_id,NEW.id,NEW.updated_at,NEW.updated_at)
    ON CONFLICT(account_id,connection_id) DO UPDATE
      SET available_at=EXCLUDED.available_at,lease_id=NULL,lease_expires_at=NULL,updated_at=EXCLUDED.updated_at;
  ELSE
    DELETE FROM spyglass.integration_health_probe_queue WHERE account_id=NEW.account_id AND connection_id=NEW.id;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER integration_health_probe_queue_sync
AFTER INSERT OR UPDATE OF state,current_revision,credential_id,credential_generation,updated_at ON spyglass.integration_connections
FOR EACH ROW EXECUTE FUNCTION spyglass.sync_integration_health_probe_queue();

INSERT INTO spyglass.integration_health_probe_queue(account_id,connection_id,available_at,updated_at)
SELECT account_id,id,updated_at,updated_at FROM spyglass.integration_connections WHERE state='active'
ON CONFLICT(account_id,connection_id) DO NOTHING;

CREATE FUNCTION public.spyglass_claim_integration_health_probe(
  p_probe_id uuid,p_now timestamptz,p_lease_expires_at timestamptz
) RETURNS TABLE (
  account_id uuid,probe_id uuid,connection_id uuid,connection_revision_id uuid,connection_revision bigint,
  connector_kind text,capabilities text[],email_address text,audience_reference text,https_origin text,path_prefix text,
  credential_id uuid,credential_generation bigint,credential_provider text,credential_reference_sha256 bytea,lease_expires_at timestamptz
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
    revision.https_origin,revision.path_prefix INTO revision_row
  FROM spyglass.integration_connection_revisions revision
  WHERE revision.account_id=queue_row.account_id AND revision.connection_id=queue_row.connection_id AND
    revision.revision=connection_row.current_revision FOR SHARE;
  SELECT credential.id,credential.generation,credential.provider,credential.reference_sha256,credential.state,credential.expires_at
    INTO credential_row
  FROM spyglass.integration_credentials credential
  WHERE credential.account_id=queue_row.account_id AND credential.connection_id=queue_row.connection_id AND
    credential.id=connection_row.credential_id AND credential.generation=connection_row.credential_generation FOR SHARE;
  IF revision_row.id IS NULL OR credential_row.id IS NULL OR credential_row.state<>'active' OR
     (credential_row.expires_at IS NOT NULL AND credential_row.expires_at<=p_now) OR
     NOT (revision_row.capabilities @> ARRAY['email.send']::text[] OR revision_row.capabilities=ARRAY['web.publish']::text[]) THEN
    DELETE FROM spyglass.integration_health_probe_queue probe_queue WHERE probe_queue.account_id=queue_row.account_id AND probe_queue.connection_id=queue_row.connection_id;
    RETURN;
  END IF;
  UPDATE spyglass.integration_health_probe_queue probe_queue SET lease_id=p_probe_id,lease_expires_at=p_lease_expires_at,updated_at=p_now
    WHERE probe_queue.account_id=queue_row.account_id AND probe_queue.connection_id=queue_row.connection_id;
  RETURN QUERY SELECT queue_row.account_id,p_probe_id,queue_row.connection_id,revision_row.id,revision_row.revision,
    connection_row.connector_kind,revision_row.capabilities,revision_row.email_address,revision_row.audience_reference,
    revision_row.https_origin,revision_row.path_prefix,credential_row.id,credential_row.generation,credential_row.provider,
    credential_row.reference_sha256,p_lease_expires_at;
END;
$$;

CREATE FUNCTION public.spyglass_complete_integration_health_probe(
  p_account_id uuid,p_connection_id uuid,p_probe_id uuid,p_connection_revision_id uuid,p_connection_revision bigint,
  p_credential_id uuid,p_credential_generation bigint,p_state text,p_error_code text,p_latency_milliseconds integer,p_checked_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE queue_row record; connection_row record; next_available timestamptz;
BEGIN
  IF p_account_id IS NULL OR p_connection_id IS NULL OR p_probe_id IS NULL OR p_connection_revision_id IS NULL OR
     p_connection_revision<=0 OR p_credential_id IS NULL OR p_credential_generation<=0 OR
     p_state NOT IN ('healthy','degraded','unavailable') OR p_latency_milliseconds NOT BETWEEN 0 AND 300000 OR p_checked_at IS NULL OR
     (p_state='healthy' AND p_error_code IS NOT NULL) OR
     (p_state IN ('degraded','unavailable') AND (p_error_code IS NULL OR p_error_code !~ '^[a-z][a-z0-9_.]{0,99}$')) THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Integration health probe completion';
  END IF;
  PERFORM set_config('app.account_id',p_account_id::text,true);
  SELECT queue.lease_id,queue.lease_expires_at,queue.updated_at INTO queue_row
  FROM spyglass.integration_health_probe_queue queue
  WHERE queue.account_id=p_account_id AND queue.connection_id=p_connection_id FOR UPDATE;
  SELECT connection.state,connection.current_revision,connection.credential_id,connection.credential_generation INTO connection_row
  FROM spyglass.integration_connections connection
  WHERE connection.account_id=p_account_id AND connection.id=p_connection_id FOR SHARE;
  IF queue_row.lease_id IS DISTINCT FROM p_probe_id OR queue_row.lease_expires_at IS NULL OR p_checked_at<queue_row.updated_at OR
     p_checked_at>queue_row.lease_expires_at OR connection_row.state IS DISTINCT FROM 'active' OR
     connection_row.current_revision IS DISTINCT FROM p_connection_revision OR connection_row.credential_id IS DISTINCT FROM p_credential_id OR
     connection_row.credential_generation IS DISTINCT FROM p_credential_generation OR
     NOT EXISTS (SELECT 1 FROM spyglass.integration_connection_revisions revision WHERE revision.account_id=p_account_id AND
       revision.connection_id=p_connection_id AND revision.id=p_connection_revision_id AND revision.revision=p_connection_revision) THEN
    RAISE EXCEPTION USING ERRCODE='P2005',MESSAGE='Integration health probe lease or binding changed';
  END IF;
  INSERT INTO spyglass.integration_health_observations(account_id,id,connection_id,connection_revision,credential_id,
    credential_generation,state,error_code,latency_milliseconds,checked_at)
  VALUES(p_account_id,p_probe_id,p_connection_id,p_connection_revision,p_credential_id,p_credential_generation,p_state,
    p_error_code,p_latency_milliseconds,p_checked_at);
  next_available:=p_checked_at+CASE p_state WHEN 'healthy' THEN interval '2 minutes'
    WHEN 'degraded' THEN interval '1 minute' ELSE interval '30 seconds' END;
  UPDATE spyglass.integration_health_probe_queue probe_queue SET available_at=next_available,lease_id=NULL,lease_expires_at=NULL,updated_at=p_checked_at
    WHERE probe_queue.account_id=p_account_id AND probe_queue.connection_id=p_connection_id;
END;
$$;

CREATE TRIGGER integration_health_probe_queue_erasure_count BEFORE DELETE ON spyglass.integration_health_probe_queue
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_integration_erasure_count();

REVOKE ALL ON TABLE spyglass.integration_health_probe_queue FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.sync_integration_health_probe_queue() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_integration_health_probe(uuid,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_complete_integration_health_probe(uuid,uuid,uuid,uuid,bigint,uuid,bigint,text,text,integer,timestamptz) FROM PUBLIC;

COMMIT;
