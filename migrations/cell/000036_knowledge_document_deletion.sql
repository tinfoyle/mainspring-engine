BEGIN;

-- Deletion scheduling is intentionally content-free and outside Account RLS so
-- one per-cell worker can claim work fairly. Customer metadata and the exact
-- object manifest remain in Account-scoped tables.
CREATE TABLE spyglass.knowledge_document_deletion_queue (
    account_id uuid NOT NULL,
    document_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('pending','leased','retry','completed','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at timestamptz NOT NULL,
    lease_id uuid,
    lease_expires_at timestamptz,
    last_error_code text CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    completed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,document_id),
    FOREIGN KEY (account_id,document_id) REFERENCES spyglass.knowledge_documents(account_id,id) ON DELETE CASCADE,
    CHECK ((state='leased' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR
           (state<>'leased' AND lease_id IS NULL AND lease_expires_at IS NULL)),
    CHECK ((state='completed' AND completed_at IS NOT NULL) OR (state<>'completed' AND completed_at IS NULL))
);

CREATE INDEX knowledge_document_deletion_ready
    ON spyglass.knowledge_document_deletion_queue(next_attempt_at,created_at,document_id)
    WHERE state IN ('pending','retry','leased');

-- Receipts contain immutable object coordinates and integrity metadata, never
-- document content. They make an exact-version object delete replayable after
-- an unknown commit or worker crash.
CREATE TABLE spyglass.knowledge_document_deletion_receipts (
    account_id uuid NOT NULL,
    document_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    object_kind text NOT NULL CHECK (object_kind IN ('source','extracted')),
    object_key text NOT NULL CHECK (char_length(object_key) BETWEEN 1 AND 1024),
    object_version text NOT NULL CHECK (char_length(object_version) BETWEEN 1 AND 256 AND object_version=btrim(object_version)),
    byte_size bigint NOT NULL CHECK (byte_size BETWEEN 1 AND 52428800),
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256)=32),
    deleted_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,document_id,revision_id,object_kind),
    FOREIGN KEY (account_id,document_id) REFERENCES spyglass.knowledge_documents(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,revision_id) REFERENCES spyglass.knowledge_document_revisions(account_id,id) ON DELETE CASCADE,
    CHECK (object_key='accounts/'||account_id::text||'/documents/'||document_id::text||'/revisions/'||revision_id::text||
        CASE object_kind WHEN 'source' THEN '/source' ELSE '/extracted/text' END)
);

ALTER TABLE spyglass.knowledge_document_deletion_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.knowledge_document_deletion_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY knowledge_document_deletion_receipts_isolation ON spyglass.knowledge_document_deletion_receipts
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.enqueue_knowledge_document_deletion() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
BEGIN
    IF NEW.state='deletion_pending' AND OLD.state IS DISTINCT FROM NEW.state THEN
        INSERT INTO spyglass.knowledge_document_deletion_queue
            (account_id,document_id,state,next_attempt_at,created_at,updated_at)
        VALUES (NEW.account_id,NEW.id,'pending',NEW.updated_at,NEW.updated_at,NEW.updated_at)
        ON CONFLICT (account_id,document_id) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER knowledge_documents_enqueue_deletion
AFTER UPDATE OF state ON spyglass.knowledge_documents
FOR EACH ROW EXECUTE FUNCTION spyglass.enqueue_knowledge_document_deletion();

INSERT INTO spyglass.knowledge_document_deletion_queue
    (account_id,document_id,state,next_attempt_at,created_at,updated_at)
SELECT account_id,id,'pending',updated_at,updated_at,updated_at
FROM spyglass.knowledge_documents WHERE state='deletion_pending'
ON CONFLICT (account_id,document_id) DO NOTHING;

CREATE TRIGGER knowledge_document_deletion_receipts_immutable
BEFORE UPDATE OR DELETE ON spyglass.knowledge_document_deletion_receipts
FOR EACH ROW EXECUTE FUNCTION spyglass.reject_knowledge_history_change();
CREATE TRIGGER knowledge_document_deletion_receipts_erasure_count
BEFORE DELETE ON spyglass.knowledge_document_deletion_receipts
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER account_namespace_write_fence
BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_document_deletion_receipts
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE TRIGGER knowledge_document_deletion_queue_erasure_count
BEFORE DELETE ON spyglass.knowledge_document_deletion_queue
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER account_namespace_write_fence
BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_document_deletion_queue
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

CREATE FUNCTION public.spyglass_claim_knowledge_document_deletion(
    p_lease_id uuid,p_now timestamptz,p_lease_seconds integer
) RETURNS TABLE (account_id uuid,document_id uuid,lease_id uuid,attempt_count integer)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE candidate record; claimed_attempt integer;
BEGIN
    IF p_lease_id IS NULL OR p_now IS NULL OR p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Knowledge document deletion claim';
    END IF;
    SELECT q.account_id,q.document_id INTO candidate
    FROM spyglass.knowledge_document_deletion_queue q
    WHERE (q.state IN ('pending','retry') AND q.next_attempt_at<=p_now)
       OR (q.state='leased' AND q.lease_expires_at<=p_now)
    ORDER BY COALESCE(q.lease_expires_at,q.next_attempt_at),q.created_at,q.document_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;

    UPDATE spyglass.knowledge_document_deletion_queue q SET
        state='leased',lease_id=p_lease_id,lease_expires_at=p_now+(p_lease_seconds*interval '1 second'),
        attempt_count=q.attempt_count+1,last_error_code=NULL,updated_at=p_now
    WHERE q.account_id=candidate.account_id AND q.document_id=candidate.document_id
    RETURNING q.attempt_count INTO claimed_attempt;

    RETURN QUERY SELECT candidate.account_id,candidate.document_id,p_lease_id,claimed_attempt;
END;
$$;

CREATE FUNCTION public.spyglass_complete_knowledge_document_deletion(
    p_account_id uuid,p_document_id uuid,p_lease_id uuid,p_now timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record; document_row record; deleted_revision_count bigint;
BEGIN
    IF p_account_id IS NULL OR p_document_id IS NULL OR p_lease_id IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Knowledge document deletion completion';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.knowledge_document_deletion_queue q
    WHERE q.account_id=p_account_id AND q.document_id=p_document_id FOR UPDATE;
    IF FOUND AND queue_row.state='completed' THEN RETURN false; END IF;
    IF queue_row.document_id IS NULL OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Knowledge document deletion lease lost';
    END IF;

    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT d.* INTO document_row FROM spyglass.knowledge_documents d
    WHERE d.account_id=p_account_id AND d.id=p_document_id FOR UPDATE;
    IF document_row.id IS NULL OR document_row.state<>'deletion_pending' OR document_row.legal_hold OR
       (document_row.retain_until IS NOT NULL AND p_now<document_row.retain_until) THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Knowledge document deletion target is not eligible';
    END IF;
    IF p_now<document_row.updated_at THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='Knowledge document deletion completion predates the document';
    END IF;
    IF EXISTS (
        SELECT 1 FROM spyglass.knowledge_document_revisions r
        WHERE r.account_id=p_account_id AND r.document_id=p_document_id AND r.state NOT IN ('ready','failed','deleted')
    ) THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Knowledge document deletion revisions are not terminal';
    END IF;
    IF EXISTS (
        SELECT 1 FROM spyglass.knowledge_document_revisions r
        WHERE r.account_id=p_account_id AND r.document_id=p_document_id AND (
            NOT EXISTS (SELECT 1 FROM spyglass.knowledge_document_deletion_receipts receipt
                WHERE receipt.account_id=r.account_id AND receipt.document_id=r.document_id AND receipt.revision_id=r.id
                  AND receipt.object_kind='source' AND receipt.object_key=r.object_key AND receipt.object_version=r.object_version
                  AND receipt.byte_size=r.byte_size AND receipt.content_sha256=r.content_sha256)
            OR (r.extracted_object_version<>'' AND NOT EXISTS (
                SELECT 1 FROM spyglass.knowledge_document_deletion_receipts receipt
                WHERE receipt.account_id=r.account_id AND receipt.document_id=r.document_id AND receipt.revision_id=r.id
                  AND receipt.object_kind='extracted' AND receipt.object_key=r.extracted_object_key
                  AND receipt.object_version=r.extracted_object_version AND receipt.byte_size=r.text_bytes
                  AND receipt.content_sha256=r.text_sha256
            ))
        )
    ) THEN
        RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='Knowledge document deletion receipts are incomplete';
    END IF;

    -- This is the only routine allowed to remove indexed customer content.
    DELETE FROM spyglass.knowledge_document_chunks chunk
    USING spyglass.knowledge_document_revisions revision
    WHERE chunk.account_id=p_account_id AND revision.account_id=p_account_id
      AND revision.document_id=p_document_id AND chunk.revision_id=revision.id;

    UPDATE spyglass.knowledge_document_revisions SET state='deleted',updated_at=p_now
    WHERE account_id=p_account_id AND document_id=p_document_id AND state IN ('ready','failed');
    GET DIAGNOSTICS deleted_revision_count = ROW_COUNT;

    UPDATE spyglass.knowledge_documents SET state='deleted',version=document_row.version+1,
        updated_at=p_now,deleted_at=p_now
    WHERE account_id=p_account_id AND id=p_document_id AND version=document_row.version;

    INSERT INTO spyglass.knowledge_document_events
        (account_id,id,aggregate_kind,document_id,revision_id,event_type,from_version,to_version,
         actor_kind,actor_id,reason_code,correlation_id,redacted_payload,occurred_at)
    VALUES (p_account_id,p_lease_id,'document',p_document_id,NULL,'deletion_completed',
        document_row.version,document_row.version+1,'workload','knowledge-document-deleter',
        'deletion_completed',p_lease_id,jsonb_build_object('revision_count',deleted_revision_count),p_now)
    ON CONFLICT (account_id,id) DO NOTHING;

    UPDATE spyglass.knowledge_document_deletion_queue SET state='completed',lease_id=NULL,lease_expires_at=NULL,
        last_error_code=NULL,completed_at=p_now,updated_at=p_now
    WHERE account_id=p_account_id AND document_id=p_document_id;
    RETURN true;
END;
$$;

CREATE FUNCTION public.spyglass_fail_knowledge_document_deletion(
    p_account_id uuid,p_document_id uuid,p_lease_id uuid,p_retry boolean,p_next_attempt_at timestamptz,
    p_error_code text,p_now timestamptz,p_max_attempts integer
) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record; next_state text;
BEGIN
    IF p_account_id IS NULL OR p_document_id IS NULL OR p_lease_id IS NULL OR p_retry IS NULL OR
       p_next_attempt_at IS NULL OR p_now IS NULL OR p_next_attempt_at<p_now OR
       p_error_code IS NULL OR p_error_code !~ '^[a-z][a-z0-9_]{0,99}$' OR
       p_max_attempts IS NULL OR p_max_attempts NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Knowledge document deletion failure';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.knowledge_document_deletion_queue q
    WHERE q.account_id=p_account_id AND q.document_id=p_document_id FOR UPDATE;
    IF NOT FOUND OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Knowledge document deletion lease lost';
    END IF;
    next_state:=CASE WHEN p_retry AND queue_row.attempt_count<p_max_attempts THEN 'retry' ELSE 'dead_letter' END;
    UPDATE spyglass.knowledge_document_deletion_queue SET state=next_state,next_attempt_at=p_next_attempt_at,
        lease_id=NULL,lease_expires_at=NULL,last_error_code=p_error_code,updated_at=p_now
    WHERE account_id=p_account_id AND document_id=p_document_id;
    RETURN next_state;
END;
$$;

CREATE FUNCTION public.spyglass_knowledge_document_deletion_stats(p_now timestamptz)
RETURNS TABLE(pending bigint,ready bigint,leased bigint,retrying bigint,completed bigint,dead_letter bigint,oldest_ready_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
    SELECT count(*) FILTER (WHERE state='pending'),
           count(*) FILTER (WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now)),
           count(*) FILTER (WHERE state='leased' AND lease_expires_at>p_now),
           count(*) FILTER (WHERE state='retry'),
           count(*) FILTER (WHERE state='completed'),
           count(*) FILTER (WHERE state='dead_letter'),
           min(COALESCE(lease_expires_at,next_attempt_at)) FILTER (WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now))
    FROM spyglass.knowledge_document_deletion_queue
$$;

-- Replace blanket chunk immutability with a guard that permits only the
-- transactionally finalized deletion path above.
DROP TRIGGER knowledge_document_chunks_immutable ON spyglass.knowledge_document_chunks;
CREATE FUNCTION spyglass.protect_knowledge_document_chunk_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' AND (pg_trigger_depth()>1 OR
       (current_setting('spyglass.account_movement',true)='on' AND spyglass.account_movement_write_allowed(OLD.account_id,'DELETE'))) THEN
        RETURN OLD;
    END IF;
    IF TG_OP='UPDATE' OR NOT EXISTS (
        SELECT 1 FROM spyglass.knowledge_document_revisions revision
        JOIN spyglass.knowledge_documents document
          ON document.account_id=revision.account_id AND document.id=revision.document_id
        WHERE revision.account_id=OLD.account_id AND revision.id=OLD.revision_id
          AND document.state='deletion_pending'
    ) THEN
        RAISE EXCEPTION 'Knowledge document chunk is immutable';
    END IF;
    RETURN OLD;
END;
$$;
CREATE TRIGGER knowledge_document_chunks_immutable
BEFORE UPDATE OR DELETE ON spyglass.knowledge_document_chunks
FOR EACH ROW EXECUTE FUNCTION spyglass.protect_knowledge_document_chunk_change();

REVOKE ALL ON FUNCTION spyglass.enqueue_knowledge_document_deletion() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.protect_knowledge_document_chunk_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_knowledge_document_deletion(uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_complete_knowledge_document_deletion(uuid,uuid,uuid,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_fail_knowledge_document_deletion(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_knowledge_document_deletion_stats(timestamptz) FROM PUBLIC;

COMMIT;
