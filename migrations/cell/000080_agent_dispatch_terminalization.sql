BEGIN;

-- A dispatch can be denied before a runner exists (for example when the
-- Account has insufficient AI Tokens). Such failures have no runner result
-- digest by definition, but they must still become visible terminal Agent
-- state instead of leaving the Run permanently planned.
ALTER TABLE spyglass.agent_invocations
    DROP CONSTRAINT agent_invocations_check1,
    ADD CONSTRAINT agent_invocations_result_shape_v2 CHECK (
        (status='succeeded' AND response_model IS NOT NULL AND provider_response_id IS NOT NULL AND
         runner_result_digest IS NOT NULL AND result_digest IS NOT NULL AND result_payload IS NOT NULL AND
         input_tokens IS NOT NULL AND output_tokens IS NOT NULL AND total_tokens=input_tokens+output_tokens AND failure_code IS NULL) OR
        (status IN ('queued','running') AND response_model IS NULL AND provider_response_id IS NULL AND
         runner_result_digest IS NULL AND result_digest IS NULL AND result_payload IS NULL AND
         input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL AND failure_code IS NULL) OR
        (status='failed' AND response_model IS NULL AND provider_response_id IS NULL AND result_digest IS NULL AND
         result_payload IS NULL AND input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL AND failure_code IS NOT NULL) OR
        (status='canceled' AND response_model IS NULL AND provider_response_id IS NULL AND result_digest IS NULL AND
         result_payload IS NULL AND input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL AND failure_code IS NOT NULL)
    );

CREATE OR REPLACE FUNCTION public.spyglass_fail_agent_dispatch(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_retry boolean,p_next_attempt_at timestamptz,
    p_error_code text,p_now timestamptz,p_max_attempts integer
) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE
    queue_row record;
    invocation_row record;
    next_state text;
    succeeded_count bigint;
    terminal_state text;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_lease_id IS NULL OR p_retry IS NULL OR
       p_next_attempt_at IS NULL OR p_next_attempt_at<p_now OR p_error_code IS NULL OR p_error_code !~ '^[a-z][a-z0-9_]{0,99}$' OR
       p_now IS NULL OR p_max_attempts IS NULL OR p_max_attempts NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent dispatch failure';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.agent_dispatch_queue q
    WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id FOR UPDATE;
    IF queue_row.invocation_id IS NULL OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent dispatch lease lost';
    END IF;
    next_state:=CASE WHEN p_retry AND queue_row.attempt_count<p_max_attempts THEN 'retry' ELSE 'dead_letter' END;
    UPDATE spyglass.agent_dispatch_queue SET state=next_state,next_attempt_at=p_next_attempt_at,
        lease_id=NULL,lease_expires_at=NULL,last_error_code=p_error_code,updated_at=p_now
    WHERE account_id=p_account_id AND invocation_id=p_invocation_id;

    IF next_state='dead_letter' THEN
        PERFORM set_config('app.account_id',p_account_id::text,true);
        SELECT i.* INTO invocation_row FROM spyglass.agent_invocations i
        WHERE i.account_id=p_account_id AND i.id=p_invocation_id FOR UPDATE;
        IF invocation_row.id IS NULL OR invocation_row.status<>'queued' THEN
            RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent dispatch terminal state binding denied';
        END IF;
        UPDATE spyglass.agent_invocations SET status='failed',failure_code=p_error_code,completed_at=p_now
        WHERE account_id=p_account_id AND id=p_invocation_id;
        UPDATE spyglass.agent_invocations SET status='canceled',failure_code='prior_turn_failed',completed_at=p_now
        WHERE account_id=p_account_id AND run_id=invocation_row.run_id AND turn>invocation_row.turn AND status='queued';
        SELECT count(*) INTO succeeded_count FROM spyglass.agent_invocations
        WHERE account_id=p_account_id AND run_id=invocation_row.run_id AND status='succeeded';
        terminal_state:=CASE WHEN succeeded_count>0 THEN 'partially_failed' ELSE 'failed' END;
        UPDATE spyglass.agent_runs SET state=terminal_state,started_at=COALESCE(started_at,p_now),completed_at=p_now
        WHERE account_id=p_account_id AND id=invocation_row.run_id;
        UPDATE spyglass.agent_result_projection_queue q SET state='projected',lease_id=NULL,lease_expires_at=NULL,
            last_error_code=NULL,projected_at=p_now,updated_at=p_now
        FROM spyglass.agent_invocations i
        WHERE i.account_id=p_account_id AND i.run_id=invocation_row.run_id AND i.turn>=invocation_row.turn AND
              i.status IN ('failed','canceled') AND q.account_id=i.account_id AND q.invocation_id=i.id AND q.state<>'projected';
    END IF;
    RETURN next_state;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_fail_agent_dispatch(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer) FROM PUBLIC;

COMMIT;
