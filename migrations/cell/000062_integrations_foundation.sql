BEGIN;

-- Approved Marketing history is immutable during normal operation, but all of
-- it must leave with the Account. The original RESTRICT edge made whole-Account
-- erasure depend on PostgreSQL cascade order once an approved release existed.
ALTER TABLE spyglass.marketing_release_plans
  DROP CONSTRAINT marketing_release_plans_account_id_approval_id_fkey,
  ADD CONSTRAINT marketing_release_plans_account_id_approval_id_fkey
    FOREIGN KEY (account_id,approval_id) REFERENCES spyglass.attention_consequential_approvals(account_id,id) ON DELETE CASCADE;

CREATE TABLE spyglass.integration_connections (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 160 AND name=btrim(name)),
    connector_kind text NOT NULL CHECK (connector_kind IN ('email','google_drive','web_research','web_publish')),
    state text NOT NULL CHECK (state IN ('pending','active','disabled','revoked')),
    current_revision bigint NOT NULL CHECK (current_revision>0),
    credential_id uuid,
    credential_generation bigint NOT NULL DEFAULT 0 CHECK (credential_generation>=0),
    version bigint NOT NULL CHECK (version>0),
    created_by_user_id uuid NOT NULL,
    revoked_by_user_id uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    revoked_at timestamptz,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    CHECK (
      (state='pending' AND credential_id IS NULL AND credential_generation=0 AND revoked_by_user_id IS NULL AND revoked_at IS NULL) OR
      (state IN ('active','disabled') AND credential_id IS NOT NULL AND credential_generation>0 AND revoked_by_user_id IS NULL AND revoked_at IS NULL) OR
      (state='revoked' AND ((credential_id IS NULL AND credential_generation=0) OR (credential_id IS NOT NULL AND credential_generation>0)) AND
       revoked_by_user_id IS NOT NULL AND revoked_at=updated_at AND revoked_at>=created_at)
    )
);

CREATE TABLE spyglass.integration_connection_revisions (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    connection_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    capabilities text[] NOT NULL CHECK (cardinality(capabilities) BETWEEN 1 AND 4 AND array_position(capabilities,NULL) IS NULL),
    email_address text NOT NULL DEFAULT '' CHECK (octet_length(email_address)<=320 AND email_address=btrim(email_address)),
    audience_reference text NOT NULL DEFAULT '' CHECK (octet_length(audience_reference)<=500 AND audience_reference=btrim(audience_reference)),
    https_origin text NOT NULL DEFAULT '' CHECK (octet_length(https_origin)<=500 AND https_origin=btrim(https_origin)),
    path_prefix text NOT NULL DEFAULT '' CHECK (octet_length(path_prefix)<=500 AND path_prefix=btrim(path_prefix)),
    created_by_user_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,connection_id,revision),
    UNIQUE (account_id,connection_id,id,revision),
    FOREIGN KEY (account_id,connection_id) REFERENCES spyglass.integration_connections(account_id,id) ON DELETE CASCADE,
    CHECK (
      (capabilities IN (ARRAY['email.read']::text[],ARRAY['email.send']::text[],ARRAY['email.read','email.send']::text[]) AND
       email_address ~ '^[^[:space:]@]+@[^[:space:]@]+$' AND
       (NOT capabilities @> ARRAY['email.send']::text[] OR octet_length(audience_reference) BETWEEN 1 AND 500) AND
       https_origin='' AND path_prefix='') OR
      (capabilities=ARRAY['web.publish']::text[] AND email_address='' AND audience_reference='' AND
       https_origin ~ '^https://[^/?#]+$' AND path_prefix ~ '^/([^/]|$)' AND path_prefix !~ '/\\.\\.?(/|$)')
    )
);

CREATE TABLE spyglass.integration_credentials (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    connection_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation>0),
    provider text NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 64 AND provider ~ '^[a-z][a-z0-9_.]{0,63}$'),
    reference_sha256 bytea NOT NULL CHECK (octet_length(reference_sha256)=32),
    state text NOT NULL CHECK (state IN ('active','rotated','revoked')),
    created_by_user_id uuid NOT NULL,
    ended_by_user_id uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    expires_at timestamptz CHECK (expires_at IS NULL OR expires_at>created_at),
    ended_at timestamptz,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,connection_id,generation),
    UNIQUE (account_id,connection_id,id,generation),
    FOREIGN KEY (account_id,connection_id) REFERENCES spyglass.integration_connections(account_id,id) ON DELETE CASCADE,
    CHECK ((state='active' AND ended_by_user_id IS NULL AND ended_at IS NULL AND updated_at=created_at) OR
           (state IN ('rotated','revoked') AND ended_by_user_id IS NOT NULL AND ended_at=updated_at AND ended_at>=created_at))
);
CREATE UNIQUE INDEX integration_one_active_credential
    ON spyglass.integration_credentials(account_id,connection_id) WHERE state='active';

CREATE TABLE spyglass.integration_health_observations (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_revision bigint NOT NULL,
    credential_id uuid NOT NULL,
    credential_generation bigint NOT NULL,
    state text NOT NULL CHECK (state IN ('healthy','degraded','unavailable')),
    error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_.]{0,99}$'),
    latency_milliseconds integer NOT NULL CHECK (latency_milliseconds BETWEEN 0 AND 300000),
    checked_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,connection_id,connection_revision)
      REFERENCES spyglass.integration_connection_revisions(account_id,connection_id,revision) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_id,credential_id,credential_generation)
      REFERENCES spyglass.integration_credentials(account_id,connection_id,id,generation) ON DELETE CASCADE,
    CHECK ((state='healthy' AND error_code IS NULL) OR (state IN ('degraded','unavailable') AND error_code IS NOT NULL))
);

CREATE TABLE spyglass.integration_executions (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    release_id uuid NOT NULL,
    release_version bigint NOT NULL CHECK (release_version>0),
    approval_id uuid NOT NULL,
    capability text NOT NULL CHECK (capability IN ('email.send','web.publish')),
    connection_id uuid NOT NULL,
    connection_revision_id uuid NOT NULL,
    connection_revision bigint NOT NULL CHECK (connection_revision>0),
    credential_id uuid NOT NULL,
    credential_generation bigint NOT NULL CHECK (credential_generation>0),
    payload_sha256 bytea NOT NULL CHECK (octet_length(payload_sha256)=32),
    state text NOT NULL CHECK (state IN ('prepared','executing','reconciling','retry_wait','unknown','manual_resolution','succeeded','failed','cancelled')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 3),
    current_attempt_id uuid,
    last_error_code text CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_.]{0,99}$'),
    lease_expires_at timestamptz,
    next_attempt_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    completed_at timestamptz,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,release_id,capability),
    FOREIGN KEY (account_id,release_id) REFERENCES spyglass.marketing_release_plans(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,approval_id) REFERENCES spyglass.attention_consequential_approvals(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_id) REFERENCES spyglass.integration_connections(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_id,connection_revision_id,connection_revision)
      REFERENCES spyglass.integration_connection_revisions(account_id,connection_id,id,revision) ON DELETE CASCADE,
    FOREIGN KEY (account_id,connection_id,credential_id,credential_generation)
      REFERENCES spyglass.integration_credentials(account_id,connection_id,id,generation) ON DELETE CASCADE,
    CHECK (
      (state='prepared' AND attempt_count=0 AND current_attempt_id IS NULL AND last_error_code IS NULL AND lease_expires_at IS NULL AND next_attempt_at IS NULL AND completed_at IS NULL) OR
      (state IN ('executing','reconciling') AND attempt_count BETWEEN 1 AND 3 AND current_attempt_id IS NOT NULL AND last_error_code IS NULL AND lease_expires_at>updated_at AND next_attempt_at IS NULL AND completed_at IS NULL) OR
      (state='retry_wait' AND attempt_count BETWEEN 1 AND 2 AND current_attempt_id IS NULL AND last_error_code IS NOT NULL AND lease_expires_at IS NULL AND next_attempt_at>updated_at AND completed_at IS NULL) OR
      (state='unknown' AND attempt_count BETWEEN 1 AND 2 AND current_attempt_id IS NULL AND last_error_code IS NOT NULL AND lease_expires_at IS NULL AND next_attempt_at IS NULL AND completed_at IS NULL) OR
      (state='manual_resolution' AND attempt_count BETWEEN 1 AND 3 AND current_attempt_id IS NULL AND last_error_code IS NOT NULL AND lease_expires_at IS NULL AND next_attempt_at IS NULL AND completed_at IS NULL) OR
      (state='succeeded' AND attempt_count BETWEEN 1 AND 3 AND current_attempt_id IS NULL AND last_error_code IS NULL AND lease_expires_at IS NULL AND next_attempt_at IS NULL AND completed_at=updated_at) OR
      (state IN ('failed','cancelled') AND current_attempt_id IS NULL AND last_error_code IS NOT NULL AND lease_expires_at IS NULL AND next_attempt_at IS NULL AND completed_at=updated_at)
    )
);

CREATE TABLE spyglass.integration_execution_attempts (
    account_id uuid NOT NULL,
    execution_id uuid NOT NULL,
    id uuid NOT NULL,
    attempt_number integer NOT NULL CHECK (attempt_number BETWEEN 1 AND 3),
    mode text NOT NULL CHECK (mode IN ('execute','reconcile')),
    outcome text CHECK (outcome IS NULL OR outcome IN ('succeeded','not_applied','failed','unknown')),
    error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_.]{0,99}$'),
    started_at timestamptz NOT NULL,
    lease_expires_at timestamptz NOT NULL CHECK (lease_expires_at>started_at),
    completed_at timestamptz,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,execution_id,attempt_number),
    FOREIGN KEY (account_id,execution_id) REFERENCES spyglass.integration_executions(account_id,id) ON DELETE CASCADE,
    CHECK ((outcome IS NULL AND error_code IS NULL AND completed_at IS NULL) OR
           (outcome='succeeded' AND error_code IS NULL AND completed_at>=started_at) OR
           (outcome IN ('not_applied','failed','unknown') AND error_code IS NOT NULL AND completed_at>=started_at))
);

CREATE TABLE spyglass.integration_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    aggregate_kind text NOT NULL CHECK (aggregate_kind IN ('connection','credential','execution')),
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('connection_created','connection_revised','connection_activated','connection_disabled','connection_revoked',
      'credential_bound','credential_rotated','credential_revoked','execution_prepared','execution_succeeded','execution_failed','execution_cancelled','execution_manual_resolution')),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user','workload')),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 200 AND actor_id=btrim(actor_id)),
    correlation_id uuid NOT NULL,
    redacted_payload jsonb NOT NULL CHECK (jsonb_typeof(redacted_payload)='object' AND octet_length(redacted_payload::text)<=2048 AND
      NOT (redacted_payload ?| ARRAY['credential','secret','token','payload','content','objective','audience','recipient','subject','body','provider_response'])),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE
);

CREATE TABLE spyglass.integration_execution_queue (
    account_id uuid NOT NULL,
    execution_id uuid NOT NULL,
    available_at timestamptz NOT NULL,
    lease_expires_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,execution_id),
    FOREIGN KEY (account_id,execution_id) REFERENCES spyglass.integration_executions(account_id,id) ON DELETE CASCADE,
    CHECK (lease_expires_at IS NULL OR lease_expires_at>updated_at)
);

CREATE INDEX integration_connections_list ON spyglass.integration_connections(account_id,updated_at DESC,id);
CREATE INDEX integration_revisions_list ON spyglass.integration_connection_revisions(account_id,connection_id,revision DESC);
CREATE INDEX integration_credentials_list ON spyglass.integration_credentials(account_id,connection_id,generation DESC);
CREATE INDEX integration_health_latest ON spyglass.integration_health_observations(account_id,connection_id,checked_at DESC,id);
CREATE INDEX integration_executions_list ON spyglass.integration_executions(account_id,updated_at DESC,id);
CREATE INDEX integration_execution_attempts_list ON spyglass.integration_execution_attempts(account_id,execution_id,attempt_number,id);
CREATE INDEX integration_execution_queue_due ON spyglass.integration_execution_queue(available_at,execution_id);

DO $$ DECLARE table_name text; BEGIN
  FOREACH table_name IN ARRAY ARRAY['integration_connections','integration_connection_revisions','integration_credentials','integration_health_observations',
    'integration_executions','integration_execution_attempts','integration_events'] LOOP
    EXECUTE format('ALTER TABLE spyglass.%I ENABLE ROW LEVEL SECURITY',table_name);
    EXECUTE format('ALTER TABLE spyglass.%I FORCE ROW LEVEL SECURITY',table_name);
    EXECUTE format('CREATE POLICY %I ON spyglass.%I USING (account_id=nullif(current_setting(''app.account_id'',true),'''')::uuid) WITH CHECK (account_id=nullif(current_setting(''app.account_id'',true),'''')::uuid)',table_name||'_isolation',table_name);
  END LOOP;
END $$;

CREATE FUNCTION spyglass.validate_integration_connection_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE revision_row record; credential_row record;
BEGIN
  SELECT revision,created_at INTO revision_row FROM spyglass.integration_connection_revisions
  WHERE account_id=NEW.account_id AND connection_id=NEW.id AND revision=NEW.current_revision;
  IF NOT FOUND OR revision_row.created_at>NEW.updated_at THEN
    RAISE EXCEPTION 'Integration connection revision binding is invalid';
  END IF;
  IF NEW.credential_id IS NOT NULL THEN
    SELECT state,expires_at INTO credential_row FROM spyglass.integration_credentials
    WHERE account_id=NEW.account_id AND connection_id=NEW.id AND id=NEW.credential_id AND generation=NEW.credential_generation;
    IF NOT FOUND OR (NEW.state IN ('active','disabled') AND
       (credential_row.state<>'active' OR (credential_row.expires_at IS NOT NULL AND credential_row.expires_at<=NEW.updated_at))) THEN
      RAISE EXCEPTION 'Integration connection credential binding is invalid';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.protect_integration_connection() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Integration connection deletion is prohibited';
  END IF;
  IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.connector_kind IS DISTINCT FROM OLD.connector_kind OR
     NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.version<>OLD.version+1 OR
     NEW.current_revision NOT IN (OLD.current_revision,OLD.current_revision+1) OR NEW.credential_generation NOT IN (OLD.credential_generation,OLD.credential_generation+1) OR
     (NEW.credential_generation=OLD.credential_generation AND NEW.credential_id IS DISTINCT FROM OLD.credential_id) OR
     (NEW.current_revision=OLD.current_revision AND NEW.name IS DISTINCT FROM OLD.name) OR
     (OLD.state='pending' AND NEW.state NOT IN ('pending','active','revoked')) OR
     (OLD.state='active' AND NEW.state NOT IN ('active','disabled','revoked')) OR
     (OLD.state='disabled' AND NEW.state NOT IN ('disabled','active','revoked')) OR OLD.state='revoked' THEN
    RAISE EXCEPTION 'Integration connection transition is invalid';
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.validate_integration_revision_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE connection_row record; prior_revision bigint;
BEGIN
  IF current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(NEW.account_id,'INSERT') THEN RETURN NEW; END IF;
  SELECT state,current_revision,connector_kind INTO connection_row FROM spyglass.integration_connections
  WHERE account_id=NEW.account_id AND id=NEW.connection_id FOR UPDATE;
  IF NOT FOUND OR connection_row.state='revoked' THEN RAISE EXCEPTION 'Integration connection is unavailable'; END IF;
  IF (connection_row.connector_kind='email' AND NOT (NEW.capabilities <@ ARRAY['email.read','email.send']::text[])) OR
     (connection_row.connector_kind='web_publish' AND NEW.capabilities<>ARRAY['web.publish']::text[]) OR
     connection_row.connector_kind IN ('google_drive','web_research') THEN
    RAISE EXCEPTION 'Integration connection capability does not match connector kind';
  END IF;
  SELECT max(revision) INTO prior_revision FROM spyglass.integration_connection_revisions WHERE account_id=NEW.account_id AND connection_id=NEW.connection_id;
  IF NEW.revision<>COALESCE(prior_revision,0)+1 OR NEW.revision NOT IN (connection_row.current_revision,connection_row.current_revision+1) THEN
    RAISE EXCEPTION 'Integration connection revision is not sequential';
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.validate_integration_revision_binding() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_value bigint;
BEGIN
  IF current_setting('spyglass.account_movement',true)='on' THEN RETURN NEW; END IF;
  SELECT current_revision INTO current_value FROM spyglass.integration_connections WHERE account_id=NEW.account_id AND id=NEW.connection_id;
  IF current_value<>NEW.revision THEN RAISE EXCEPTION 'Integration connection did not bind the new revision'; END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.validate_integration_credential_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior_generation bigint;
BEGIN
  IF current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(NEW.account_id,'INSERT') THEN RETURN NEW; END IF;
  PERFORM 1 FROM spyglass.integration_connections WHERE account_id=NEW.account_id AND id=NEW.connection_id AND state<>'revoked' FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'Integration connection is unavailable'; END IF;
  SELECT max(generation) INTO prior_generation FROM spyglass.integration_credentials WHERE account_id=NEW.account_id AND connection_id=NEW.connection_id;
  IF NEW.generation<>COALESCE(prior_generation,0)+1 THEN RAISE EXCEPTION 'Integration credential generation is not sequential'; END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.validate_integration_credential_binding() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE connection_row record;
BEGIN
  IF current_setting('spyglass.account_movement',true)='on' THEN RETURN NEW; END IF;
  SELECT state,credential_id,credential_generation INTO connection_row FROM spyglass.integration_connections
  WHERE account_id=NEW.account_id AND id=NEW.connection_id;
  IF NOT FOUND THEN RAISE EXCEPTION 'Integration credential connection is missing'; END IF;
  IF NEW.state='active' AND NOT ((connection_row.state='pending' AND NEW.generation=1) OR
     (connection_row.state IN ('active','disabled') AND connection_row.credential_id=NEW.id AND connection_row.credential_generation=NEW.generation)) THEN
    RAISE EXCEPTION 'Active Integration credential is not bound';
  END IF;
  IF NEW.state IN ('rotated','revoked') AND connection_row.state IN ('active','disabled') AND connection_row.credential_id=NEW.id THEN
    RAISE EXCEPTION 'Ended Integration credential remains bound';
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.protect_integration_credential() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Integration credential deletion is prohibited';
  END IF;
  IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.connection_id IS DISTINCT FROM OLD.connection_id OR
     NEW.generation IS DISTINCT FROM OLD.generation OR NEW.provider IS DISTINCT FROM OLD.provider OR NEW.reference_sha256 IS DISTINCT FROM OLD.reference_sha256 OR
     NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.expires_at IS DISTINCT FROM OLD.expires_at OR
     OLD.state<>'active' OR NEW.state NOT IN ('rotated','revoked') THEN
    RAISE EXCEPTION 'Integration credential transition is invalid';
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.reject_integration_immutable_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' AND (pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE'))) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'Integration revision, observation, attempt, or event is immutable';
END;
$$;

CREATE FUNCTION spyglass.protect_integration_attempt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Integration attempt deletion is prohibited';
  END IF;
  IF OLD.outcome IS NOT NULL OR NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.execution_id IS DISTINCT FROM OLD.execution_id OR NEW.id IS DISTINCT FROM OLD.id OR
     NEW.attempt_number IS DISTINCT FROM OLD.attempt_number OR NEW.mode IS DISTINCT FROM OLD.mode OR NEW.started_at IS DISTINCT FROM OLD.started_at OR
     NEW.lease_expires_at IS DISTINCT FROM OLD.lease_expires_at OR NEW.outcome IS NULL OR NEW.completed_at IS NULL THEN
    RAISE EXCEPTION 'Integration attempt transition is invalid';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER integration_connections_guard BEFORE UPDATE OR DELETE ON spyglass.integration_connections FOR EACH ROW EXECUTE FUNCTION spyglass.protect_integration_connection();
CREATE CONSTRAINT TRIGGER integration_connection_binding AFTER INSERT OR UPDATE ON spyglass.integration_connections DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION spyglass.validate_integration_connection_binding();
CREATE TRIGGER integration_revisions_sequence BEFORE INSERT ON spyglass.integration_connection_revisions FOR EACH ROW EXECUTE FUNCTION spyglass.validate_integration_revision_insert();
CREATE CONSTRAINT TRIGGER integration_revision_binding AFTER INSERT ON spyglass.integration_connection_revisions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION spyglass.validate_integration_revision_binding();
CREATE TRIGGER integration_revisions_immutable BEFORE UPDATE OR DELETE ON spyglass.integration_connection_revisions FOR EACH ROW EXECUTE FUNCTION spyglass.reject_integration_immutable_change();
CREATE TRIGGER integration_credentials_sequence BEFORE INSERT ON spyglass.integration_credentials FOR EACH ROW EXECUTE FUNCTION spyglass.validate_integration_credential_insert();
CREATE TRIGGER integration_credentials_guard BEFORE UPDATE OR DELETE ON spyglass.integration_credentials FOR EACH ROW EXECUTE FUNCTION spyglass.protect_integration_credential();
CREATE CONSTRAINT TRIGGER integration_credential_binding AFTER INSERT OR UPDATE ON spyglass.integration_credentials DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION spyglass.validate_integration_credential_binding();
CREATE TRIGGER integration_health_immutable BEFORE UPDATE OR DELETE ON spyglass.integration_health_observations FOR EACH ROW EXECUTE FUNCTION spyglass.reject_integration_immutable_change();
CREATE TRIGGER integration_attempts_guard BEFORE UPDATE OR DELETE ON spyglass.integration_execution_attempts FOR EACH ROW EXECUTE FUNCTION spyglass.protect_integration_attempt();
CREATE TRIGGER integration_events_immutable BEFORE UPDATE OR DELETE ON spyglass.integration_events FOR EACH ROW EXECUTE FUNCTION spyglass.reject_integration_immutable_change();

CREATE FUNCTION spyglass.validate_integration_execution_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE release_row record; campaign_row record; approval_row record; connection_row record; revision_row record; credential_row record; release_channel_valid boolean;
BEGIN
  IF current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(NEW.account_id,'INSERT') THEN RETURN NEW; END IF;
  SELECT state,version,approval_id,campaign_id INTO release_row FROM spyglass.marketing_release_plans
  WHERE account_id=NEW.account_id AND id=NEW.release_id FOR SHARE;
  SELECT state,active_release_id INTO campaign_row FROM spyglass.marketing_campaigns
  WHERE account_id=NEW.account_id AND id=release_row.campaign_id FOR SHARE;
  SELECT EXISTS (SELECT 1 FROM spyglass.marketing_release_channels channel
    WHERE channel.account_id=NEW.account_id AND channel.release_id=NEW.release_id AND
      channel.channel=CASE WHEN NEW.capability='email.send' THEN 'email' ELSE 'web' END) INTO release_channel_valid;
  SELECT state,capability,expires_at INTO approval_row FROM spyglass.attention_consequential_approvals
  WHERE account_id=NEW.account_id AND id=NEW.approval_id FOR SHARE;
  SELECT state,current_revision,credential_id,credential_generation INTO connection_row FROM spyglass.integration_connections
  WHERE account_id=NEW.account_id AND id=NEW.connection_id FOR SHARE;
  SELECT capabilities INTO revision_row FROM spyglass.integration_connection_revisions
  WHERE account_id=NEW.account_id AND connection_id=NEW.connection_id AND id=NEW.connection_revision_id AND revision=NEW.connection_revision FOR SHARE;
  SELECT state,expires_at INTO credential_row FROM spyglass.integration_credentials
  WHERE account_id=NEW.account_id AND connection_id=NEW.connection_id AND id=NEW.credential_id AND generation=NEW.credential_generation FOR SHARE;
  IF release_row.state IS DISTINCT FROM 'approved' OR release_row.version IS DISTINCT FROM NEW.release_version OR release_row.approval_id IS DISTINCT FROM NEW.approval_id OR
     campaign_row.state IS DISTINCT FROM 'active' OR campaign_row.active_release_id IS DISTINCT FROM NEW.release_id OR NOT release_channel_valid OR
     approval_row.state IS DISTINCT FROM 'approved' OR approval_row.capability IS DISTINCT FROM 'marketing.release.activate' OR approval_row.expires_at IS NULL OR approval_row.expires_at<=NEW.created_at OR connection_row.state IS DISTINCT FROM 'active' OR
     connection_row.current_revision IS DISTINCT FROM NEW.connection_revision OR connection_row.credential_id IS DISTINCT FROM NEW.credential_id OR
     connection_row.credential_generation IS DISTINCT FROM NEW.credential_generation OR NOT COALESCE(revision_row.capabilities @> ARRAY[NEW.capability]::text[],false) OR
     credential_row.state IS DISTINCT FROM 'active' OR (credential_row.expires_at IS NOT NULL AND credential_row.expires_at<=NEW.created_at) THEN
    RAISE EXCEPTION 'Integration execution authority binding is invalid';
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.protect_integration_execution() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    IF pg_trigger_depth()>1 OR (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE')) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'Integration execution deletion is prohibited';
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

CREATE FUNCTION spyglass.enqueue_integration_execution() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
  INSERT INTO spyglass.integration_execution_queue(account_id,execution_id,available_at,updated_at)
  VALUES (NEW.account_id,NEW.id,NEW.created_at,NEW.created_at)
  ON CONFLICT(account_id,execution_id) DO NOTHING;
  RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.sync_integration_execution_queue() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
  IF NEW.state IN ('executing','reconciling') THEN
    INSERT INTO spyglass.integration_execution_queue(account_id,execution_id,available_at,lease_expires_at,updated_at)
    VALUES (NEW.account_id,NEW.id,NEW.lease_expires_at,NEW.lease_expires_at,NEW.updated_at)
    ON CONFLICT(account_id,execution_id) DO UPDATE SET available_at=EXCLUDED.available_at,lease_expires_at=EXCLUDED.lease_expires_at,updated_at=EXCLUDED.updated_at;
  ELSIF NEW.state='retry_wait' THEN
    INSERT INTO spyglass.integration_execution_queue(account_id,execution_id,available_at,lease_expires_at,updated_at)
    VALUES (NEW.account_id,NEW.id,NEW.next_attempt_at,NULL,NEW.updated_at)
    ON CONFLICT(account_id,execution_id) DO UPDATE SET available_at=EXCLUDED.available_at,lease_expires_at=NULL,updated_at=EXCLUDED.updated_at;
  ELSIF NEW.state='unknown' AND NEW.attempt_count<3 THEN
    INSERT INTO spyglass.integration_execution_queue(account_id,execution_id,available_at,lease_expires_at,updated_at)
    VALUES (NEW.account_id,NEW.id,NEW.updated_at+interval '30 seconds',NULL,NEW.updated_at)
    ON CONFLICT(account_id,execution_id) DO UPDATE SET available_at=EXCLUDED.available_at,lease_expires_at=NULL,updated_at=EXCLUDED.updated_at;
  ELSE
    DELETE FROM spyglass.integration_execution_queue WHERE account_id=NEW.account_id AND execution_id=NEW.id;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER integration_execution_validate BEFORE INSERT ON spyglass.integration_executions FOR EACH ROW EXECUTE FUNCTION spyglass.validate_integration_execution_insert();
CREATE TRIGGER integration_executions_guard BEFORE UPDATE OR DELETE ON spyglass.integration_executions FOR EACH ROW EXECUTE FUNCTION spyglass.protect_integration_execution();
CREATE TRIGGER integration_execution_enqueue AFTER INSERT ON spyglass.integration_executions FOR EACH ROW EXECUTE FUNCTION spyglass.enqueue_integration_execution();
CREATE TRIGGER integration_execution_queue_sync AFTER UPDATE OF state,lease_expires_at,next_attempt_at,updated_at ON spyglass.integration_executions FOR EACH ROW EXECUTE FUNCTION spyglass.sync_integration_execution_queue();

CREATE FUNCTION public.spyglass_claim_integration_execution(
  p_attempt_id uuid,p_now timestamptz,p_lease_expires_at timestamptz
) RETURNS TABLE (
  account_id uuid,execution_id uuid,attempt_id uuid,mode text,capability text,release_id uuid,release_version bigint,approval_id uuid,
  connection_id uuid,connection_revision_id uuid,connection_revision bigint,credential_id uuid,credential_generation bigint,
  payload_sha256 bytea,idempotency_key uuid,lease_expires_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE queue_row record; execution_row record; release_row record; campaign_row record; approval_row record; connection_row record; revision_row record; credential_row record;
        selected_mode text; previous_attempt uuid; effective_state text; authorization_valid boolean; release_channel_valid boolean;
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

CREATE FUNCTION public.spyglass_complete_integration_execution(
  p_account_id uuid,p_execution_id uuid,p_attempt_id uuid,p_outcome text,p_error_code text,p_retry_at timestamptz,p_completed_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE execution_row record; attempt_row record; next_state text; next_error text; next_retry timestamptz; terminal_at timestamptz;
BEGIN
  IF p_account_id IS NULL OR p_execution_id IS NULL OR p_attempt_id IS NULL OR p_outcome NOT IN ('succeeded','not_applied','failed','unknown') OR
     p_completed_at IS NULL OR (p_outcome='succeeded' AND (p_error_code IS NOT NULL OR p_retry_at IS NOT NULL)) OR
     (p_outcome<>'succeeded' AND (p_error_code IS NULL OR p_error_code !~ '^[a-z][a-z0-9_.]{0,99}$')) OR
     (p_outcome='not_applied' AND (p_retry_at IS NULL OR p_retry_at<=p_completed_at)) OR
     (p_outcome<>'not_applied' AND p_retry_at IS NOT NULL) THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Integration execution completion';
  END IF;
  PERFORM set_config('app.account_id',p_account_id::text,true);
  SELECT * INTO execution_row FROM spyglass.integration_executions
  WHERE account_id=p_account_id AND id=p_execution_id FOR UPDATE;
  IF NOT FOUND OR execution_row.state NOT IN ('executing','reconciling') OR execution_row.current_attempt_id<>p_attempt_id OR
     execution_row.lease_expires_at<p_completed_at THEN
    RAISE EXCEPTION USING ERRCODE='P2005',MESSAGE='Integration execution lease changed';
  END IF;
  SELECT * INTO attempt_row FROM spyglass.integration_execution_attempts
  WHERE account_id=p_account_id AND execution_id=p_execution_id AND id=p_attempt_id FOR UPDATE;
  IF NOT FOUND OR attempt_row.outcome IS NOT NULL OR attempt_row.mode<>(CASE WHEN execution_row.state='executing' THEN 'execute' ELSE 'reconcile' END) OR
     (p_outcome='not_applied' AND attempt_row.mode<>'reconcile') THEN
    RAISE EXCEPTION USING ERRCODE='P2005',MESSAGE='Integration execution attempt changed';
  END IF;
  UPDATE spyglass.integration_execution_attempts SET outcome=p_outcome,error_code=p_error_code,completed_at=p_completed_at
  WHERE account_id=p_account_id AND id=p_attempt_id;
  next_error:=p_error_code;
  IF p_outcome='succeeded' THEN
    next_state:='succeeded'; next_error:=NULL; terminal_at:=p_completed_at;
  ELSIF p_outcome='failed' THEN
    next_state:='failed'; terminal_at:=p_completed_at;
  ELSIF p_outcome='not_applied' THEN
    IF execution_row.attempt_count>=3 THEN next_state:='failed'; terminal_at:=p_completed_at;
    ELSE next_state:='retry_wait'; next_retry:=p_retry_at; END IF;
  ELSE
    IF execution_row.attempt_count>=3 THEN next_state:='manual_resolution'; ELSE next_state:='unknown'; END IF;
  END IF;
  UPDATE spyglass.integration_executions SET state=next_state,current_attempt_id=NULL,last_error_code=next_error,lease_expires_at=NULL,
    next_attempt_at=next_retry,updated_at=p_completed_at,completed_at=terminal_at WHERE account_id=p_account_id AND id=p_execution_id;
END;
$$;

CREATE OR REPLACE FUNCTION public.spyglass_stage_account_move(
    p_move_id uuid,p_account_id uuid,p_role text,p_expected_generation bigint,p_new_generation bigint,p_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,spyglass AS $$
DECLARE namespace record;
BEGIN
    IF p_move_id IS NULL OR p_account_id IS NULL OR p_role NOT IN ('source','destination') OR
       p_expected_generation<=0 OR p_new_generation<=0 OR p_at IS NULL THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move cell stage'; END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT placement_generation,state INTO namespace FROM spyglass.account_namespaces WHERE account_id=p_account_id FOR UPDATE;
    IF p_role='source' THEN
      IF NOT FOUND OR namespace.placement_generation<>p_expected_generation OR namespace.state NOT IN ('active','draining','moving') THEN
        RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='source Account namespace changed'; END IF;
      IF EXISTS (SELECT 1 FROM spyglass.work_capacity_release_queue
                 WHERE account_id=p_account_id AND processing_state<>'completed') OR
         EXISTS (SELECT 1 FROM spyglass.runner_invocation_queue
                 WHERE account_id=p_account_id AND processing_state NOT IN ('completed','execution_failed','canceled')) OR
         EXISTS (SELECT 1 FROM spyglass.agent_dispatch_queue
                 WHERE account_id=p_account_id AND state NOT IN ('provisioned','dead_letter')) OR
         EXISTS (SELECT 1 FROM spyglass.agent_result_projection_queue
                 WHERE account_id=p_account_id AND state NOT IN ('projected','dead_letter')) OR
         EXISTS (SELECT 1 FROM spyglass.integration_executions
                 WHERE account_id=p_account_id AND state NOT IN ('manual_resolution','succeeded','failed','cancelled')) THEN
        RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='source Account has unfinished technical work';
      END IF;
      UPDATE spyglass.account_namespaces SET state='moving' WHERE account_id=p_account_id;
      INSERT INTO spyglass.account_move_checkpoints(account_id,move_id,role,state,placement_generation,updated_at)
      VALUES(p_account_id,p_move_id,'source','frozen',p_expected_generation,p_at)
      ON CONFLICT(account_id,move_id,role) DO NOTHING;
      PERFORM 1 FROM spyglass.account_move_checkpoints WHERE account_id=p_account_id AND move_id=p_move_id AND role='source'
        AND state='frozen' AND placement_generation=p_expected_generation FOR UPDATE;
      IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='source Account move checkpoint conflicts'; END IF;
    ELSE
      IF FOUND AND (namespace.placement_generation<>p_new_generation OR namespace.state<>'moving') THEN
        RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='destination Account namespace conflicts'; END IF;
      IF NOT FOUND THEN
        INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES(p_account_id,p_new_generation,'moving',p_at);
      END IF;
      INSERT INTO spyglass.account_move_checkpoints(account_id,move_id,role,state,placement_generation,updated_at)
      VALUES(p_account_id,p_move_id,'destination','staged',p_new_generation,p_at)
      ON CONFLICT(account_id,move_id,role) DO NOTHING;
      PERFORM 1 FROM spyglass.account_move_checkpoints WHERE account_id=p_account_id AND move_id=p_move_id AND role='destination'
        AND state='staged' AND placement_generation=p_new_generation FOR UPDATE;
      IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='destination Account move checkpoint conflicts'; END IF;
    END IF;
END;
$$;

DO $$ DECLARE table_name text; BEGIN
  FOREACH table_name IN ARRAY ARRAY['integration_connections','integration_connection_revisions','integration_credentials','integration_health_observations',
    'integration_executions','integration_execution_attempts','integration_events'] LOOP
    EXECUTE format('CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.%I FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence()',table_name);
  END LOOP;
END $$;

CREATE FUNCTION spyglass.capture_integration_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts jsonb; current_count bigint;
BEGIN
  IF current_setting('spyglass.erasure_request_id',true)<>'' AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
    counts:=COALESCE(NULLIF(current_setting('spyglass.integration_erasure_counts',true),'')::jsonb,'{}'::jsonb);
    current_count:=COALESCE((counts->>TG_TABLE_NAME)::bigint,0)+1;
    PERFORM set_config('spyglass.integration_erasure_counts',(counts||jsonb_build_object(TG_TABLE_NAME,current_count))::text,true);
  END IF;
  RETURN OLD;
END;
$$;

CREATE FUNCTION spyglass.add_integration_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts text;
BEGIN
  counts:=current_setting('spyglass.integration_erasure_counts',true);
  IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
  RETURN NEW;
END;
$$;

DO $$ DECLARE table_name text; BEGIN
  FOREACH table_name IN ARRAY ARRAY['integration_connections','integration_connection_revisions','integration_credentials','integration_health_observations',
    'integration_executions','integration_execution_attempts','integration_events','integration_execution_queue'] LOOP
    EXECUTE format('CREATE TRIGGER %I BEFORE DELETE ON spyglass.%I FOR EACH ROW EXECUTE FUNCTION spyglass.capture_integration_erasure_count()',table_name||'_erasure_count',table_name);
  END LOOP;
END $$;
CREATE TRIGGER account_erasure_integration_counts BEFORE INSERT ON spyglass.account_erasure_tombstones FOR EACH ROW EXECUTE FUNCTION spyglass.add_integration_erasure_counts();

REVOKE ALL ON TABLE spyglass.integration_execution_queue FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.validate_integration_connection_binding() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_integration_connection() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.validate_integration_revision_insert() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.validate_integration_revision_binding() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.validate_integration_credential_insert() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.validate_integration_credential_binding() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_integration_credential() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.reject_integration_immutable_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_integration_attempt() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.validate_integration_execution_insert() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_integration_execution() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.enqueue_integration_execution() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.sync_integration_execution_queue() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.capture_integration_erasure_count() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.add_integration_erasure_counts() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_integration_execution(uuid,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_complete_integration_execution(uuid,uuid,uuid,text,text,timestamptz,timestamptz) FROM PUBLIC;

COMMIT;
