BEGIN;

CREATE TABLE spyglass.integration_authorization_sessions (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_revision_id uuid NOT NULL,
    provider text NOT NULL CHECK (provider='google_oauth'),
    requested_scope text NOT NULL CHECK (requested_scope='https://www.googleapis.com/auth/drive.readonly'),
    scope_revision_sha256 bytea NOT NULL CHECK (octet_length(scope_revision_sha256)=32),
    redirect_uri text NOT NULL CHECK (octet_length(redirect_uri) BETWEEN 1 AND 2048 AND redirect_uri=btrim(redirect_uri)),
    state_sha256 bytea NOT NULL CHECK (octet_length(state_sha256)=32),
    pkce_challenge_sha256 bytea NOT NULL CHECK (octet_length(pkce_challenge_sha256)=32),
    status text NOT NULL CHECK (status IN ('pending','exchanging','completed','failed','expired')),
    version bigint NOT NULL CHECK (version BETWEEN 1 AND 3),
    created_by_user_id uuid NOT NULL,
    credential_id uuid,
    credential_generation bigint NOT NULL DEFAULT 0 CHECK (credential_generation>=0),
    error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_.]{0,99}$'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    expires_at timestamptz NOT NULL CHECK (expires_at>created_at AND expires_at<=created_at+interval '15 minutes'),
    claimed_at timestamptz,
    completed_at timestamptz,
    PRIMARY KEY (account_id,id),
    UNIQUE (state_sha256),
    FOREIGN KEY (account_id,connection_id) REFERENCES spyglass.integration_connections(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_revision_id)
      REFERENCES spyglass.integration_connection_revisions(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_id,credential_id,credential_generation)
      REFERENCES spyglass.integration_credentials(account_id,connection_id,id,generation) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    CHECK (
      (status='pending' AND version=1 AND updated_at=created_at AND claimed_at IS NULL AND completed_at IS NULL AND
       credential_id IS NULL AND credential_generation=0 AND error_code IS NULL) OR
      (status='exchanging' AND version=2 AND claimed_at=updated_at AND claimed_at<expires_at AND completed_at IS NULL AND
       credential_id IS NULL AND credential_generation=0 AND error_code IS NULL) OR
      (status='completed' AND version=3 AND claimed_at IS NOT NULL AND completed_at=updated_at AND completed_at>=claimed_at AND
       credential_id IS NOT NULL AND credential_generation>0 AND error_code IS NULL) OR
      (status='failed' AND version=3 AND claimed_at IS NOT NULL AND completed_at=updated_at AND completed_at>=claimed_at AND
       credential_id IS NULL AND credential_generation=0 AND error_code IS NOT NULL AND error_code<>'authorization_expired') OR
      (status='expired' AND version=2 AND claimed_at IS NULL AND completed_at=updated_at AND completed_at>=expires_at AND
       credential_id IS NULL AND credential_generation=0 AND error_code='authorization_expired')
    )
);

CREATE TABLE spyglass.integration_authorization_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    session_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('authorization_started','authorization_exchange_claimed','authorization_completed','authorization_failed','authorization_expired')),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user','provider_callback','workload')),
    actor_id text NOT NULL CHECK (octet_length(actor_id) BETWEEN 1 AND 200 AND actor_id=btrim(actor_id)),
    correlation_id uuid NOT NULL,
    redacted_payload jsonb NOT NULL CHECK (jsonb_typeof(redacted_payload)='object' AND octet_length(redacted_payload::text)<=2048 AND
      NOT (redacted_payload ?| ARRAY['state','code','verifier','credential','secret','token','payload','content','provider_response'])),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,session_id) REFERENCES spyglass.integration_authorization_sessions(account_id,id) ON DELETE CASCADE
);

CREATE INDEX integration_authorization_sessions_connection
  ON spyglass.integration_authorization_sessions(account_id,connection_id,created_at DESC,id);
CREATE INDEX integration_authorization_sessions_status
  ON spyglass.integration_authorization_sessions(account_id,status,expires_at,id);
CREATE INDEX integration_authorization_events_session
  ON spyglass.integration_authorization_events(account_id,session_id,occurred_at,id);

ALTER TABLE spyglass.integration_authorization_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.integration_authorization_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY integration_authorization_sessions_isolation ON spyglass.integration_authorization_sessions
  USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
  WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
ALTER TABLE spyglass.integration_authorization_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.integration_authorization_events FORCE ROW LEVEL SECURITY;
CREATE POLICY integration_authorization_events_isolation ON spyglass.integration_authorization_events
  USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
  WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.validate_integration_authorization_session() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE connection_row record; revision_row record;
BEGIN
  IF current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(NEW.account_id,'INSERT') THEN RETURN NEW; END IF;
  SELECT connector_kind,state,current_revision INTO connection_row
  FROM spyglass.integration_connections
  WHERE account_id=NEW.account_id AND id=NEW.connection_id FOR SHARE;
  SELECT revision,capabilities,drive_folder_ids INTO revision_row
  FROM spyglass.integration_connection_revisions
  WHERE account_id=NEW.account_id AND connection_id=NEW.connection_id AND id=NEW.connection_revision_id FOR SHARE;
  IF connection_row.connector_kind IS DISTINCT FROM 'google_drive' OR connection_row.state='revoked' OR
     connection_row.current_revision IS DISTINCT FROM revision_row.revision OR
     revision_row.capabilities IS DISTINCT FROM ARRAY['google_drive.read']::text[] OR
     cardinality(revision_row.drive_folder_ids) NOT BETWEEN 1 AND 50 THEN
    RAISE EXCEPTION 'Integration authorization session authority is invalid';
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.protect_integration_authorization_session() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Integration authorization session deletion is prohibited';
  END IF;
  IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR
     NEW.connection_id IS DISTINCT FROM OLD.connection_id OR NEW.connection_revision_id IS DISTINCT FROM OLD.connection_revision_id OR
     NEW.provider IS DISTINCT FROM OLD.provider OR NEW.requested_scope IS DISTINCT FROM OLD.requested_scope OR
     NEW.scope_revision_sha256 IS DISTINCT FROM OLD.scope_revision_sha256 OR NEW.redirect_uri IS DISTINCT FROM OLD.redirect_uri OR
     NEW.state_sha256 IS DISTINCT FROM OLD.state_sha256 OR NEW.pkce_challenge_sha256 IS DISTINCT FROM OLD.pkce_challenge_sha256 OR
     NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR NEW.created_at IS DISTINCT FROM OLD.created_at OR
     NEW.expires_at IS DISTINCT FROM OLD.expires_at OR NEW.version<>OLD.version+1 OR
     (OLD.status='pending' AND NEW.status NOT IN ('exchanging','expired')) OR
     (OLD.status='exchanging' AND NEW.status NOT IN ('completed','failed')) OR
     OLD.status IN ('completed','failed','expired') THEN
    RAISE EXCEPTION 'Integration authorization session transition is invalid';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER integration_authorization_sessions_validate
BEFORE INSERT ON spyglass.integration_authorization_sessions
FOR EACH ROW EXECUTE FUNCTION spyglass.validate_integration_authorization_session();
CREATE TRIGGER integration_authorization_sessions_guard
BEFORE UPDATE OR DELETE ON spyglass.integration_authorization_sessions
FOR EACH ROW EXECUTE FUNCTION spyglass.protect_integration_authorization_session();
CREATE TRIGGER integration_authorization_events_immutable
BEFORE UPDATE OR DELETE ON spyglass.integration_authorization_events
FOR EACH ROW EXECUTE FUNCTION spyglass.reject_integration_immutable_change();
CREATE TRIGGER integration_authorization_sessions_erasure_count
BEFORE DELETE ON spyglass.integration_authorization_sessions
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_integration_erasure_count();
CREATE TRIGGER integration_authorization_events_erasure_count
BEFORE DELETE ON spyglass.integration_authorization_events
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_integration_erasure_count();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.integration_authorization_sessions
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.integration_authorization_events
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

REVOKE ALL ON FUNCTION spyglass.validate_integration_authorization_session() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_integration_authorization_session() FROM PUBLIC;

COMMIT;
