BEGIN;

CREATE TABLE spyglass.knowledge_documents (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200 AND title=btrim(title)),
    sensitivity text NOT NULL CHECK (sensitivity IN ('public','internal','confidential','restricted')),
    current_revision_id uuid,
    current_revision bigint CHECK (current_revision IS NULL OR current_revision>0),
    state text NOT NULL CHECK (state IN ('processing','ready','failed','deletion_pending','deleted')),
    retain_until timestamptz,
    legal_hold boolean NOT NULL DEFAULT false,
    version bigint NOT NULL CHECK (version>0),
    created_by_kind text NOT NULL CHECK (created_by_kind IN ('user','workload')),
    created_by_id text NOT NULL CHECK (char_length(created_by_id) BETWEEN 1 AND 256 AND created_by_id=btrim(created_by_id)),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    deletion_requested_at timestamptz,
    deleted_at timestamptz,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    CHECK (retain_until IS NULL OR retain_until>=created_at),
    CHECK ((current_revision_id IS NULL)=(current_revision IS NULL)),
    CHECK ((state IN ('processing','failed') AND current_revision_id IS NULL AND deletion_requested_at IS NULL AND deleted_at IS NULL) OR
           (state='ready' AND current_revision_id IS NOT NULL AND deletion_requested_at IS NULL AND deleted_at IS NULL) OR
           (state='deletion_pending' AND deletion_requested_at>=created_at AND deleted_at IS NULL) OR
           (state='deleted' AND deletion_requested_at>=created_at AND deleted_at>=deletion_requested_at AND legal_hold=false))
);

CREATE TABLE spyglass.knowledge_document_revisions (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    document_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    filename text NOT NULL CHECK (char_length(filename) BETWEEN 1 AND 255 AND filename=btrim(filename) AND filename !~ '[/\\]'),
    declared_media_type text NOT NULL CHECK (char_length(declared_media_type)<=200 AND declared_media_type=btrim(declared_media_type)),
    verified_media_type text NOT NULL CHECK (verified_media_type IN ('text/plain','text/markdown','text/csv','text/tab-separated-values','application/json','application/xml','text/html','application/yaml','application/pdf','application/vnd.openxmlformats-officedocument.wordprocessingml.document')),
    byte_size bigint NOT NULL CHECK (byte_size BETWEEN 1 AND 52428800),
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256)=32),
    object_key text NOT NULL CHECK (char_length(object_key) BETWEEN 1 AND 1024),
    object_version text NOT NULL CHECK (char_length(object_version) BETWEEN 1 AND 256 AND object_version=btrim(object_version)),
    change_summary text NOT NULL CHECK (char_length(change_summary)<=1000 AND change_summary=btrim(change_summary)),
    state text NOT NULL CHECK (state IN ('quarantined','extracting','ready','failed','deleted')),
    scan_state text NOT NULL CHECK (scan_state IN ('pending','clean','infected','error')),
    scan_engine text NOT NULL CHECK (char_length(scan_engine)<=200 AND scan_engine=btrim(scan_engine)),
    scan_signature text NOT NULL CHECK (char_length(scan_signature)<=200 AND scan_signature=btrim(scan_signature)),
    scanned_at timestamptz,
    extraction_state text NOT NULL CHECK (extraction_state IN ('pending','ready','failed')),
    extractor text NOT NULL CHECK (char_length(extractor)<=200 AND extractor=btrim(extractor)),
    text_sha256 bytea CHECK (text_sha256 IS NULL OR octet_length(text_sha256)=32),
    text_bytes bigint NOT NULL DEFAULT 0 CHECK (text_bytes BETWEEN 0 AND 8388608),
    extracted_at timestamptz,
    index_state text NOT NULL CHECK (index_state IN ('pending','ready','failed')),
    index_generation text NOT NULL CHECK (char_length(index_generation)<=200 AND index_generation=btrim(index_generation)),
    chunk_count integer NOT NULL DEFAULT 0 CHECK (chunk_count BETWEEN 0 AND 16384),
    indexed_at timestamptz,
    failure_code text NOT NULL CHECK (failure_code='' OR failure_code ~ '^[a-z][a-z0-9._:/-]{0,127}$'),
    created_by_kind text NOT NULL CHECK (created_by_kind IN ('user','workload')),
    created_by_id text NOT NULL CHECK (char_length(created_by_id) BETWEEN 1 AND 256 AND created_by_id=btrim(created_by_id)),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,document_id,revision),
    UNIQUE (account_id,object_key,object_version),
    FOREIGN KEY (account_id,document_id) REFERENCES spyglass.knowledge_documents(account_id,id) ON DELETE CASCADE,
    CHECK (object_key='accounts/'||account_id::text||'/documents/'||document_id::text||'/revisions/'||id::text||'/source'),
    CHECK (declared_media_type IN ('','application/octet-stream',verified_media_type)),
    CHECK ((state='quarantined' AND scan_state='pending' AND scan_engine='' AND scan_signature='' AND scanned_at IS NULL AND extraction_state='pending' AND extractor='' AND text_sha256 IS NULL AND text_bytes=0 AND extracted_at IS NULL AND index_state='pending' AND index_generation='' AND chunk_count=0 AND indexed_at IS NULL AND failure_code='') OR
           (state='extracting' AND scan_state='clean' AND scan_engine<>'' AND scanned_at>=created_at AND extraction_state IN ('pending','ready') AND index_state='pending' AND index_generation='' AND chunk_count=0 AND indexed_at IS NULL AND failure_code='' AND
             ((extraction_state='pending' AND extractor='' AND text_sha256 IS NULL AND text_bytes=0 AND extracted_at IS NULL) OR
              (extraction_state='ready' AND extractor<>'' AND text_sha256 IS NOT NULL AND text_bytes>0 AND extracted_at>=scanned_at))) OR
           (state='ready' AND scan_state='clean' AND scan_engine<>'' AND scanned_at>=created_at AND extraction_state='ready' AND extractor<>'' AND text_sha256 IS NOT NULL AND text_bytes>0 AND extracted_at>=scanned_at AND index_state='ready' AND index_generation<>'' AND chunk_count>0 AND indexed_at>=extracted_at AND failure_code='') OR
           (state='failed' AND scan_state IN ('infected','error','clean') AND scan_engine<>'' AND scanned_at>=created_at AND extraction_state IN ('failed','ready') AND index_state='failed' AND index_generation='' AND chunk_count=0 AND indexed_at IS NULL AND failure_code<>'' AND
             ((extraction_state='failed' AND extractor='' AND text_sha256 IS NULL AND text_bytes=0 AND extracted_at IS NULL) OR
              (extraction_state='ready' AND extractor<>'' AND text_sha256 IS NOT NULL AND text_bytes>0 AND extracted_at>=scanned_at))) OR
           (state='deleted' AND scan_state<>'pending' AND scan_engine<>'' AND scanned_at>=created_at AND extraction_state<>'pending' AND index_state<>'pending')),
    CHECK (scanned_at IS NULL OR scanned_at<=updated_at),
    CHECK (extracted_at IS NULL OR extracted_at<=updated_at),
    CHECK (indexed_at IS NULL OR indexed_at<=updated_at)
);

CREATE TABLE spyglass.knowledge_document_chunks (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    revision_id uuid NOT NULL,
    chunk_index integer NOT NULL CHECK (chunk_index BETWEEN 0 AND 16383),
    start_byte bigint NOT NULL CHECK (start_byte>=0),
    end_byte bigint NOT NULL CHECK (end_byte>start_byte),
    content text NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 65536),
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256)=32),
    token_count integer NOT NULL CHECK (token_count>0),
    index_generation text NOT NULL CHECK (char_length(index_generation) BETWEEN 1 AND 200 AND index_generation=btrim(index_generation)),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,revision_id,chunk_index),
    FOREIGN KEY (account_id,revision_id) REFERENCES spyglass.knowledge_document_revisions(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.knowledge_document_events (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    aggregate_kind text NOT NULL CHECK (aggregate_kind IN ('document','revision')),
    document_id uuid NOT NULL,
    revision_id uuid,
    event_type text NOT NULL CHECK (event_type IN ('document_created','revision_admitted','scan_completed','extraction_completed','index_completed','processing_failed','revision_published','deletion_requested','deletion_completed')),
    from_version bigint NOT NULL CHECK (from_version>=0),
    to_version bigint NOT NULL CHECK (to_version>=from_version),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user','workload')),
    actor_id text NOT NULL CHECK (char_length(actor_id) BETWEEN 1 AND 256 AND actor_id=btrim(actor_id)),
    reason_code text NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    correlation_id uuid NOT NULL,
    redacted_payload jsonb NOT NULL CHECK (jsonb_typeof(redacted_payload)='object' AND octet_length(redacted_payload::text)<=8192),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,document_id) REFERENCES spyglass.knowledge_documents(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,revision_id) REFERENCES spyglass.knowledge_document_revisions(account_id,id) ON DELETE CASCADE,
    CHECK ((aggregate_kind='document' AND revision_id IS NULL AND event_type IN ('document_created','revision_published','deletion_requested','deletion_completed')) OR
           (aggregate_kind='revision' AND revision_id IS NOT NULL AND event_type IN ('revision_admitted','scan_completed','extraction_completed','index_completed','processing_failed')))
);

CREATE INDEX knowledge_documents_list ON spyglass.knowledge_documents(account_id,state,updated_at,id);
CREATE INDEX knowledge_document_revisions_queue ON spyglass.knowledge_document_revisions(account_id,state,updated_at,id);
CREATE INDEX knowledge_document_chunks_revision ON spyglass.knowledge_document_chunks(account_id,revision_id,chunk_index);
CREATE INDEX knowledge_document_events_aggregate ON spyglass.knowledge_document_events(account_id,document_id,revision_id,occurred_at,id);

ALTER TABLE spyglass.knowledge_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_documents FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_document_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_document_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_document_chunks ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_document_chunks FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_document_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_document_events FORCE ROW LEVEL SECURITY;
CREATE POLICY knowledge_documents_isolation ON spyglass.knowledge_documents USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY knowledge_document_revisions_isolation ON spyglass.knowledge_document_revisions USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY knowledge_document_chunks_isolation ON spyglass.knowledge_document_chunks USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY knowledge_document_events_isolation ON spyglass.knowledge_document_events USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.protect_knowledge_document_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.created_by_kind IS DISTINCT FROM OLD.created_by_kind OR NEW.created_by_id IS DISTINCT FROM OLD.created_by_id OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Knowledge document identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.protect_knowledge_revision_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.document_id IS DISTINCT FROM OLD.document_id OR NEW.revision IS DISTINCT FROM OLD.revision OR NEW.filename IS DISTINCT FROM OLD.filename OR NEW.declared_media_type IS DISTINCT FROM OLD.declared_media_type OR NEW.verified_media_type IS DISTINCT FROM OLD.verified_media_type OR NEW.byte_size IS DISTINCT FROM OLD.byte_size OR NEW.content_sha256 IS DISTINCT FROM OLD.content_sha256 OR NEW.object_key IS DISTINCT FROM OLD.object_key OR NEW.object_version IS DISTINCT FROM OLD.object_version OR NEW.change_summary IS DISTINCT FROM OLD.change_summary OR NEW.created_by_kind IS DISTINCT FROM OLD.created_by_kind OR NEW.created_by_id IS DISTINCT FROM OLD.created_by_id OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Knowledge document revision identity is immutable';
    END IF;
    IF OLD.state='quarantined' AND NEW.state NOT IN ('extracting','failed') THEN RAISE EXCEPTION 'invalid Knowledge revision transition'; END IF;
    IF OLD.state='extracting' AND NEW.state NOT IN ('extracting','ready','failed') THEN RAISE EXCEPTION 'invalid Knowledge revision transition'; END IF;
    IF OLD.state IN ('ready','failed') AND NEW.state<>'deleted' THEN RAISE EXCEPTION 'invalid Knowledge revision transition'; END IF;
    IF OLD.state='deleted' THEN RAISE EXCEPTION 'deleted Knowledge revision is immutable'; END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.validate_knowledge_document_current_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.current_revision_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM spyglass.knowledge_document_revisions revision_row
        WHERE revision_row.account_id=NEW.account_id AND revision_row.document_id=NEW.id
          AND revision_row.id=NEW.current_revision_id AND revision_row.revision=NEW.current_revision
          AND revision_row.state='ready'
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='Knowledge document current revision must be an exact ready revision';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER knowledge_documents_identity_guard BEFORE UPDATE ON spyglass.knowledge_documents FOR EACH ROW EXECUTE FUNCTION spyglass.protect_knowledge_document_identity();
CREATE TRIGGER knowledge_document_revisions_identity_guard BEFORE UPDATE ON spyglass.knowledge_document_revisions FOR EACH ROW EXECUTE FUNCTION spyglass.protect_knowledge_revision_identity();
CREATE TRIGGER knowledge_documents_current_revision_guard BEFORE INSERT OR UPDATE OF current_revision_id,current_revision ON spyglass.knowledge_documents FOR EACH ROW EXECUTE FUNCTION spyglass.validate_knowledge_document_current_revision();
CREATE TRIGGER knowledge_documents_immutable_delete BEFORE DELETE ON spyglass.knowledge_documents FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER knowledge_document_revisions_immutable_delete BEFORE DELETE ON spyglass.knowledge_document_revisions FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER knowledge_document_chunks_immutable BEFORE UPDATE OR DELETE ON spyglass.knowledge_document_chunks FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER knowledge_document_events_immutable BEFORE UPDATE OR DELETE ON spyglass.knowledge_document_events FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();

CREATE TRIGGER knowledge_documents_erasure_count BEFORE DELETE ON spyglass.knowledge_documents FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER knowledge_document_revisions_erasure_count BEFORE DELETE ON spyglass.knowledge_document_revisions FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER knowledge_document_chunks_erasure_count BEFORE DELETE ON spyglass.knowledge_document_chunks FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER knowledge_document_events_erasure_count BEFORE DELETE ON spyglass.knowledge_document_events FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();

CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_documents FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_document_revisions FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_document_chunks FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_document_events FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

REVOKE ALL ON FUNCTION spyglass.protect_knowledge_document_identity() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_knowledge_revision_identity() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.validate_knowledge_document_current_revision() FROM PUBLIC;

COMMIT;
