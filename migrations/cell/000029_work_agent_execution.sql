BEGIN;

-- A Persona assignment freezes one execution intent before any worker can
-- observe it. Customer text and the immutable Persona version remain in the
-- Account cell; the worker claims only identifiers across Accounts.
CREATE TABLE spyglass.work_agent_executions (
    account_id uuid NOT NULL,
    execution_id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    work_version bigint NOT NULL CHECK (work_version>0),
    initiating_user_id uuid NOT NULL,
    persona_id uuid NOT NULL,
    persona_version_id uuid NOT NULL,
    boardroom_id uuid NOT NULL,
    boardroom_version bigint NOT NULL CHECK (boardroom_version>0),
    planned_run_id uuid NOT NULL,
    planned_conversation_id uuid NOT NULL,
    title text NOT NULL CHECK (char_length(title) BETWEEN 2 AND 240),
    description text NOT NULL CHECK (char_length(description)<=20000),
    linked_run_id uuid,
    linked_conversation_id uuid,
    queued_at timestamptz NOT NULL,
    linked_at timestamptz,
    updated_at timestamptz NOT NULL CHECK (updated_at>=queued_at),
    PRIMARY KEY (account_id,execution_id),
    UNIQUE (account_id,execution_id,work_item_id),
    UNIQUE (account_id,planned_run_id),
    UNIQUE (account_id,planned_conversation_id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,work_item_id) REFERENCES spyglass.work_items(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,persona_id) REFERENCES spyglass.agent_personas(account_id,id),
    FOREIGN KEY (account_id,persona_version_id) REFERENCES spyglass.agent_persona_versions(account_id,id),
    FOREIGN KEY (account_id,boardroom_id) REFERENCES spyglass.agent_boardrooms(account_id,id),
    FOREIGN KEY (account_id,linked_run_id,linked_conversation_id)
        REFERENCES spyglass.agent_runs(account_id,id,conversation_id),
    CHECK ((linked_run_id IS NOT NULL AND linked_run_id=planned_run_id AND linked_conversation_id=planned_conversation_id AND linked_at IS NOT NULL) OR
           (linked_run_id IS NULL AND linked_conversation_id IS NULL AND linked_at IS NULL))
);

-- The shared claim surface is deliberately identifier-only and not RLS
-- scoped. Its role has no direct table grant; cross-Account access is limited
-- to the security-definer claim/failure/stat functions below.
CREATE TABLE spyglass.work_agent_execution_queue (
    account_id uuid NOT NULL,
    execution_id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('pending','leased','retry','linked','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at timestamptz NOT NULL,
    lease_id uuid,
    lease_expires_at timestamptz,
    last_error_code text CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    queued_at timestamptz NOT NULL,
    linked_at timestamptz,
    updated_at timestamptz NOT NULL CHECK (updated_at>=queued_at),
    PRIMARY KEY (account_id,execution_id),
    FOREIGN KEY (account_id,execution_id,work_item_id)
        REFERENCES spyglass.work_agent_executions(account_id,execution_id,work_item_id) ON DELETE CASCADE,
    CHECK ((state='leased' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR
           (state<>'leased' AND lease_id IS NULL AND lease_expires_at IS NULL)),
    CHECK ((state='linked' AND linked_at IS NOT NULL AND last_error_code IS NULL) OR
           (state<>'linked' AND linked_at IS NULL))
);

CREATE UNIQUE INDEX work_agent_execution_active_item
    ON spyglass.work_agent_execution_queue(account_id,work_item_id)
    WHERE state IN ('pending','leased','retry');
CREATE INDEX work_agent_execution_ready
    ON spyglass.work_agent_execution_queue(next_attempt_at,queued_at,execution_id)
    WHERE state IN ('pending','leased','retry');
CREATE INDEX work_agent_execution_linked
    ON spyglass.work_agent_executions(account_id,linked_at,execution_id)
    WHERE linked_at IS NOT NULL;

ALTER TABLE spyglass.work_agent_executions ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.work_agent_executions FORCE ROW LEVEL SECURITY;
CREATE POLICY work_agent_executions_isolation ON spyglass.work_agent_executions
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

ALTER TABLE spyglass.work_item_events
    DROP CONSTRAINT work_item_events_event_type_check,
    ADD CONSTRAINT work_item_events_event_type_check
        CHECK (event_type IN ('created','transitioned','assigned','provenance_attached','conversation_linked','agent_execution_linked'));

CREATE TRIGGER account_namespace_write_fence
BEFORE INSERT OR UPDATE OR DELETE ON spyglass.work_agent_executions
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();
CREATE FUNCTION spyglass.block_account_move_with_active_work_agent_execution() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
BEGIN
    IF NEW.state='moving' AND OLD.state<>'moving' AND EXISTS (
        SELECT 1 FROM spyglass.work_agent_execution_queue q
        WHERE q.account_id=NEW.account_id AND q.state IN ('pending','leased','retry')) THEN
        RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='source Account has unfinished Work Agent execution';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_namespace_work_agent_execution_move_gate
BEFORE UPDATE OF state ON spyglass.account_namespaces
FOR EACH ROW EXECUTE FUNCTION spyglass.block_account_move_with_active_work_agent_execution();
REVOKE ALL ON FUNCTION spyglass.block_account_move_with_active_work_agent_execution() FROM PUBLIC;

CREATE FUNCTION public.spyglass_claim_work_agent_execution(
    p_lease_id uuid,p_now timestamptz,p_lease_seconds integer
) RETURNS TABLE(execution_id uuid,account_id uuid,work_item_id uuid,lease_id uuid,attempt_count integer)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE candidate record; claimed_attempt integer;
BEGIN
    IF p_lease_id IS NULL OR p_now IS NULL OR p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Work Agent execution claim';
    END IF;
    SELECT q.account_id,q.execution_id,q.work_item_id INTO candidate
    FROM spyglass.work_agent_execution_queue q
    WHERE (q.state IN ('pending','retry') AND q.next_attempt_at<=p_now)
       OR (q.state='leased' AND q.lease_expires_at<=p_now)
    ORDER BY COALESCE(q.lease_expires_at,q.next_attempt_at),q.queued_at,q.execution_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;
    UPDATE spyglass.work_agent_execution_queue q SET
        state='leased',lease_id=p_lease_id,lease_expires_at=p_now+(p_lease_seconds*interval '1 second'),
        attempt_count=q.attempt_count+1,last_error_code=NULL,updated_at=p_now
    WHERE q.account_id=candidate.account_id AND q.execution_id=candidate.execution_id
    RETURNING q.attempt_count INTO claimed_attempt;
    RETURN QUERY SELECT candidate.execution_id,candidate.account_id,candidate.work_item_id,p_lease_id,claimed_attempt;
END;
$$;

CREATE FUNCTION public.spyglass_heartbeat_work_agent_execution(
    p_execution_id uuid,p_account_id uuid,p_lease_id uuid,p_now timestamptz,p_lease_seconds integer
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
BEGIN
    IF p_execution_id IS NULL OR p_account_id IS NULL OR p_lease_id IS NULL OR p_now IS NULL OR
       p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Work Agent execution heartbeat';
    END IF;
    UPDATE spyglass.work_agent_execution_queue SET lease_expires_at=p_now+(p_lease_seconds*interval '1 second'),updated_at=p_now
    WHERE account_id=p_account_id AND execution_id=p_execution_id AND state='leased' AND lease_id=p_lease_id
      AND lease_expires_at>=statement_timestamp();
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Work Agent execution lease lost'; END IF;
    RETURN true;
END;
$$;

CREATE FUNCTION public.spyglass_load_work_agent_execution(
    p_execution_id uuid,p_account_id uuid,p_work_item_id uuid,p_lease_id uuid
) RETURNS TABLE(work_version bigint,initiating_user_id uuid,persona_id uuid,persona_version_id uuid,
    boardroom_id uuid,boardroom_version bigint,planned_run_id uuid,planned_conversation_id uuid,
    title text,description text,queued_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record;
BEGIN
    IF p_execution_id IS NULL OR p_account_id IS NULL OR p_work_item_id IS NULL OR p_lease_id IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Work Agent execution load';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.work_agent_execution_queue q
    WHERE q.account_id=p_account_id AND q.execution_id=p_execution_id AND q.work_item_id=p_work_item_id;
    IF NOT FOUND OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Work Agent execution lease lost';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    RETURN QUERY SELECT e.work_version,e.initiating_user_id,e.persona_id,e.persona_version_id,e.boardroom_id,
        e.boardroom_version,e.planned_run_id,e.planned_conversation_id,e.title,e.description,e.queued_at
    FROM spyglass.work_agent_executions e
    WHERE e.account_id=p_account_id AND e.execution_id=p_execution_id AND e.work_item_id=p_work_item_id;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Work Agent execution intent unavailable'; END IF;
END;
$$;

-- This is the only worker mutation boundary. It rechecks the frozen Work and
-- Persona inputs, serializes Account Run capacity, creates the complete Agent
-- plan, links Work, and retires the lease in one transaction.
CREATE FUNCTION public.spyglass_start_link_work_agent_execution(
    p_execution_id uuid,p_account_id uuid,p_lease_id uuid,p_entitlement_version bigint,p_maximum_concurrent_runs bigint,
    p_plan_digest bytea,p_event_id uuid,p_user_message_id uuid,p_invocation_id uuid,p_profile text,
    p_model_operation_ids uuid[],p_tool_operation_ids uuid[],p_request_expires_at timestamptz,p_now timestamptz
) RETURNS TABLE(created_run boolean,linked_run boolean,reconciled boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record; execution_row record; work_row record; persona_row record; boardroom_row record; active_runs bigint;
DECLARE context_sequence bigint;
BEGIN
    IF p_execution_id IS NULL OR p_account_id IS NULL OR p_lease_id IS NULL OR p_entitlement_version IS NULL OR p_entitlement_version<1 OR
       p_maximum_concurrent_runs IS NULL OR p_maximum_concurrent_runs<1 OR p_plan_digest IS NULL OR octet_length(p_plan_digest)<>32 OR
       p_event_id IS NULL OR p_user_message_id IS NULL OR p_invocation_id IS NULL OR p_profile IS NULL OR
       p_profile !~ '^[a-z][a-z0-9-]{0,49}$' OR p_model_operation_ids IS NULL OR p_tool_operation_ids IS NULL OR
       cardinality(p_model_operation_ids) NOT BETWEEN 1 AND 6 OR cardinality(p_tool_operation_ids) NOT BETWEEN 0 AND 5 OR
       cardinality(p_model_operation_ids)<>cardinality(p_tool_operation_ids)+1 OR p_request_expires_at IS NULL OR p_now IS NULL OR
       p_request_expires_at<=p_now OR p_request_expires_at>p_now+interval '24 hours' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Work Agent execution start/link';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.work_agent_execution_queue q
    WHERE q.account_id=p_account_id AND q.execution_id=p_execution_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Work Agent execution snapshot unavailable'; END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT e.* INTO execution_row FROM spyglass.work_agent_executions e
    WHERE e.account_id=p_account_id AND e.execution_id=p_execution_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Work Agent execution intent unavailable'; END IF;
    IF queue_row.state='linked' THEN
        IF execution_row.linked_run_id=execution_row.planned_run_id AND execution_row.linked_conversation_id=execution_row.planned_conversation_id AND
           EXISTS (SELECT 1 FROM spyglass.work_items w WHERE w.account_id=p_account_id AND w.id=execution_row.work_item_id
                   AND w.run_id=execution_row.planned_run_id AND w.conversation_id=execution_row.planned_conversation_id) AND
           EXISTS (SELECT 1 FROM spyglass.agent_runs r WHERE r.account_id=p_account_id AND r.id=execution_row.planned_run_id
                   AND r.conversation_id=execution_row.planned_conversation_id) THEN
            RETURN QUERY SELECT false,true,true; RETURN;
        END IF;
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Work Agent execution linked state is corrupt';
    END IF;
    IF queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Work Agent execution lease lost';
    END IF;
    SELECT w.* INTO work_row FROM spyglass.work_items w
    WHERE w.account_id=p_account_id AND w.id=execution_row.work_item_id FOR UPDATE;
    IF NOT FOUND OR work_row.version<>execution_row.work_version OR work_row.responsibility<>'persona' OR
       work_row.assignee_persona_id<>execution_row.persona_id OR work_row.state NOT IN ('open','in_progress','waiting') OR
       work_row.run_id IS NOT NULL OR work_row.conversation_id IS NOT NULL OR work_row.title<>execution_row.title OR
       work_row.description<>execution_row.description THEN
        RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='Work Agent execution Work changed';
    END IF;
    SELECT v.*,p.state AS persona_state,p.boardroom_id INTO persona_row
    FROM spyglass.agent_persona_versions v JOIN spyglass.agent_personas p
      ON p.account_id=v.account_id AND p.id=v.persona_id
    WHERE v.account_id=p_account_id AND v.id=execution_row.persona_version_id AND v.persona_id=execution_row.persona_id
    FOR SHARE OF v,p;
    IF NOT FOUND OR persona_row.persona_state<>'active' OR persona_row.boardroom_id<>execution_row.boardroom_id THEN
        RAISE EXCEPTION USING ERRCODE='P0004', MESSAGE='Work Agent execution Persona unavailable';
    END IF;
    SELECT b.* INTO boardroom_row FROM spyglass.agent_boardrooms b
    WHERE b.account_id=p_account_id AND b.id=execution_row.boardroom_id FOR SHARE;
    IF NOT FOUND OR boardroom_row.state<>'active' OR boardroom_row.version<>execution_row.boardroom_version THEN
        RAISE EXCEPTION USING ERRCODE='P0004', MESSAGE='Work Agent execution Persona unavailable';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('agent-runs:'||p_account_id::text,0));
    SELECT count(*) INTO active_runs FROM spyglass.agent_runs
    WHERE account_id=p_account_id AND state IN ('planned','running');
    IF active_runs>=p_maximum_concurrent_runs THEN
        RAISE EXCEPTION USING ERRCODE='P0005', MESSAGE='Work Agent execution Run capacity unavailable';
    END IF;
    INSERT INTO spyglass.agent_conversations
        (account_id,id,boardroom_id,subject,state,next_message_sequence,created_by,created_at,updated_at)
    VALUES (p_account_id,execution_row.planned_conversation_id,execution_row.boardroom_id,execution_row.title,'open',2,
            execution_row.initiating_user_id,p_now,p_now);
    context_sequence:=1;
    INSERT INTO spyglass.agent_user_messages
        (account_id,id,conversation_id,sequence,body,created_by,created_at)
    VALUES (p_account_id,p_user_message_id,execution_row.planned_conversation_id,context_sequence,
            CASE WHEN btrim(execution_row.description)='' THEN execution_row.title ELSE execution_row.title||E'\n\n'||execution_row.description END,
            execution_row.initiating_user_id,p_now);
    INSERT INTO spyglass.agent_runs
        (account_id,id,boardroom_id,conversation_id,state,entitlement_version,policy_version,plan_digest,turn_count,created_by,created_at)
    VALUES (p_account_id,execution_row.planned_run_id,execution_row.boardroom_id,execution_row.planned_conversation_id,'planned',
            p_entitlement_version,execution_row.boardroom_version,p_plan_digest,1,execution_row.initiating_user_id,p_now);
    INSERT INTO spyglass.agent_run_plan_turns
        (account_id,run_id,turn,persona_id,persona_version_id,persona_digest)
    VALUES (p_account_id,execution_row.planned_run_id,1,execution_row.persona_id,execution_row.persona_version_id,persona_row.content_digest);
    INSERT INTO spyglass.agent_invocations
        (account_id,id,run_id,turn,persona_version_id,status,expected_provider,requested_model,queued_at)
    VALUES (p_account_id,p_invocation_id,execution_row.planned_run_id,1,execution_row.persona_version_id,'queued',
            persona_row.policy->>'provider',persona_row.policy->>'model',p_now);
    INSERT INTO spyglass.agent_invocation_execution_plans
        (account_id,invocation_id,conversation_id,context_sequence,profile,model_operation_ids,tool_operation_ids,request_expires_at,created_at)
    VALUES (p_account_id,p_invocation_id,execution_row.planned_conversation_id,context_sequence,p_profile,
            p_model_operation_ids,p_tool_operation_ids,p_request_expires_at,p_now);
    UPDATE spyglass.work_items SET state='in_progress',conversation_id=execution_row.planned_conversation_id,
        run_id=execution_row.planned_run_id,version=version+1,updated_at=p_now
    WHERE account_id=p_account_id AND id=execution_row.work_item_id;
    INSERT INTO spyglass.work_item_events
        (account_id,id,work_item_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
    VALUES (p_account_id,p_event_id,execution_row.work_item_id,'agent_execution_linked',execution_row.work_version,
            execution_row.work_version+1,'workload','work-agent-execution','Persona execution started',p_execution_id::text,
            jsonb_build_object('state','in_progress','responsibility','persona','reference_kind','run'),p_now);
    UPDATE spyglass.work_agent_executions SET linked_run_id=planned_run_id,linked_conversation_id=planned_conversation_id,
        linked_at=p_now,updated_at=p_now
    WHERE account_id=p_account_id AND execution_id=p_execution_id;
    UPDATE spyglass.work_agent_execution_queue SET state='linked',lease_id=NULL,lease_expires_at=NULL,
        last_error_code=NULL,linked_at=p_now,updated_at=p_now
    WHERE account_id=p_account_id AND execution_id=p_execution_id;
    RETURN QUERY SELECT true,true,false;
END;
$$;

CREATE FUNCTION public.spyglass_fail_work_agent_execution(
    p_execution_id uuid,p_account_id uuid,p_lease_id uuid,p_retry boolean,p_next_attempt_at timestamptz,
    p_error_code text,p_now timestamptz,p_max_attempts integer
) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record; next_state text;
BEGIN
    IF p_execution_id IS NULL OR p_account_id IS NULL OR p_lease_id IS NULL OR p_retry IS NULL OR
       p_next_attempt_at IS NULL OR p_next_attempt_at<p_now OR p_error_code IS NULL OR p_error_code !~ '^[a-z][a-z0-9_]{0,99}$' OR
       p_now IS NULL OR p_max_attempts IS NULL OR p_max_attempts NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Work Agent execution failure';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.work_agent_execution_queue q
    WHERE q.account_id=p_account_id AND q.execution_id=p_execution_id FOR UPDATE;
    IF NOT FOUND OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Work Agent execution lease lost';
    END IF;
    next_state:=CASE WHEN p_retry AND queue_row.attempt_count<p_max_attempts THEN 'retry' ELSE 'dead_letter' END;
    UPDATE spyglass.work_agent_execution_queue SET state=next_state,next_attempt_at=p_next_attempt_at,
        lease_id=NULL,lease_expires_at=NULL,last_error_code=p_error_code,updated_at=p_now
    WHERE account_id=p_account_id AND execution_id=p_execution_id;
    RETURN next_state;
END;
$$;

CREATE FUNCTION public.spyglass_work_agent_execution_stats(p_now timestamptz)
RETURNS TABLE(pending bigint,ready bigint,leased bigint,retrying bigint,linked bigint,dead_letter bigint,oldest_ready_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
    SELECT count(*) FILTER (WHERE state='pending'),
           count(*) FILTER (WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now)),
           count(*) FILTER (WHERE state='leased' AND lease_expires_at>p_now),
           count(*) FILTER (WHERE state='retry'),count(*) FILTER (WHERE state='linked'),
           count(*) FILTER (WHERE state='dead_letter'),
           min(COALESCE(lease_expires_at,next_attempt_at)) FILTER (
               WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now))
    FROM spyglass.work_agent_execution_queue
$$;

REVOKE ALL ON FUNCTION public.spyglass_claim_work_agent_execution(uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_heartbeat_work_agent_execution(uuid,uuid,uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_load_work_agent_execution(uuid,uuid,uuid,uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_start_link_work_agent_execution(uuid,uuid,uuid,bigint,bigint,bytea,uuid,uuid,uuid,text,uuid[],uuid[],timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_fail_work_agent_execution(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_work_agent_execution_stats(timestamptz) FROM PUBLIC;

-- Exact Account erasure counts the bridge queue through the existing
-- cascade, without widening the stable erasure function signature.
CREATE FUNCTION spyglass.capture_work_agent_execution_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts jsonb; current_count bigint;
BEGIN
    IF current_setting('spyglass.erasure_request_id',true)<>'' AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        counts:=COALESCE(NULLIF(current_setting('spyglass.work_agent_execution_erasure_counts',true),'')::jsonb,'{}'::jsonb);
        current_count:=COALESCE((counts->>TG_TABLE_NAME)::bigint,0)+1;
        PERFORM set_config('spyglass.work_agent_execution_erasure_counts',(counts||jsonb_build_object(TG_TABLE_NAME,current_count))::text,true);
    END IF;
    RETURN OLD;
END;
$$;
CREATE FUNCTION spyglass.add_work_agent_execution_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.work_agent_execution_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER work_agent_execution_erasure_count BEFORE DELETE ON spyglass.work_agent_executions
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_work_agent_execution_erasure_count();
CREATE TRIGGER work_agent_execution_queue_erasure_count BEFORE DELETE ON spyglass.work_agent_execution_queue
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_work_agent_execution_erasure_count();
CREATE TRIGGER account_erasure_work_agent_execution_count BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_work_agent_execution_erasure_count();
REVOKE ALL ON FUNCTION spyglass.capture_work_agent_execution_erasure_count() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.add_work_agent_execution_erasure_count() FROM PUBLIC;

COMMIT;
