BEGIN;

-- This cell-wide queue contains identifiers and operational state only. It is
-- deliberately outside Account RLS so one shared worker can claim fairly;
-- source and extracted content remain behind Account RLS and the object port.
CREATE TABLE spyglass.knowledge_document_processing_queue (
    account_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('pending','leased','retry','completed','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at timestamptz NOT NULL,
    lease_id uuid,
    lease_expires_at timestamptz,
    last_error_code text CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    completed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,revision_id),
    FOREIGN KEY (account_id,revision_id) REFERENCES spyglass.knowledge_document_revisions(account_id,id) ON DELETE CASCADE,
    CHECK ((state='leased' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR
           (state<>'leased' AND lease_id IS NULL AND lease_expires_at IS NULL)),
    CHECK ((state='completed' AND completed_at IS NOT NULL) OR (state<>'completed' AND completed_at IS NULL))
);

CREATE INDEX knowledge_document_processing_ready
    ON spyglass.knowledge_document_processing_queue(next_attempt_at,created_at,revision_id)
    WHERE state IN ('pending','retry','leased');

CREATE FUNCTION spyglass.enqueue_knowledge_document_processing() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
BEGIN
    INSERT INTO spyglass.knowledge_document_processing_queue
        (account_id,revision_id,state,next_attempt_at,completed_at,created_at,updated_at)
    VALUES (NEW.account_id,NEW.id,
        CASE WHEN NEW.state IN ('ready','failed','deleted') THEN 'completed' ELSE 'pending' END,
        NEW.created_at,CASE WHEN NEW.state IN ('ready','failed','deleted') THEN NEW.updated_at END,
        NEW.created_at,NEW.updated_at)
    ON CONFLICT (account_id,revision_id) DO NOTHING;
    RETURN NEW;
END;
$$;

CREATE TRIGGER knowledge_document_revisions_enqueue_processing
AFTER INSERT ON spyglass.knowledge_document_revisions
FOR EACH ROW EXECUTE FUNCTION spyglass.enqueue_knowledge_document_processing();

INSERT INTO spyglass.knowledge_document_processing_queue
    (account_id,revision_id,state,next_attempt_at,completed_at,created_at,updated_at)
SELECT account_id,id,
       CASE WHEN state IN ('ready','failed','deleted') THEN 'completed' ELSE 'pending' END,
       created_at,CASE WHEN state IN ('ready','failed','deleted') THEN updated_at END,
       created_at,updated_at
FROM spyglass.knowledge_document_revisions;

CREATE FUNCTION public.spyglass_claim_knowledge_document_processing(
    p_lease_id uuid,p_now timestamptz,p_lease_seconds integer
) RETURNS TABLE (account_id uuid,revision_id uuid,lease_id uuid,attempt_count integer)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE candidate record; claimed_attempt integer;
BEGIN
    IF p_lease_id IS NULL OR p_now IS NULL OR p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Knowledge document processing claim';
    END IF;
    SELECT q.account_id,q.revision_id INTO candidate
    FROM spyglass.knowledge_document_processing_queue q
    WHERE (q.state IN ('pending','retry') AND q.next_attempt_at<=p_now)
       OR (q.state='leased' AND q.lease_expires_at<=p_now)
    ORDER BY COALESCE(q.lease_expires_at,q.next_attempt_at),q.created_at,q.revision_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;

    UPDATE spyglass.knowledge_document_processing_queue q SET
        state='leased',lease_id=p_lease_id,lease_expires_at=p_now+(p_lease_seconds*interval '1 second'),
        attempt_count=q.attempt_count+1,last_error_code=NULL,updated_at=p_now
    WHERE q.account_id=candidate.account_id AND q.revision_id=candidate.revision_id
    RETURNING q.attempt_count INTO claimed_attempt;

    RETURN QUERY SELECT candidate.account_id,candidate.revision_id,p_lease_id,claimed_attempt;
END;
$$;

CREATE FUNCTION public.spyglass_complete_knowledge_document_processing(
    p_account_id uuid,p_revision_id uuid,p_lease_id uuid,p_now timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record; revision_state text;
BEGIN
    IF p_account_id IS NULL OR p_revision_id IS NULL OR p_lease_id IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Knowledge document processing completion';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.knowledge_document_processing_queue q
    WHERE q.account_id=p_account_id AND q.revision_id=p_revision_id FOR UPDATE;
    IF FOUND AND queue_row.state='completed' THEN RETURN false; END IF;
    IF queue_row.revision_id IS NULL OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Knowledge document processing lease lost';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT r.state INTO revision_state FROM spyglass.knowledge_document_revisions r
    WHERE r.account_id=p_account_id AND r.id=p_revision_id FOR SHARE;
    IF revision_state IS NULL OR revision_state NOT IN ('ready','failed','deleted') THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Knowledge document processing target is not terminal';
    END IF;
    UPDATE spyglass.knowledge_document_processing_queue SET state='completed',lease_id=NULL,lease_expires_at=NULL,
        last_error_code=NULL,completed_at=p_now,updated_at=p_now
    WHERE account_id=p_account_id AND revision_id=p_revision_id;
    RETURN true;
END;
$$;

CREATE FUNCTION public.spyglass_fail_knowledge_document_processing(
    p_account_id uuid,p_revision_id uuid,p_lease_id uuid,p_retry boolean,p_next_attempt_at timestamptz,
    p_error_code text,p_now timestamptz,p_max_attempts integer
) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record; next_state text;
BEGIN
    IF p_account_id IS NULL OR p_revision_id IS NULL OR p_lease_id IS NULL OR p_retry IS NULL OR
       p_next_attempt_at IS NULL OR p_now IS NULL OR p_next_attempt_at<p_now OR
       p_error_code IS NULL OR p_error_code !~ '^[a-z][a-z0-9_]{0,99}$' OR
       p_max_attempts IS NULL OR p_max_attempts NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Knowledge document processing failure';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.knowledge_document_processing_queue q
    WHERE q.account_id=p_account_id AND q.revision_id=p_revision_id FOR UPDATE;
    IF NOT FOUND OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Knowledge document processing lease lost';
    END IF;
    next_state:=CASE WHEN p_retry AND queue_row.attempt_count<p_max_attempts THEN 'retry' ELSE 'dead_letter' END;
    UPDATE spyglass.knowledge_document_processing_queue SET state=next_state,next_attempt_at=p_next_attempt_at,
        lease_id=NULL,lease_expires_at=NULL,last_error_code=p_error_code,updated_at=p_now
    WHERE account_id=p_account_id AND revision_id=p_revision_id;
    RETURN next_state;
END;
$$;

CREATE FUNCTION public.spyglass_knowledge_document_processing_stats(p_now timestamptz)
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
    FROM spyglass.knowledge_document_processing_queue
$$;

CREATE TRIGGER knowledge_document_processing_erasure_count
BEFORE DELETE ON spyglass.knowledge_document_processing_queue
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_knowledge_erasure_count();
CREATE TRIGGER account_namespace_write_fence
BEFORE INSERT OR UPDATE OR DELETE ON spyglass.knowledge_document_processing_queue
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

REVOKE ALL ON FUNCTION spyglass.enqueue_knowledge_document_processing() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_knowledge_document_processing(uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_complete_knowledge_document_processing(uuid,uuid,uuid,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_fail_knowledge_document_processing(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_knowledge_document_processing_stats(timestamptz) FROM PUBLIC;

COMMIT;
