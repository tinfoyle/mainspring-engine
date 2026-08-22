BEGIN;

CREATE TABLE spyglass.prototype_migration_runs (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    manifest_sha256 bytea NOT NULL CHECK (octet_length(manifest_sha256)=32),
    source_tenant_id uuid NOT NULL,
    source_checkpoint text NOT NULL CHECK (char_length(source_checkpoint) BETWEEN 3 AND 256 AND source_checkpoint=btrim(source_checkpoint)),
    source_inventory_sha256 bytea NOT NULL CHECK (octet_length(source_inventory_sha256)=32),
    expected_documents bigint NOT NULL CHECK (expected_documents>=0),
    expected_evidence bigint NOT NULL CHECK (expected_evidence>=0),
    expected_claims bigint NOT NULL CHECK (expected_claims>=0),
    unresolved_records bigint NOT NULL CHECK (unresolved_records>=0),
    state text NOT NULL CHECK (state IN ('importing','imported','reconciled')),
    reconciliation_sha256 bytea CHECK (reconciliation_sha256 IS NULL OR octet_length(reconciliation_sha256)=32),
    version bigint NOT NULL CHECK (version>0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    imported_at timestamptz,
    reconciled_at timestamptz,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,manifest_sha256),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    CHECK ((state='importing' AND imported_at IS NULL AND reconciled_at IS NULL AND reconciliation_sha256 IS NULL) OR
           (state='imported' AND imported_at IS NOT NULL AND reconciled_at IS NULL AND reconciliation_sha256 IS NULL) OR
           (state='reconciled' AND imported_at IS NOT NULL AND reconciled_at IS NOT NULL AND reconciliation_sha256 IS NOT NULL))
);

CREATE TABLE spyglass.prototype_migration_receipts (
    account_id uuid NOT NULL,
    run_id uuid NOT NULL,
    source_kind text NOT NULL CHECK (source_kind IN ('document_revision','fact')),
    source_id text NOT NULL CHECK (char_length(source_id) BETWEEN 1 AND 256 AND source_id=btrim(source_id)),
    target_kind text NOT NULL CHECK (target_kind IN ('document','document_revision','evidence','claim')),
    target_id uuid NOT NULL,
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256)=32),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,run_id,source_kind,source_id,target_kind),
    UNIQUE (account_id,run_id,target_kind,target_id),
    FOREIGN KEY (account_id,run_id) REFERENCES spyglass.prototype_migration_runs(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE
);

CREATE TABLE spyglass.prototype_migration_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    run_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('migration_started','target_imported','migration_imported','migration_reconciled')),
    source_kind text,
    source_id text,
    target_kind text,
    target_id uuid,
    from_version bigint NOT NULL CHECK (from_version>=0),
    to_version bigint NOT NULL CHECK (to_version=from_version+1),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 256 AND actor_id=btrim(actor_id)),
    correlation_id uuid NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,run_id) REFERENCES spyglass.prototype_migration_runs(account_id,id) ON DELETE CASCADE,
    CHECK ((event_type='target_imported' AND source_kind IS NOT NULL AND source_id IS NOT NULL AND target_kind IS NOT NULL AND target_id IS NOT NULL) OR
           (event_type<>'target_imported' AND source_kind IS NULL AND source_id IS NULL AND target_kind IS NULL AND target_id IS NULL))
);

CREATE INDEX prototype_migration_runs_state ON spyglass.prototype_migration_runs(account_id,state,updated_at,id);
CREATE INDEX prototype_migration_receipts_target ON spyglass.prototype_migration_receipts(account_id,target_kind,target_id);
CREATE INDEX prototype_migration_events_run ON spyglass.prototype_migration_events(account_id,run_id,occurred_at,id);

ALTER TABLE spyglass.prototype_migration_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.prototype_migration_runs FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.prototype_migration_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.prototype_migration_receipts FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.prototype_migration_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.prototype_migration_events FORCE ROW LEVEL SECURITY;

CREATE POLICY prototype_migration_runs_isolation ON spyglass.prototype_migration_runs
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY prototype_migration_receipts_isolation ON spyglass.prototype_migration_receipts
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY prototype_migration_events_isolation ON spyglass.prototype_migration_events
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.protect_prototype_migration_run() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR
       NEW.manifest_sha256 IS DISTINCT FROM OLD.manifest_sha256 OR NEW.source_tenant_id IS DISTINCT FROM OLD.source_tenant_id OR
       NEW.source_checkpoint IS DISTINCT FROM OLD.source_checkpoint OR NEW.source_inventory_sha256 IS DISTINCT FROM OLD.source_inventory_sha256 OR
       NEW.expected_documents IS DISTINCT FROM OLD.expected_documents OR NEW.expected_evidence IS DISTINCT FROM OLD.expected_evidence OR
       NEW.expected_claims IS DISTINCT FROM OLD.expected_claims OR NEW.unresolved_records IS DISTINCT FROM OLD.unresolved_records OR
       NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'prototype migration identity is immutable';
    END IF;
    IF OLD.state='importing' AND NEW.state NOT IN ('importing','imported') THEN RAISE EXCEPTION 'invalid prototype migration transition'; END IF;
    IF OLD.state='imported' AND NEW.state NOT IN ('imported','reconciled') THEN RAISE EXCEPTION 'invalid prototype migration transition'; END IF;
    IF OLD.state='reconciled' AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'reconciled prototype migration is immutable'; END IF;
    IF NEW.version<>OLD.version+1 OR NEW.updated_at<OLD.updated_at THEN RAISE EXCEPTION 'invalid prototype migration version'; END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER prototype_migration_runs_guard BEFORE UPDATE ON spyglass.prototype_migration_runs
FOR EACH ROW EXECUTE FUNCTION spyglass.protect_prototype_migration_run();
CREATE TRIGGER prototype_migration_runs_immutable_delete BEFORE DELETE ON spyglass.prototype_migration_runs
FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER prototype_migration_receipts_immutable BEFORE UPDATE OR DELETE ON spyglass.prototype_migration_receipts
FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER prototype_migration_events_immutable BEFORE UPDATE OR DELETE ON spyglass.prototype_migration_events
FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();

CREATE TRIGGER prototype_migration_runs_erasure_count BEFORE DELETE ON spyglass.prototype_migration_runs
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER prototype_migration_receipts_erasure_count BEFORE DELETE ON spyglass.prototype_migration_receipts
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER prototype_migration_events_erasure_count BEFORE DELETE ON spyglass.prototype_migration_events
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();

CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.prototype_migration_runs
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.prototype_migration_receipts
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.prototype_migration_events
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

REVOKE ALL ON FUNCTION spyglass.protect_prototype_migration_run() FROM PUBLIC;

COMMIT;
