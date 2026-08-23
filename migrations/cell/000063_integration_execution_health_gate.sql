BEGIN;

CREATE OR REPLACE FUNCTION public.spyglass_claim_integration_execution(
  p_attempt_id uuid,p_now timestamptz,p_lease_expires_at timestamptz
) RETURNS TABLE (
  account_id uuid,execution_id uuid,attempt_id uuid,mode text,capability text,release_id uuid,release_version bigint,approval_id uuid,
  connection_id uuid,connection_revision_id uuid,connection_revision bigint,credential_id uuid,credential_generation bigint,
  payload_sha256 bytea,idempotency_key uuid,lease_expires_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE queue_row record; execution_row record; release_row record; campaign_row record; approval_row record; connection_row record;
        revision_row record; credential_row record; health_row record; selected_mode text; previous_attempt uuid; effective_state text;
        authorization_valid boolean; release_channel_valid boolean;
BEGIN
  IF p_attempt_id IS NULL OR p_now IS NULL OR p_lease_expires_at IS NULL OR p_lease_expires_at<=p_now OR p_lease_expires_at>p_now+interval '5 minutes' THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Integration execution claim';
  END IF;
  SELECT queue.account_id,queue.execution_id INTO queue_row FROM spyglass.integration_execution_queue queue
  WHERE queue.available_at<=p_now AND (queue.lease_expires_at IS NULL OR queue.lease_expires_at<=p_now)
  ORDER BY queue.available_at,queue.execution_id FOR UPDATE SKIP LOCKED LIMIT 1;
  IF NOT FOUND THEN RETURN; END IF;
  PERFORM set_config('app.account_id',queue_row.account_id::text,true);
  PERFORM 1 FROM spyglass.account_namespaces namespace WHERE namespace.account_id=queue_row.account_id AND namespace.state='active';
  IF NOT FOUND THEN RETURN; END IF;

  SELECT execution.* INTO execution_row FROM spyglass.integration_executions execution
  WHERE execution.account_id=queue_row.account_id AND execution.id=queue_row.execution_id FOR UPDATE;
  IF NOT FOUND OR execution_row.state IN ('manual_resolution','succeeded','failed','cancelled') THEN
    DELETE FROM spyglass.integration_execution_queue queue WHERE queue.account_id=queue_row.account_id AND queue.execution_id=queue_row.execution_id;
    RETURN;
  END IF;
  IF execution_row.state IN ('executing','reconciling') AND execution_row.lease_expires_at>p_now THEN RETURN; END IF;
  IF execution_row.state='retry_wait' AND execution_row.next_attempt_at>p_now THEN RETURN; END IF;

  IF execution_row.state IN ('executing','reconciling') THEN
    previous_attempt:=execution_row.current_attempt_id;
    UPDATE spyglass.integration_execution_attempts attempt SET outcome='unknown',error_code='lease_expired',completed_at=p_now
    WHERE attempt.account_id=execution_row.account_id AND attempt.id=previous_attempt AND attempt.outcome IS NULL;
    effective_state:=CASE WHEN execution_row.attempt_count>=3 THEN 'manual_resolution' ELSE 'unknown' END;
    UPDATE spyglass.integration_executions execution SET state=effective_state,current_attempt_id=NULL,last_error_code='lease_expired',lease_expires_at=NULL,updated_at=p_now
    WHERE execution.account_id=execution_row.account_id AND execution.id=execution_row.id;
    execution_row.state:=effective_state;
    execution_row.current_attempt_id:=NULL;
    execution_row.last_error_code:='lease_expired';
    execution_row.lease_expires_at:=NULL;
    execution_row.updated_at:=p_now;
    IF effective_state='manual_resolution' THEN RETURN; END IF;
  END IF;

  SELECT release.state,release.version,release.approval_id,release.campaign_id INTO release_row FROM spyglass.marketing_release_plans release
  WHERE release.account_id=execution_row.account_id AND release.id=execution_row.release_id FOR SHARE;
  SELECT campaign.state,campaign.active_release_id INTO campaign_row FROM spyglass.marketing_campaigns campaign
  WHERE campaign.account_id=execution_row.account_id AND campaign.id=release_row.campaign_id FOR SHARE;
  SELECT EXISTS (SELECT 1 FROM spyglass.marketing_release_channels channel
    WHERE channel.account_id=execution_row.account_id AND channel.release_id=execution_row.release_id AND
      channel.channel=CASE WHEN execution_row.capability='email.send' THEN 'email' ELSE 'web' END) INTO release_channel_valid;
  SELECT approval.state,approval.capability,approval.expires_at INTO approval_row FROM spyglass.attention_consequential_approvals approval
  WHERE approval.account_id=execution_row.account_id AND approval.id=execution_row.approval_id FOR SHARE;
  SELECT connection.state,connection.current_revision,connection.credential_id,connection.credential_generation INTO connection_row FROM spyglass.integration_connections connection
  WHERE connection.account_id=execution_row.account_id AND connection.id=execution_row.connection_id FOR SHARE;
  SELECT revision.capabilities INTO revision_row FROM spyglass.integration_connection_revisions revision
  WHERE revision.account_id=execution_row.account_id AND revision.connection_id=execution_row.connection_id AND revision.id=execution_row.connection_revision_id AND revision.revision=execution_row.connection_revision FOR SHARE;
  SELECT credential.state,credential.expires_at INTO credential_row FROM spyglass.integration_credentials credential
  WHERE credential.account_id=execution_row.account_id AND credential.connection_id=execution_row.connection_id AND credential.id=execution_row.credential_id AND credential.generation=execution_row.credential_generation FOR SHARE;
  authorization_valid:=COALESCE(release_row.state='approved' AND release_row.version=execution_row.release_version AND release_row.approval_id=execution_row.approval_id AND
    campaign_row.state='active' AND campaign_row.active_release_id=execution_row.release_id AND release_channel_valid AND
    approval_row.state='approved' AND approval_row.capability='marketing.release.activate' AND approval_row.expires_at>p_now AND connection_row.state='active' AND
    connection_row.current_revision=execution_row.connection_revision AND connection_row.credential_id=execution_row.credential_id AND
    connection_row.credential_generation=execution_row.credential_generation AND revision_row.capabilities @> ARRAY[execution_row.capability]::text[] AND
    credential_row.state='active' AND (credential_row.expires_at IS NULL OR credential_row.expires_at>p_now),false);
  IF NOT authorization_valid THEN
    IF execution_row.state='unknown' THEN
      UPDATE spyglass.integration_executions execution SET state='manual_resolution',current_attempt_id=NULL,last_error_code='reconciliation_authority_unavailable',
        lease_expires_at=NULL,next_attempt_at=NULL,updated_at=p_now,completed_at=NULL WHERE execution.account_id=execution_row.account_id AND execution.id=execution_row.id;
    ELSE
      UPDATE spyglass.integration_executions execution SET state='cancelled',current_attempt_id=NULL,last_error_code='execution_authority_unavailable',
        lease_expires_at=NULL,next_attempt_at=NULL,updated_at=p_now,completed_at=p_now WHERE execution.account_id=execution_row.account_id AND execution.id=execution_row.id;
    END IF;
    RETURN;
  END IF;

  SELECT health.state,health.checked_at INTO health_row FROM spyglass.integration_health_observations health
  WHERE health.account_id=execution_row.account_id AND health.connection_id=execution_row.connection_id AND
    health.connection_revision=execution_row.connection_revision AND health.credential_id=execution_row.credential_id AND
    health.credential_generation=execution_row.credential_generation AND health.checked_at<=p_now
  ORDER BY health.checked_at DESC,health.id DESC LIMIT 1 FOR SHARE;
  IF NOT FOUND OR health_row.checked_at<p_now-interval '5 minutes' OR health_row.state='unavailable' THEN
    UPDATE spyglass.integration_execution_queue queue
    SET available_at=GREATEST(queue.available_at,p_now+interval '30 seconds'),lease_expires_at=NULL,updated_at=p_now
    WHERE queue.account_id=execution_row.account_id AND queue.execution_id=execution_row.id;
    RETURN;
  END IF;

  selected_mode:=CASE WHEN execution_row.state='unknown' THEN 'reconcile' ELSE 'execute' END;
  IF execution_row.attempt_count>=3 THEN
    UPDATE spyglass.integration_executions execution SET state=CASE WHEN selected_mode='reconcile' THEN 'manual_resolution' ELSE 'failed' END,
      last_error_code='attempt_limit_reached',updated_at=p_now,completed_at=CASE WHEN selected_mode='execute' THEN p_now ELSE NULL END
    WHERE execution.account_id=execution_row.account_id AND execution.id=execution_row.id;
    RETURN;
  END IF;
  UPDATE spyglass.integration_executions execution SET state=CASE WHEN selected_mode='execute' THEN 'executing' ELSE 'reconciling' END,
    attempt_count=execution.attempt_count+1,current_attempt_id=p_attempt_id,last_error_code=NULL,lease_expires_at=p_lease_expires_at,next_attempt_at=NULL,updated_at=p_now,completed_at=NULL
  WHERE execution.account_id=execution_row.account_id AND execution.id=execution_row.id;
  INSERT INTO spyglass.integration_execution_attempts(account_id,execution_id,id,attempt_number,mode,started_at,lease_expires_at)
  VALUES (execution_row.account_id,execution_row.id,p_attempt_id,execution_row.attempt_count+1,selected_mode,p_now,p_lease_expires_at);
  RETURN QUERY SELECT execution_row.account_id,execution_row.id,p_attempt_id,selected_mode,execution_row.capability,execution_row.release_id,
    execution_row.release_version,execution_row.approval_id,execution_row.connection_id,execution_row.connection_revision_id,
    execution_row.connection_revision,execution_row.credential_id,execution_row.credential_generation,execution_row.payload_sha256,
    execution_row.id,p_lease_expires_at;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_claim_integration_execution(uuid,timestamptz,timestamptz) FROM PUBLIC;

COMMIT;
