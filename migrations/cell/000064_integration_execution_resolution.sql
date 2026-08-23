BEGIN;

CREATE TABLE spyglass.integration_execution_resolutions (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    execution_id uuid NOT NULL,
    requested_outcome text NOT NULL CHECK (requested_outcome IN ('succeeded','failed')),
    evidence_sha256 bytea NOT NULL CHECK (octet_length(evidence_sha256)=32),
    requested_by_user_id uuid NOT NULL,
    requested_at timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('pending','applied')),
    confirmed_by_user_id uuid,
    confirmed_at timestamptz,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,execution_id)
      REFERENCES spyglass.integration_executions(account_id,id) ON DELETE CASCADE,
    CHECK ((state='pending' AND confirmed_by_user_id IS NULL AND confirmed_at IS NULL) OR
           (state='applied' AND confirmed_by_user_id IS NOT NULL AND confirmed_at IS NOT NULL AND
            confirmed_at>=requested_at AND confirmed_by_user_id<>requested_by_user_id))
);
CREATE UNIQUE INDEX integration_execution_resolution_pending
    ON spyglass.integration_execution_resolutions(account_id,execution_id) WHERE state='pending';
CREATE INDEX integration_execution_resolutions_list
    ON spyglass.integration_execution_resolutions(account_id,execution_id,requested_at DESC,id);

ALTER TABLE spyglass.integration_execution_resolutions ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.integration_execution_resolutions FORCE ROW LEVEL SECURITY;
CREATE POLICY integration_execution_resolutions_isolation ON spyglass.integration_execution_resolutions
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE TRIGGER account_namespace_write_fence
BEFORE INSERT OR UPDATE OR DELETE ON spyglass.integration_execution_resolutions
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

ALTER TABLE spyglass.integration_events DROP CONSTRAINT integration_events_event_type_check;
ALTER TABLE spyglass.integration_events ADD CHECK (event_type IN (
    'connection_created','connection_revised','connection_activated','connection_disabled','connection_revoked',
    'credential_bound','credential_rotated','credential_revoked','execution_prepared','execution_succeeded',
    'execution_failed','execution_cancelled','execution_manual_resolution','execution_resolution_requested',
    'execution_resolution_confirmed'));

CREATE FUNCTION spyglass.protect_integration_execution_resolution() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND
       spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Integration execution resolution deletion is prohibited';
  END IF;
  IF OLD.state<>'pending' OR NEW.state<>'applied' OR
     current_setting('spyglass.integration_resolution_id',true) IS DISTINCT FROM OLD.id::text OR
     NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR
     NEW.execution_id IS DISTINCT FROM OLD.execution_id OR NEW.requested_outcome IS DISTINCT FROM OLD.requested_outcome OR
     NEW.evidence_sha256 IS DISTINCT FROM OLD.evidence_sha256 OR
     NEW.requested_by_user_id IS DISTINCT FROM OLD.requested_by_user_id OR NEW.requested_at IS DISTINCT FROM OLD.requested_at THEN
    RAISE EXCEPTION 'Integration execution resolution is immutable';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER integration_execution_resolutions_guard
BEFORE UPDATE OR DELETE ON spyglass.integration_execution_resolutions
FOR EACH ROW EXECUTE FUNCTION spyglass.protect_integration_execution_resolution();

CREATE OR REPLACE FUNCTION spyglass.protect_integration_execution() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE resolution_row record;
BEGIN
  IF TG_OP='DELETE' THEN
    IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Integration execution deletion is prohibited';
  END IF;
  IF OLD.state='manual_resolution' AND NEW.state IN ('succeeded','failed') THEN
    SELECT requested_outcome,state INTO resolution_row
    FROM spyglass.integration_execution_resolutions
    WHERE account_id=OLD.account_id AND execution_id=OLD.id
      AND id=nullif(current_setting('spyglass.integration_resolution_id',true),'')::uuid;
    IF FOUND AND resolution_row.state='applied' AND resolution_row.requested_outcome=NEW.state AND
       NEW.account_id=OLD.account_id AND NEW.id=OLD.id AND NEW.release_id=OLD.release_id AND
       NEW.release_version=OLD.release_version AND NEW.approval_id=OLD.approval_id AND NEW.capability=OLD.capability AND
       NEW.connection_id=OLD.connection_id AND NEW.connection_revision_id=OLD.connection_revision_id AND
       NEW.connection_revision=OLD.connection_revision AND NEW.credential_id=OLD.credential_id AND
       NEW.credential_generation=OLD.credential_generation AND NEW.payload_sha256=OLD.payload_sha256 AND
       NEW.created_at=OLD.created_at AND NEW.attempt_count=OLD.attempt_count AND NEW.current_attempt_id IS NULL AND
       NEW.lease_expires_at IS NULL AND NEW.next_attempt_at IS NULL AND NEW.completed_at=NEW.updated_at AND
       ((NEW.state='succeeded' AND NEW.last_error_code IS NULL) OR
        (NEW.state='failed' AND NEW.last_error_code='manual_resolution_failed')) THEN RETURN NEW; END IF;
    RAISE EXCEPTION 'Integration execution resolution transition is invalid';
  END IF;
  IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.release_id IS DISTINCT FROM OLD.release_id OR
     NEW.release_version IS DISTINCT FROM OLD.release_version OR NEW.approval_id IS DISTINCT FROM OLD.approval_id OR NEW.capability IS DISTINCT FROM OLD.capability OR
     NEW.connection_id IS DISTINCT FROM OLD.connection_id OR NEW.connection_revision_id IS DISTINCT FROM OLD.connection_revision_id OR
     NEW.connection_revision IS DISTINCT FROM OLD.connection_revision OR NEW.credential_id IS DISTINCT FROM OLD.credential_id OR
     NEW.credential_generation IS DISTINCT FROM OLD.credential_generation OR NEW.payload_sha256 IS DISTINCT FROM OLD.payload_sha256 OR
     NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.attempt_count NOT IN (OLD.attempt_count,OLD.attempt_count+1) OR
     (OLD.state='prepared' AND NEW.state NOT IN ('executing','cancelled')) OR
     (OLD.state='executing' AND NEW.state NOT IN ('succeeded','failed','unknown','manual_resolution')) OR
     (OLD.state='retry_wait' AND NEW.state NOT IN ('executing','cancelled')) OR
     (OLD.state='unknown' AND NEW.state NOT IN ('reconciling','manual_resolution')) OR
     (OLD.state='reconciling' AND NEW.state NOT IN ('succeeded','failed','retry_wait','unknown','manual_resolution')) OR
     OLD.state IN ('manual_resolution','succeeded','failed','cancelled') THEN
    RAISE EXCEPTION 'Integration execution transition is invalid';
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION public.spyglass_request_integration_execution_resolution(
  p_account_id uuid,p_execution_id uuid,p_resolution_id uuid,p_requested_outcome text,p_evidence_sha256 bytea,
  p_requested_by_user_id uuid,p_requested_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE execution_row record; existing record;
BEGIN
  IF p_account_id IS NULL OR p_execution_id IS NULL OR p_resolution_id IS NULL OR
     p_requested_outcome NOT IN ('succeeded','failed') OR p_evidence_sha256 IS NULL OR octet_length(p_evidence_sha256)<>32 OR
     p_requested_by_user_id IS NULL OR p_requested_at IS NULL THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Integration execution resolution request';
  END IF;
  PERFORM set_config('app.account_id',p_account_id::text,true);
  SELECT resolution.* INTO existing FROM spyglass.integration_execution_resolutions resolution
  WHERE resolution.account_id=p_account_id AND resolution.id=p_resolution_id;
  IF FOUND THEN
    IF existing.execution_id=p_execution_id AND existing.requested_outcome=p_requested_outcome AND
       existing.evidence_sha256=p_evidence_sha256 AND existing.requested_by_user_id=p_requested_by_user_id THEN RETURN false; END IF;
    RAISE EXCEPTION USING ERRCODE='P2005',MESSAGE='Integration execution resolution request conflicts';
  END IF;
  SELECT execution.* INTO execution_row FROM spyglass.integration_executions execution
  WHERE execution.account_id=p_account_id AND execution.id=p_execution_id FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P2001',MESSAGE='Integration execution was not found'; END IF;
  IF execution_row.state<>'manual_resolution' THEN
    RAISE EXCEPTION USING ERRCODE='P2004',MESSAGE='Integration execution is not eligible for manual resolution';
  END IF;
  INSERT INTO spyglass.integration_execution_resolutions
    (account_id,id,execution_id,requested_outcome,evidence_sha256,requested_by_user_id,requested_at,state)
  VALUES (p_account_id,p_resolution_id,p_execution_id,p_requested_outcome,p_evidence_sha256,p_requested_by_user_id,p_requested_at,'pending');
  RETURN true;
END;
$$;

CREATE FUNCTION public.spyglass_confirm_integration_execution_resolution(
  p_account_id uuid,p_execution_id uuid,p_resolution_id uuid,p_confirmed_by_user_id uuid,p_confirmed_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE resolution_row record; execution_row record;
BEGIN
  IF p_account_id IS NULL OR p_execution_id IS NULL OR p_resolution_id IS NULL OR
     p_confirmed_by_user_id IS NULL OR p_confirmed_at IS NULL THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Integration execution resolution confirmation';
  END IF;
  PERFORM set_config('app.account_id',p_account_id::text,true);
  SELECT resolution.* INTO resolution_row FROM spyglass.integration_execution_resolutions resolution
  WHERE resolution.account_id=p_account_id AND resolution.id=p_resolution_id FOR UPDATE;
  IF NOT FOUND OR resolution_row.execution_id<>p_execution_id THEN
    RAISE EXCEPTION USING ERRCODE='P2001',MESSAGE='Integration execution resolution was not found';
  END IF;
  IF resolution_row.state='applied' THEN
    IF resolution_row.confirmed_by_user_id=p_confirmed_by_user_id THEN RETURN false; END IF;
    RAISE EXCEPTION USING ERRCODE='P2005',MESSAGE='Integration execution resolution confirmation conflicts';
  END IF;
  IF resolution_row.requested_by_user_id=p_confirmed_by_user_id THEN
    RAISE EXCEPTION USING ERRCODE='P2004',MESSAGE='Integration execution resolution requires a distinct confirmer';
  END IF;
  IF p_confirmed_at<resolution_row.requested_at THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='Integration execution resolution confirmation predates request';
  END IF;
  SELECT execution.* INTO execution_row FROM spyglass.integration_executions execution
  WHERE execution.account_id=p_account_id AND execution.id=p_execution_id FOR UPDATE;
  IF NOT FOUND OR execution_row.state<>'manual_resolution' THEN
    RAISE EXCEPTION USING ERRCODE='P2005',MESSAGE='Integration execution resolution state conflicts';
  END IF;
  PERFORM set_config('spyglass.integration_resolution_id',p_resolution_id::text,true);
  UPDATE spyglass.integration_execution_resolutions SET state='applied',confirmed_by_user_id=p_confirmed_by_user_id,confirmed_at=p_confirmed_at
  WHERE account_id=p_account_id AND id=p_resolution_id;
  UPDATE spyglass.integration_executions SET state=resolution_row.requested_outcome,
    last_error_code=CASE WHEN resolution_row.requested_outcome='succeeded' THEN NULL ELSE 'manual_resolution_failed' END,
    updated_at=p_confirmed_at,completed_at=p_confirmed_at WHERE account_id=p_account_id AND id=p_execution_id;
  RETURN true;
END;
$$;

CREATE TRIGGER integration_execution_resolutions_erasure_count
BEFORE DELETE ON spyglass.integration_execution_resolutions
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_integration_erasure_count();

REVOKE ALL ON FUNCTION spyglass.protect_integration_execution_resolution() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_request_integration_execution_resolution(uuid,uuid,uuid,text,bytea,uuid,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_confirm_integration_execution_resolution(uuid,uuid,uuid,uuid,timestamptz) FROM PUBLIC;

COMMIT;
