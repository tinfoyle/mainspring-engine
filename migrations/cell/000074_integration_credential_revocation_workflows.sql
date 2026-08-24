BEGIN;

CREATE TABLE spyglass.integration_credential_revocation_workflows (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_version bigint NOT NULL CHECK (connection_version>0),
    credential_id uuid NOT NULL,
    credential_generation bigint NOT NULL CHECK (credential_generation>0),
    provider text NOT NULL CHECK (provider='google_oauth'),
    reference_sha256 bytea NOT NULL CHECK (octet_length(reference_sha256)=32),
    state text NOT NULL CHECK (state IN ('prepared','provider_revoking','provider_revoked','vault_fenced','completed')),
    created_by_user_id uuid NOT NULL,
    provider_started_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,connection_id,id),
    FOREIGN KEY (account_id,connection_id) REFERENCES spyglass.integration_connections(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_id,credential_id,credential_generation)
      REFERENCES spyglass.integration_credentials(account_id,connection_id,id,generation) ON DELETE CASCADE,
    CHECK ((state='prepared' AND provider_started_at IS NULL) OR
           (state IN ('provider_revoking','provider_revoked','vault_fenced','completed') AND provider_started_at IS NOT NULL))
);

CREATE UNIQUE INDEX integration_one_open_credential_revocation
  ON spyglass.integration_credential_revocation_workflows(account_id,connection_id)
  WHERE state<>'completed';

ALTER TABLE spyglass.integration_credential_revocation_workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.integration_credential_revocation_workflows FORCE ROW LEVEL SECURITY;
CREATE POLICY integration_credential_revocation_workflows_isolation ON spyglass.integration_credential_revocation_workflows
  USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
  WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.protect_integration_credential_revocation_workflow() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Integration credential revocation workflow deletion is prohibited';
  END IF;
  IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR
     NEW.connection_id IS DISTINCT FROM OLD.connection_id OR NEW.connection_version IS DISTINCT FROM OLD.connection_version OR
     NEW.credential_id IS DISTINCT FROM OLD.credential_id OR NEW.credential_generation IS DISTINCT FROM OLD.credential_generation OR
     NEW.provider IS DISTINCT FROM OLD.provider OR NEW.reference_sha256 IS DISTINCT FROM OLD.reference_sha256 OR
     NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR NEW.created_at IS DISTINCT FROM OLD.created_at OR
     NEW.updated_at<OLD.updated_at OR
     (OLD.state='prepared' AND NEW.state<>'provider_revoking') OR
     (OLD.state='provider_revoking' AND NEW.state<>'provider_revoked') OR
     (OLD.state='provider_revoked' AND NEW.state<>'vault_fenced') OR
     (OLD.state='vault_fenced' AND NEW.state<>'completed') OR OLD.state='completed' THEN
    RAISE EXCEPTION 'Integration credential revocation workflow transition is invalid';
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION spyglass.fence_connection_during_authorization() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_setting('spyglass.integration_authorization_completion',true)='on' OR
     current_setting('spyglass.integration_revocation_completion',true)='on' THEN RETURN NEW; END IF;
  IF (NEW.state,NEW.current_revision,NEW.credential_id,NEW.credential_generation) IS DISTINCT FROM
     (OLD.state,OLD.current_revision,OLD.credential_id,OLD.credential_generation) AND (
       EXISTS (SELECT 1 FROM spyglass.integration_authorization_workflows workflow
         WHERE workflow.account_id=OLD.account_id AND workflow.connection_id=OLD.id AND workflow.state NOT IN ('completed','failed')) OR
       EXISTS (SELECT 1 FROM spyglass.integration_credential_revocation_workflows workflow
         WHERE workflow.account_id=OLD.account_id AND workflow.connection_id=OLD.id AND workflow.state<>'completed')
     ) THEN
    RAISE EXCEPTION 'Integration connection has an active credential workflow';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER integration_credential_revocation_workflows_guard
BEFORE UPDATE OR DELETE ON spyglass.integration_credential_revocation_workflows
FOR EACH ROW EXECUTE FUNCTION spyglass.protect_integration_credential_revocation_workflow();
CREATE TRIGGER integration_credential_revocation_workflows_erasure_count
BEFORE DELETE ON spyglass.integration_credential_revocation_workflows
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_integration_erasure_count();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.integration_credential_revocation_workflows
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

REVOKE ALL ON FUNCTION spyglass.protect_integration_credential_revocation_workflow() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.fence_connection_during_authorization() FROM PUBLIC;

COMMIT;
