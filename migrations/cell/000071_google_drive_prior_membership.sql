BEGIN;

-- Drive removal entries omit their former parent folder. Return only the last
-- authorized folder for an object under the caller's live sync lease; raw
-- provider identities and capture-table access remain unavailable to workers.
CREATE FUNCTION public.spyglass_resolve_integration_source_folder(
  p_account_id uuid,p_grant_id uuid,p_sync_id uuid,p_provider_object_sha256 bytea
) RETURNS TABLE(folder_id text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE authority record;
BEGIN
  IF p_account_id IS NULL OR p_grant_id IS NULL OR p_sync_id IS NULL OR
     p_provider_object_sha256 IS NULL OR octet_length(p_provider_object_sha256)<>32 THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Integration source membership lookup';
  END IF;
  PERFORM set_config('app.account_id',p_account_id::text,true);
  SELECT queue.connection_id,queue.lease_id,queue.lease_expires_at,
    source_grant.state AS grant_state,source_grant.source_kind,source_grant.folders,
    connection.state AS connection_state,revision.capabilities,revision.drive_folder_ids
  INTO authority
  FROM spyglass.integration_source_sync_queue queue
  JOIN spyglass.baseline_source_grants source_grant
    ON source_grant.account_id=queue.account_id AND source_grant.id=queue.grant_id
  JOIN spyglass.integration_connections connection
    ON connection.account_id=queue.account_id AND connection.id=queue.connection_id
  JOIN spyglass.integration_connection_revisions revision
    ON revision.account_id=connection.account_id AND revision.connection_id=connection.id AND
       revision.revision=connection.current_revision
  WHERE queue.account_id=p_account_id AND queue.grant_id=p_grant_id
  FOR SHARE OF queue,source_grant,connection,revision;
  IF NOT FOUND OR authority.lease_id IS DISTINCT FROM p_sync_id OR authority.lease_expires_at IS NULL OR
     authority.lease_expires_at<statement_timestamp() OR authority.grant_state IS DISTINCT FROM 'active' OR
     authority.source_kind IS DISTINCT FROM 'google_drive' OR authority.connection_state IS DISTINCT FROM 'active' OR
     authority.capabilities IS DISTINCT FROM ARRAY['google_drive.read']::text[] OR
     NOT authority.folders <@ authority.drive_folder_ids THEN
    RAISE EXCEPTION USING ERRCODE='P2005',MESSAGE='Integration source membership authority changed';
  END IF;
  RETURN QUERY
  SELECT latest.folder_id
  FROM (
    SELECT capture.folder_id,capture.operation
    FROM spyglass.integration_source_captures capture
    WHERE capture.account_id=p_account_id AND capture.grant_id=p_grant_id AND
      capture.connection_id=authority.connection_id AND capture.provider_object_sha256=p_provider_object_sha256
    ORDER BY capture.captured_at DESC,capture.id DESC
    LIMIT 1
  ) latest
  WHERE latest.operation='admitted';
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_resolve_integration_source_folder(uuid,uuid,uuid,bytea) FROM PUBLIC;

COMMIT;
