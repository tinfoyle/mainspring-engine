BEGIN;

CREATE TABLE spyglass.baseline_source_grants (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    assessment_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    source_kind text NOT NULL CHECK (source_kind IN ('email','google_drive')),
    folders text[] NOT NULL CHECK (cardinality(folders) BETWEEN 1 AND 50 AND array_position(folders,NULL) IS NULL),
    since_at timestamptz,
    until_at timestamptz,
    state text NOT NULL CHECK (state IN ('active','revoked')),
    granted_by_user_id uuid NOT NULL,
    revoked_by_user_id uuid,
    revoke_reason text NOT NULL DEFAULT '' CHECK (char_length(revoke_reason)<=1000),
    version bigint NOT NULL CHECK (version IN (1,2)),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    revoked_at timestamptz,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,assessment_id) REFERENCES spyglass.baseline_assessments(account_id,id) ON DELETE CASCADE,
    CHECK ((source_kind='email' AND cardinality(folders)<=20 AND (since_at IS NULL OR until_at IS NULL OR since_at<=until_at)) OR
           (source_kind='google_drive' AND since_at IS NULL AND until_at IS NULL)),
    CHECK ((state='active' AND version=1 AND revoked_by_user_id IS NULL AND revoke_reason='' AND revoked_at IS NULL AND updated_at=created_at) OR
           (state='revoked' AND version=2 AND revoked_by_user_id IS NOT NULL AND char_length(btrim(revoke_reason)) BETWEEN 3 AND 1000 AND revoked_at>=created_at AND updated_at=revoked_at))
);

CREATE UNIQUE INDEX baseline_one_active_connection_grant ON spyglass.baseline_source_grants(account_id,source_kind,connection_id) WHERE state='active';
CREATE INDEX baseline_source_grants_list ON spyglass.baseline_source_grants(account_id,assessment_id,id);

CREATE TABLE spyglass.baseline_source_grant_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    grant_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('source_granted','source_revoked')),
    from_version bigint NOT NULL CHECK (from_version>=0),
    to_version bigint NOT NULL CHECK (to_version=from_version+1),
    actor_user_id uuid NOT NULL,
    reason_code text NOT NULL CHECK (reason_code IN ('source_granted','source_revoked')),
    correlation_id uuid NOT NULL,
    redacted_payload jsonb NOT NULL CHECK (jsonb_typeof(redacted_payload)='object' AND octet_length(redacted_payload::text)<=1024),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,grant_id) REFERENCES spyglass.baseline_source_grants(account_id,id) ON DELETE CASCADE
);

ALTER TABLE spyglass.baseline_source_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_source_grants FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_source_grant_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.baseline_source_grant_events FORCE ROW LEVEL SECURITY;
CREATE POLICY baseline_source_grants_isolation ON spyglass.baseline_source_grants USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY baseline_source_grant_events_isolation ON spyglass.baseline_source_grant_events USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.protect_baseline_source_grant() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.assessment_id IS DISTINCT FROM OLD.assessment_id OR
       NEW.connection_id IS DISTINCT FROM OLD.connection_id OR NEW.source_kind IS DISTINCT FROM OLD.source_kind OR NEW.folders IS DISTINCT FROM OLD.folders OR
       NEW.since_at IS DISTINCT FROM OLD.since_at OR NEW.until_at IS DISTINCT FROM OLD.until_at OR NEW.granted_by_user_id IS DISTINCT FROM OLD.granted_by_user_id OR
       NEW.created_at IS DISTINCT FROM OLD.created_at OR OLD.state<>'active' OR NEW.state<>'revoked' OR OLD.version<>1 OR NEW.version<>2 THEN
        RAISE EXCEPTION 'Baseline source grant scope is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER baseline_source_grant_guard BEFORE UPDATE ON spyglass.baseline_source_grants FOR EACH ROW EXECUTE FUNCTION spyglass.protect_baseline_source_grant();
CREATE TRIGGER baseline_source_grants_immutable_delete BEFORE DELETE ON spyglass.baseline_source_grants FOR EACH ROW EXECUTE FUNCTION spyglass.reject_baseline_history_change();
CREATE TRIGGER baseline_source_grant_events_immutable BEFORE UPDATE OR DELETE ON spyglass.baseline_source_grant_events FOR EACH ROW EXECUTE FUNCTION spyglass.reject_baseline_history_change();
CREATE TRIGGER baseline_source_grants_erasure_count BEFORE DELETE ON spyglass.baseline_source_grants FOR EACH ROW EXECUTE FUNCTION spyglass.capture_baseline_erasure_count();
CREATE TRIGGER baseline_source_grant_events_erasure_count BEFORE DELETE ON spyglass.baseline_source_grant_events FOR EACH ROW EXECUTE FUNCTION spyglass.capture_baseline_erasure_count();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.baseline_source_grants FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.baseline_source_grant_events FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

REVOKE ALL ON FUNCTION spyglass.protect_baseline_source_grant() FROM PUBLIC;

COMMIT;
