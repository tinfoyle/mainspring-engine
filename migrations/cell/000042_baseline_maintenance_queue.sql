BEGIN;

-- Content-free per-cell queue for recurring Baseline obligations. Like the
-- document queues it is outside Account RLS and exposed only through narrow
-- SECURITY DEFINER functions; assessment and Work rows remain Account-RLS.
CREATE TABLE spyglass.baseline_maintenance_queue (
    account_id uuid NOT NULL,
    assessment_id uuid NOT NULL,
    assessment_version bigint NOT NULL CHECK (assessment_version>0),
    scheduled_for timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('pending','leased','retry','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at timestamptz NOT NULL,
    lease_id uuid,
    lease_expires_at timestamptz,
    last_error_code text CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,assessment_id),
    FOREIGN KEY (account_id,assessment_id) REFERENCES spyglass.baseline_assessments(account_id,id) ON DELETE CASCADE,
    CHECK ((state='leased' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR
           (state<>'leased' AND lease_id IS NULL AND lease_expires_at IS NULL))
);

CREATE INDEX baseline_maintenance_queue_ready
    ON spyglass.baseline_maintenance_queue(next_attempt_at,scheduled_for,account_id,assessment_id)
    WHERE state IN ('pending','retry','leased');

CREATE FUNCTION spyglass.refresh_baseline_maintenance_queue(p_account_id uuid,p_assessment_id uuid,p_now timestamptz)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE assessment_row record; requirement_due timestamptz; obligation_due timestamptz;
BEGIN
    SELECT state,version,reassess_at,created_at INTO assessment_row
    FROM spyglass.baseline_assessments WHERE account_id=p_account_id AND id=p_assessment_id;
    IF NOT FOUND OR assessment_row.state<>'ready' THEN
        DELETE FROM spyglass.baseline_maintenance_queue WHERE account_id=p_account_id AND assessment_id=p_assessment_id;
        RETURN;
    END IF;
    SELECT min(renew_at) INTO requirement_due FROM spyglass.baseline_requirements
    WHERE account_id=p_account_id AND assessment_id=p_assessment_id AND disposition='satisfied' AND renew_at IS NOT NULL;
    obligation_due:=LEAST(assessment_row.reassess_at,requirement_due);
    IF obligation_due IS NULL THEN
        DELETE FROM spyglass.baseline_maintenance_queue WHERE account_id=p_account_id AND assessment_id=p_assessment_id;
        RETURN;
    END IF;
    INSERT INTO spyglass.baseline_maintenance_queue
        (account_id,assessment_id,assessment_version,scheduled_for,state,attempt_count,next_attempt_at,created_at,updated_at)
    VALUES (p_account_id,p_assessment_id,assessment_row.version,obligation_due,'pending',0,
        obligation_due-interval '30 days',assessment_row.created_at,p_now)
    ON CONFLICT (account_id,assessment_id) DO UPDATE SET
        assessment_version=EXCLUDED.assessment_version,
        scheduled_for=EXCLUDED.scheduled_for,
        state=CASE WHEN spyglass.baseline_maintenance_queue.scheduled_for IS DISTINCT FROM EXCLUDED.scheduled_for OR spyglass.baseline_maintenance_queue.assessment_version IS DISTINCT FROM EXCLUDED.assessment_version THEN 'pending' ELSE spyglass.baseline_maintenance_queue.state END,
        attempt_count=CASE WHEN spyglass.baseline_maintenance_queue.scheduled_for IS DISTINCT FROM EXCLUDED.scheduled_for OR spyglass.baseline_maintenance_queue.assessment_version IS DISTINCT FROM EXCLUDED.assessment_version THEN 0 ELSE spyglass.baseline_maintenance_queue.attempt_count END,
        next_attempt_at=CASE WHEN spyglass.baseline_maintenance_queue.scheduled_for IS DISTINCT FROM EXCLUDED.scheduled_for OR spyglass.baseline_maintenance_queue.assessment_version IS DISTINCT FROM EXCLUDED.assessment_version THEN EXCLUDED.next_attempt_at ELSE spyglass.baseline_maintenance_queue.next_attempt_at END,
        lease_id=CASE WHEN spyglass.baseline_maintenance_queue.scheduled_for IS DISTINCT FROM EXCLUDED.scheduled_for OR spyglass.baseline_maintenance_queue.assessment_version IS DISTINCT FROM EXCLUDED.assessment_version THEN NULL ELSE spyglass.baseline_maintenance_queue.lease_id END,
        lease_expires_at=CASE WHEN spyglass.baseline_maintenance_queue.scheduled_for IS DISTINCT FROM EXCLUDED.scheduled_for OR spyglass.baseline_maintenance_queue.assessment_version IS DISTINCT FROM EXCLUDED.assessment_version THEN NULL ELSE spyglass.baseline_maintenance_queue.lease_expires_at END,
        last_error_code=CASE WHEN spyglass.baseline_maintenance_queue.scheduled_for IS DISTINCT FROM EXCLUDED.scheduled_for OR spyglass.baseline_maintenance_queue.assessment_version IS DISTINCT FROM EXCLUDED.assessment_version THEN NULL ELSE spyglass.baseline_maintenance_queue.last_error_code END,
        updated_at=p_now;
END;
$$;

CREATE FUNCTION spyglass.refresh_baseline_maintenance_from_assessment() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
    PERFORM spyglass.refresh_baseline_maintenance_queue(NEW.account_id,NEW.id,NEW.updated_at);
    RETURN NEW;
END;
$$;

CREATE FUNCTION spyglass.refresh_baseline_maintenance_from_requirement() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
    PERFORM spyglass.refresh_baseline_maintenance_queue(NEW.account_id,NEW.assessment_id,statement_timestamp());
    RETURN NEW;
END;
$$;

CREATE TRIGGER baseline_assessments_refresh_maintenance
AFTER INSERT OR UPDATE OF state,reassess_at,version ON spyglass.baseline_assessments
FOR EACH ROW EXECUTE FUNCTION spyglass.refresh_baseline_maintenance_from_assessment();
CREATE TRIGGER baseline_requirements_refresh_maintenance
AFTER INSERT OR UPDATE OF disposition,renew_at ON spyglass.baseline_requirements
FOR EACH ROW EXECUTE FUNCTION spyglass.refresh_baseline_maintenance_from_requirement();

INSERT INTO spyglass.baseline_maintenance_queue
    (account_id,assessment_id,assessment_version,scheduled_for,state,attempt_count,next_attempt_at,created_at,updated_at)
SELECT assessment.account_id,assessment.id,assessment.version,
       LEAST(assessment.reassess_at,requirement.renew_at),'pending',0,
       LEAST(assessment.reassess_at,requirement.renew_at)-interval '30 days',assessment.created_at,statement_timestamp()
FROM spyglass.baseline_assessments assessment
LEFT JOIN LATERAL (
    SELECT min(renew_at) AS renew_at FROM spyglass.baseline_requirements
    WHERE account_id=assessment.account_id AND assessment_id=assessment.id AND disposition='satisfied' AND renew_at IS NOT NULL
) requirement ON true
WHERE assessment.state='ready' AND LEAST(assessment.reassess_at,requirement.renew_at) IS NOT NULL;

CREATE FUNCTION public.spyglass_claim_baseline_maintenance(p_lease_id uuid,p_now timestamptz,p_lease_seconds integer)
RETURNS TABLE(account_id uuid,assessment_id uuid,assessment_version bigint,scheduled_for timestamptz,lease_id uuid,attempt_count integer)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE candidate record; claimed_attempt integer;
BEGIN
    IF p_lease_id IS NULL OR p_now IS NULL OR p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Baseline maintenance claim';
    END IF;
    SELECT queue.account_id,queue.assessment_id INTO candidate
    FROM spyglass.baseline_maintenance_queue queue
    WHERE (queue.state IN ('pending','retry') AND queue.next_attempt_at<=p_now)
       OR (queue.state='leased' AND queue.lease_expires_at<=p_now)
    ORDER BY COALESCE(queue.lease_expires_at,queue.next_attempt_at),queue.scheduled_for,queue.account_id,queue.assessment_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;
    UPDATE spyglass.baseline_maintenance_queue queue SET state='leased',lease_id=p_lease_id,
        lease_expires_at=p_now+(p_lease_seconds*interval '1 second'),attempt_count=queue.attempt_count+1,
        last_error_code=NULL,updated_at=p_now
    WHERE queue.account_id=candidate.account_id AND queue.assessment_id=candidate.assessment_id
    RETURNING queue.attempt_count INTO claimed_attempt;
    RETURN QUERY SELECT queue.account_id,queue.assessment_id,queue.assessment_version,queue.scheduled_for,p_lease_id,claimed_attempt
    FROM spyglass.baseline_maintenance_queue queue
    WHERE queue.account_id=candidate.account_id AND queue.assessment_id=candidate.assessment_id;
END;
$$;

CREATE FUNCTION public.spyglass_complete_baseline_maintenance(
    p_account_id uuid,p_assessment_id uuid,p_lease_id uuid,p_scheduled_for timestamptz,p_now timestamptz
) RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE queue_row record;
BEGIN
    IF p_account_id IS NULL OR p_assessment_id IS NULL OR p_lease_id IS NULL OR p_scheduled_for IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Baseline maintenance completion';
    END IF;
    SELECT queue.* INTO queue_row FROM spyglass.baseline_maintenance_queue queue
    WHERE queue.account_id=p_account_id AND queue.assessment_id=p_assessment_id FOR UPDATE;
    IF NOT FOUND OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.scheduled_for<>p_scheduled_for OR queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Baseline maintenance lease lost';
    END IF;
    UPDATE spyglass.baseline_maintenance_queue SET state='retry',next_attempt_at=p_now+interval '24 hours',
        lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL,updated_at=p_now
    WHERE account_id=p_account_id AND assessment_id=p_assessment_id;
    RETURN true;
END;
$$;

CREATE FUNCTION public.spyglass_fail_baseline_maintenance(
    p_account_id uuid,p_assessment_id uuid,p_lease_id uuid,p_scheduled_for timestamptz,p_retry boolean,
    p_next_attempt_at timestamptz,p_error_code text,p_now timestamptz,p_max_attempts integer
) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE queue_row record; next_state text;
BEGIN
    IF p_account_id IS NULL OR p_assessment_id IS NULL OR p_lease_id IS NULL OR p_scheduled_for IS NULL OR p_retry IS NULL OR
       p_next_attempt_at IS NULL OR p_now IS NULL OR p_next_attempt_at<p_now OR p_error_code IS NULL OR
       p_error_code !~ '^[a-z][a-z0-9_]{0,99}$' OR p_max_attempts IS NULL OR p_max_attempts NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Baseline maintenance failure';
    END IF;
    SELECT queue.* INTO queue_row FROM spyglass.baseline_maintenance_queue queue
    WHERE queue.account_id=p_account_id AND queue.assessment_id=p_assessment_id FOR UPDATE;
    IF NOT FOUND OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.scheduled_for<>p_scheduled_for OR queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Baseline maintenance lease lost';
    END IF;
    next_state:=CASE WHEN p_retry AND queue_row.attempt_count<p_max_attempts THEN 'retry' ELSE 'dead_letter' END;
    UPDATE spyglass.baseline_maintenance_queue SET state=next_state,next_attempt_at=p_next_attempt_at,
        lease_id=NULL,lease_expires_at=NULL,last_error_code=p_error_code,updated_at=p_now
    WHERE account_id=p_account_id AND assessment_id=p_assessment_id;
    RETURN next_state;
END;
$$;

CREATE FUNCTION public.spyglass_baseline_maintenance_stats(p_now timestamptz)
RETURNS TABLE(pending bigint,ready bigint,leased bigint,retrying bigint,dead_letter bigint,oldest_ready_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
    SELECT count(*) FILTER (WHERE state='pending'),
           count(*) FILTER (WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now)),
           count(*) FILTER (WHERE state='leased' AND lease_expires_at>p_now),
           count(*) FILTER (WHERE state='retry'),
           count(*) FILTER (WHERE state='dead_letter'),
           min(COALESCE(lease_expires_at,next_attempt_at)) FILTER (WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now))
    FROM spyglass.baseline_maintenance_queue
$$;

CREATE TRIGGER baseline_maintenance_queue_erasure_count BEFORE DELETE ON spyglass.baseline_maintenance_queue
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_baseline_erasure_count();
CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.baseline_maintenance_queue
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

REVOKE ALL ON FUNCTION spyglass.refresh_baseline_maintenance_queue(uuid,uuid,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.refresh_baseline_maintenance_from_assessment() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.refresh_baseline_maintenance_from_requirement() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_baseline_maintenance(uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_complete_baseline_maintenance(uuid,uuid,uuid,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_fail_baseline_maintenance(uuid,uuid,uuid,timestamptz,boolean,timestamptz,text,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_baseline_maintenance_stats(timestamptz) FROM PUBLIC;

COMMIT;
