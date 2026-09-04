BEGIN;

-- Persona-owned Work must not remain permanently in progress when a runner
-- reaches the model but cannot complete the turn. Retry the immutable Work
-- intent twice (three total executions), then make the failure visible as
-- waiting Work instead of silently spinning or abandoning it.
CREATE OR REPLACE FUNCTION spyglass.reconcile_failed_work_agent_execution(
    p_account_id uuid,p_invocation_id uuid,p_now timestamptz
) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE execution_row record; work_row record; execution_count integer; next_attempt integer;
DECLARE retry_execution_id uuid; retry_run_id uuid; retry_conversation_id uuid; work_event_id uuid;
DECLARE seed text;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid failed Work Agent reconciliation';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT execution.* INTO execution_row
    FROM spyglass.agent_invocations invocation
    JOIN spyglass.work_agent_executions execution
      ON execution.account_id=invocation.account_id AND execution.planned_run_id=invocation.run_id
    WHERE invocation.account_id=p_account_id AND invocation.id=p_invocation_id;
    IF NOT FOUND THEN RETURN 'not_work'; END IF;

    SELECT work.* INTO work_row FROM spyglass.work_items work
    WHERE work.account_id=p_account_id AND work.id=execution_row.work_item_id FOR UPDATE;
    IF NOT FOUND OR work_row.state<>'in_progress' OR work_row.responsibility<>'persona' OR
       work_row.run_id IS DISTINCT FROM execution_row.planned_run_id THEN
        RETURN 'not_active';
    END IF;
    SELECT count(*) INTO execution_count FROM spyglass.work_agent_executions execution
    WHERE execution.account_id=p_account_id AND execution.work_item_id=work_row.id;

    IF execution_count<3 THEN
        next_attempt:=execution_count+1;
        seed:=work_row.id::text||'/agent-failure-retry/'||next_attempt::text;
        retry_execution_id:=(substr(md5(seed||'/execution'),1,8)||'-'||substr(md5(seed||'/execution'),9,4)||'-'||
            substr(md5(seed||'/execution'),13,4)||'-'||substr(md5(seed||'/execution'),17,4)||'-'||
            substr(md5(seed||'/execution'),21,12))::uuid;
        retry_run_id:=(substr(md5(seed||'/run'),1,8)||'-'||substr(md5(seed||'/run'),9,4)||'-'||
            substr(md5(seed||'/run'),13,4)||'-'||substr(md5(seed||'/run'),17,4)||'-'||
            substr(md5(seed||'/run'),21,12))::uuid;
        retry_conversation_id:=(substr(md5(seed||'/conversation'),1,8)||'-'||substr(md5(seed||'/conversation'),9,4)||'-'||
            substr(md5(seed||'/conversation'),13,4)||'-'||substr(md5(seed||'/conversation'),17,4)||'-'||
            substr(md5(seed||'/conversation'),21,12))::uuid;
        work_event_id:=(substr(md5(seed||'/event'),1,8)||'-'||substr(md5(seed||'/event'),9,4)||'-'||
            substr(md5(seed||'/event'),13,4)||'-'||substr(md5(seed||'/event'),17,4)||'-'||
            substr(md5(seed||'/event'),21,12))::uuid;

        UPDATE spyglass.work_items SET state='open',conversation_id=NULL,run_id=NULL,completed_at=NULL,
            version=version+1,updated_at=p_now
        WHERE account_id=p_account_id AND id=work_row.id;
        INSERT INTO spyglass.work_item_events
            (account_id,id,work_item_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
        VALUES (p_account_id,work_event_id,work_row.id,'transitioned',work_row.version,work_row.version+1,'workload',
                'work-agent-failure-recovery','Agent execution retry scheduled',retry_execution_id::text,
                jsonb_build_object('state','open','responsibility','persona','attempt',next_attempt),p_now);
        INSERT INTO spyglass.work_agent_executions
            (account_id,execution_id,work_item_id,work_version,initiating_user_id,persona_id,persona_version_id,boardroom_id,
             boardroom_version,planned_run_id,planned_conversation_id,title,description,queued_at,updated_at)
        VALUES (p_account_id,retry_execution_id,work_row.id,work_row.version+1,execution_row.initiating_user_id,
                execution_row.persona_id,execution_row.persona_version_id,execution_row.boardroom_id,execution_row.boardroom_version,
                retry_run_id,retry_conversation_id,execution_row.title,execution_row.description,p_now,p_now);
        INSERT INTO spyglass.work_agent_execution_queue
            (account_id,execution_id,work_item_id,state,next_attempt_at,queued_at,updated_at)
        VALUES (p_account_id,retry_execution_id,work_row.id,'pending',p_now,p_now,p_now);
        RETURN 'retry_scheduled';
    END IF;

    seed:=work_row.id::text||'/agent-failure-waiting/'||execution_count::text;
    work_event_id:=(substr(md5(seed||'/event'),1,8)||'-'||substr(md5(seed||'/event'),9,4)||'-'||
        substr(md5(seed||'/event'),13,4)||'-'||substr(md5(seed||'/event'),17,4)||'-'||
        substr(md5(seed||'/event'),21,12))::uuid;
    UPDATE spyglass.work_items SET state='waiting',completed_at=NULL,version=version+1,updated_at=p_now
    WHERE account_id=p_account_id AND id=work_row.id;
    INSERT INTO spyglass.work_item_events
        (account_id,id,work_item_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
    VALUES (p_account_id,work_event_id,work_row.id,'transitioned',work_row.version,work_row.version+1,'workload',
            'work-agent-failure-recovery','Agent execution requires operator attention',p_invocation_id::text,
            jsonb_build_object('state','waiting','responsibility','persona','attempts',execution_count),p_now);
    RETURN 'waiting';
END;
$$;
REVOKE ALL ON FUNCTION spyglass.reconcile_failed_work_agent_execution(uuid,uuid,timestamptz) FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.spyglass_project_agent_invocation_failure(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_runner_result_digest bytea,p_failure_code text,
    p_completed_at timestamptz,p_now timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE invocation_row record; exchange_row record; queue_row record; succeeded_count bigint; terminal_state text;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_lease_id IS NULL OR p_runner_result_digest IS NULL OR octet_length(p_runner_result_digest)<>32 OR
       p_failure_code IS NULL OR p_failure_code !~ '^[a-z][a-z0-9_]{0,99}$' OR p_completed_at IS NULL OR p_now IS NULL OR p_completed_at>p_now THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent invocation failure projection';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.agent_result_projection_queue q
    WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id FOR UPDATE;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    IF FOUND AND queue_row.state='projected' THEN
        SELECT i.* INTO invocation_row FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.id=p_invocation_id;
        IF invocation_row.status='failed' AND invocation_row.runner_result_digest=p_runner_result_digest AND invocation_row.failure_code=p_failure_code THEN RETURN false; END IF;
        RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent invocation failure projection conflicts with existing result';
    END IF;
    IF queue_row.invocation_id IS NULL OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent result projection lease lost';
    END IF;
    SELECT i.* INTO invocation_row FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.id=p_invocation_id FOR UPDATE;
    SELECT x.result_outcome,x.result_digest INTO exchange_row FROM spyglass.runner_invocation_exchanges x
    WHERE x.account_id=p_account_id AND x.invocation_id=p_invocation_id FOR SHARE;
    IF invocation_row.id IS NULL OR exchange_row.result_outcome<>'execution_failed' OR exchange_row.result_digest<>p_runner_result_digest THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent invocation failure binding denied';
    END IF;
    IF invocation_row.status NOT IN ('queued','running') OR p_completed_at<invocation_row.queued_at THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent invocation cannot be failed from its current state';
    END IF;
    UPDATE spyglass.agent_invocations SET status='failed',runner_result_digest=p_runner_result_digest,failure_code=p_failure_code,completed_at=p_completed_at
    WHERE account_id=p_account_id AND id=p_invocation_id;
    UPDATE spyglass.agent_invocations SET status='canceled',failure_code='prior_turn_failed',completed_at=p_completed_at
    WHERE account_id=p_account_id AND run_id=invocation_row.run_id AND turn>invocation_row.turn AND status='queued';
    SELECT count(*) INTO succeeded_count FROM spyglass.agent_invocations
    WHERE account_id=p_account_id AND run_id=invocation_row.run_id AND status='succeeded';
    terminal_state:=CASE WHEN succeeded_count>0 THEN 'partially_failed' ELSE 'failed' END;
    UPDATE spyglass.agent_runs SET state=terminal_state,started_at=COALESCE(started_at,p_completed_at),completed_at=p_completed_at
    WHERE account_id=p_account_id AND id=invocation_row.run_id;
    UPDATE spyglass.agent_result_projection_queue SET state='projected',lease_id=NULL,lease_expires_at=NULL,
        last_error_code=NULL,projected_at=p_now,updated_at=p_now
    WHERE account_id=p_account_id AND invocation_id=p_invocation_id;
    PERFORM spyglass.reconcile_failed_work_agent_execution(p_account_id,p_invocation_id,p_now);
    RETURN true;
END;
$$;

-- Capacity is backpressure, not an execution failure. Undo the claim's
-- attempt increment, retry promptly, and never dead-letter for this reason.
CREATE OR REPLACE FUNCTION public.spyglass_fail_work_agent_execution(
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
    IF p_error_code='run_capacity' THEN
        UPDATE spyglass.work_agent_execution_queue SET state='retry',attempt_count=GREATEST(attempt_count-1,0),
            next_attempt_at=p_now+interval '5 seconds',lease_id=NULL,lease_expires_at=NULL,last_error_code=p_error_code,updated_at=p_now
        WHERE account_id=p_account_id AND execution_id=p_execution_id;
        RETURN 'retry';
    END IF;
    next_state:=CASE WHEN p_retry AND queue_row.attempt_count<p_max_attempts THEN 'retry' ELSE 'dead_letter' END;
    UPDATE spyglass.work_agent_execution_queue SET state=next_state,next_attempt_at=p_next_attempt_at,
        lease_id=NULL,lease_expires_at=NULL,last_error_code=p_error_code,updated_at=p_now
    WHERE account_id=p_account_id AND execution_id=p_execution_id;
    RETURN next_state;
END;
$$;

-- Repair capacity deferrals accumulated under the old semantics.
UPDATE spyglass.work_agent_execution_queue SET state='retry',attempt_count=0,next_attempt_at=statement_timestamp(),
    lease_id=NULL,lease_expires_at=NULL,last_error_code='run_capacity',linked_at=NULL,updated_at=statement_timestamp()
WHERE state IN ('retry','dead_letter') AND last_error_code='run_capacity'
  AND NOT EXISTS (
      SELECT 1 FROM spyglass.work_agent_execution_queue active
      WHERE active.account_id=work_agent_execution_queue.account_id
        AND active.work_item_id=work_agent_execution_queue.work_item_id
        AND active.execution_id<>work_agent_execution_queue.execution_id
        AND active.state IN ('pending','leased','retry')
  );

-- Reconcile executions already projected as failures before this migration.
DO $$
DECLARE failed record;
BEGIN
    FOR failed IN
        SELECT invocation.account_id,invocation.id AS invocation_id
        FROM spyglass.agent_invocations invocation
        JOIN spyglass.agent_result_projection_queue projection
          ON projection.account_id=invocation.account_id AND projection.invocation_id=invocation.id
        JOIN spyglass.work_agent_executions execution
          ON execution.account_id=invocation.account_id AND execution.planned_run_id=invocation.run_id
        JOIN spyglass.work_items work
          ON work.account_id=execution.account_id AND work.id=execution.work_item_id AND work.run_id=invocation.run_id
        WHERE invocation.status='failed' AND projection.state='projected'
          AND work.state='in_progress' AND work.responsibility='persona'
    LOOP
        PERFORM spyglass.reconcile_failed_work_agent_execution(failed.account_id,failed.invocation_id,statement_timestamp());
    END LOOP;
END;
$$;

COMMIT;
