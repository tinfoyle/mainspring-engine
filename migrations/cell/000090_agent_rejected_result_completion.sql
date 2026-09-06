BEGIN;

-- Authenticated completed results can fail application validation. Preserve the
-- same account, lease and digest checks and the execute-only worker authority;
-- only these two application rejection codes may close a completed result.
-- Rejected output never becomes a message or approval.
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
    IF invocation_row.id IS NULL OR NOT COALESCE(exchange_row.result_outcome='execution_failed' OR (exchange_row.result_outcome='completed' AND p_failure_code IN ('turn_output_invalid','result_policy_denied')),false) OR exchange_row.result_digest IS DISTINCT FROM p_runner_result_digest THEN
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

COMMIT;
