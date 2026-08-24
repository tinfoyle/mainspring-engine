BEGIN;

CREATE TABLE spyglass.integration_authorization_workflows (
    account_id uuid NOT NULL,
    session_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_version bigint NOT NULL CHECK (connection_version>0),
    state text NOT NULL CHECK (state IN ('claimed','provider_exchanging','credential_stored','previous_fenced','completed','failed')),
    target_credential_id uuid NOT NULL,
    target_generation bigint NOT NULL CHECK (target_generation>0),
    reference_sha256 bytea NOT NULL CHECK (octet_length(reference_sha256)=32),
    previous_credential_id uuid,
    previous_generation bigint NOT NULL DEFAULT 0 CHECK (previous_generation>=0),
    code_sha256 bytea NOT NULL CHECK (octet_length(code_sha256)=32),
    exchange_started_at timestamptz,
    failure_code text CHECK (failure_code IS NULL OR failure_code ~ '^[a-z][a-z0-9_.]{0,99}$'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,session_id),
    UNIQUE (account_id,target_credential_id,target_generation),
    FOREIGN KEY (account_id,session_id) REFERENCES spyglass.integration_authorization_sessions(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_id) REFERENCES spyglass.integration_connections(account_id,id) ON DELETE CASCADE,
    CHECK ((previous_credential_id IS NULL AND previous_generation=0) OR (previous_credential_id IS NOT NULL AND previous_generation>0)),
    CHECK ((state='claimed' AND exchange_started_at IS NULL AND failure_code IS NULL) OR
           (state IN ('provider_exchanging','credential_stored','previous_fenced','completed') AND exchange_started_at IS NOT NULL AND failure_code IS NULL) OR
           (state='failed' AND failure_code IS NOT NULL))
);

CREATE INDEX integration_authorization_workflows_connection
  ON spyglass.integration_authorization_workflows(account_id,connection_id,state,created_at,session_id);

ALTER TABLE spyglass.integration_authorization_workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.integration_authorization_workflows FORCE ROW LEVEL SECURITY;
CREATE POLICY integration_authorization_workflows_isolation ON spyglass.integration_authorization_workflows
  USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
  WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.protect_integration_authorization_workflow() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Integration authorization workflow deletion is prohibited';
  END IF;
  IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.session_id IS DISTINCT FROM OLD.session_id OR
     NEW.connection_id IS DISTINCT FROM OLD.connection_id OR NEW.connection_version IS DISTINCT FROM OLD.connection_version OR
     NEW.target_credential_id IS DISTINCT FROM OLD.target_credential_id OR NEW.target_generation IS DISTINCT FROM OLD.target_generation OR
     NEW.reference_sha256 IS DISTINCT FROM OLD.reference_sha256 OR NEW.previous_credential_id IS DISTINCT FROM OLD.previous_credential_id OR
     NEW.previous_generation IS DISTINCT FROM OLD.previous_generation OR NEW.code_sha256 IS DISTINCT FROM OLD.code_sha256 OR
     NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.updated_at<OLD.updated_at OR
     (OLD.state='claimed' AND NEW.state NOT IN ('provider_exchanging','failed')) OR
     (OLD.state='provider_exchanging' AND NEW.state NOT IN ('credential_stored','failed')) OR
     (OLD.state='credential_stored' AND NEW.state NOT IN ('previous_fenced','completed')) OR
     (OLD.state='previous_fenced' AND NEW.state<>'completed') OR OLD.state IN ('completed','failed') THEN
    RAISE EXCEPTION 'Integration authorization workflow transition is invalid';
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.fence_connection_during_authorization() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_setting('spyglass.integration_authorization_completion',true)='on' THEN RETURN NEW; END IF;
  IF (NEW.state,NEW.current_revision,NEW.credential_id,NEW.credential_generation) IS DISTINCT FROM
     (OLD.state,OLD.current_revision,OLD.credential_id,OLD.credential_generation) AND EXISTS (
       SELECT 1 FROM spyglass.integration_authorization_workflows workflow
       WHERE workflow.account_id=OLD.account_id AND workflow.connection_id=OLD.id
         AND workflow.state NOT IN ('completed','failed')
     ) THEN
    RAISE EXCEPTION 'Integration connection has an active authorization workflow';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER integration_authorization_workflows_guard
BEFORE UPDATE OR DELETE ON spyglass.integration_authorization_workflows
FOR EACH ROW EXECUTE FUNCTION spyglass.protect_integration_authorization_workflow();
CREATE TRIGGER integration_authorization_workflows_erasure_count
BEFORE DELETE ON spyglass.integration_authorization_workflows
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_integration_erasure_count();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.integration_authorization_workflows
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER integration_connection_authorization_fence
BEFORE UPDATE ON spyglass.integration_connections
FOR EACH ROW EXECUTE FUNCTION spyglass.fence_connection_during_authorization();

REVOKE ALL ON FUNCTION spyglass.protect_integration_authorization_workflow() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.fence_connection_during_authorization() FROM PUBLIC;

COMMIT;
