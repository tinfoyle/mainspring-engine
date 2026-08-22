BEGIN;

-- Cost is calculated by the trusted model gateway from an operator-owned exact
-- model price book, enforced by the runner against the immutable Persona
-- ceiling, and retained with the invocation for audit and recovery.
ALTER TABLE spyglass.agent_invocations
    ADD COLUMN cost_micros bigint NOT NULL DEFAULT 0 CHECK (cost_micros>=0);

DROP FUNCTION public.spyglass_project_agent_invocation_success(
    uuid,uuid,uuid,uuid,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,timestamptz,timestamptz
);

CREATE FUNCTION public.spyglass_project_agent_invocation_success(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_message_id uuid,p_provider text,p_response_model text,p_provider_response_id text,
    p_runner_result_digest bytea,p_result_digest bytea,p_result_payload jsonb,p_body text,
    p_input_tokens bigint,p_output_tokens bigint,p_total_tokens bigint,p_cost_micros bigint,p_completed_at timestamptz,p_now timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE invocation_row record; exchange_row record; run_row record; queue_row record; existing_message record; assigned_sequence bigint;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_lease_id IS NULL OR p_message_id IS NULL OR p_provider IS NULL OR
       p_provider !~ '^[a-z][a-z0-9-]{0,31}$' OR p_response_model IS NULL OR p_response_model !~ '^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$' OR
       p_provider_response_id IS NULL OR char_length(p_provider_response_id) NOT BETWEEN 1 AND 200 OR p_provider_response_id ~ '[[:space:][:cntrl:]]' OR
       p_runner_result_digest IS NULL OR octet_length(p_runner_result_digest)<>32 OR p_result_digest IS NULL OR octet_length(p_result_digest)<>32 OR
       p_result_payload IS NULL OR jsonb_typeof(p_result_payload)<>'object' OR octet_length(p_result_payload::text)>262144 OR
       p_body IS NULL OR char_length(btrim(p_body)) NOT BETWEEN 1 AND 65536 OR p_result_payload->>'contribution' IS DISTINCT FROM btrim(p_body) OR
       p_input_tokens IS NULL OR p_input_tokens<0 OR p_output_tokens IS NULL OR p_output_tokens<0 OR
       p_total_tokens IS NULL OR p_total_tokens<>p_input_tokens+p_output_tokens OR p_cost_micros IS NULL OR p_cost_micros<0 OR
       p_completed_at IS NULL OR p_now IS NULL OR p_completed_at>p_now THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent invocation success projection';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.agent_result_projection_queue q
    WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id FOR UPDATE;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    IF FOUND AND queue_row.state='projected' THEN
        SELECT i.* INTO invocation_row FROM spyglass.agent_invocations i
        WHERE i.account_id=p_account_id AND i.id=p_invocation_id;
        SELECT m.* INTO existing_message FROM spyglass.agent_messages m
        WHERE m.account_id=p_account_id AND m.invocation_id=p_invocation_id;
        IF invocation_row.status='succeeded' AND existing_message.id=p_message_id AND invocation_row.response_model=p_response_model AND
           invocation_row.provider_response_id=p_provider_response_id AND invocation_row.runner_result_digest=p_runner_result_digest AND
           invocation_row.result_digest=p_result_digest AND invocation_row.result_payload=p_result_payload AND
           invocation_row.input_tokens=p_input_tokens AND invocation_row.output_tokens=p_output_tokens AND invocation_row.total_tokens=p_total_tokens AND
           invocation_row.cost_micros=p_cost_micros AND existing_message.result_digest=p_result_digest AND existing_message.body=btrim(p_body) THEN RETURN false; END IF;
        RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent invocation success projection conflicts with existing result';
    END IF;
    IF queue_row.invocation_id IS NULL OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent result projection lease lost';
    END IF;
    SELECT i.* INTO invocation_row FROM spyglass.agent_invocations i
    WHERE i.account_id=p_account_id AND i.id=p_invocation_id FOR UPDATE;
    IF NOT FOUND OR invocation_row.expected_provider<>p_provider THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent invocation projection identity denied';
    END IF;
    SELECT x.result_outcome,x.result_digest INTO exchange_row FROM spyglass.runner_invocation_exchanges x
    WHERE x.account_id=p_account_id AND x.invocation_id=p_invocation_id FOR SHARE;
    IF NOT FOUND OR exchange_row.result_outcome<>'completed' OR exchange_row.result_digest<>p_runner_result_digest THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent invocation runner result binding denied';
    END IF;
    IF invocation_row.status NOT IN ('queued','running') OR p_completed_at<invocation_row.queued_at THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent invocation cannot be projected from its current state';
    END IF;
    SELECT r.conversation_id INTO run_row FROM spyglass.agent_runs r
    WHERE r.account_id=p_account_id AND r.id=invocation_row.run_id FOR SHARE;
    UPDATE spyglass.agent_conversations c SET next_message_sequence=c.next_message_sequence+1,updated_at=GREATEST(c.updated_at,p_completed_at)
    WHERE c.account_id=p_account_id AND c.id=run_row.conversation_id
    RETURNING c.next_message_sequence-1 INTO assigned_sequence;
    IF assigned_sequence IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent conversation projection target missing'; END IF;
    UPDATE spyglass.agent_invocations SET status='succeeded',response_model=p_response_model,provider_response_id=p_provider_response_id,
        runner_result_digest=p_runner_result_digest,result_digest=p_result_digest,result_payload=p_result_payload,
        input_tokens=p_input_tokens,output_tokens=p_output_tokens,total_tokens=p_total_tokens,cost_micros=p_cost_micros,completed_at=p_completed_at
    WHERE account_id=p_account_id AND id=p_invocation_id;
    INSERT INTO spyglass.agent_messages
        (account_id,id,conversation_id,run_id,invocation_id,sequence,role,persona_version_id,body,structured_result,result_digest,created_at)
    VALUES
        (p_account_id,p_message_id,run_row.conversation_id,invocation_row.run_id,p_invocation_id,assigned_sequence,'persona',
         invocation_row.persona_version_id,btrim(p_body),p_result_payload,p_result_digest,p_completed_at);
    UPDATE spyglass.agent_runs r SET state=CASE
            WHEN (SELECT count(*) FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.run_id=r.id)=r.turn_count
             AND NOT EXISTS (SELECT 1 FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.run_id=r.id AND i.status<>'succeeded')
            THEN 'succeeded' ELSE 'running' END,
        started_at=COALESCE(r.started_at,p_completed_at),
        completed_at=CASE WHEN (SELECT count(*) FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.run_id=r.id)=r.turn_count
             AND NOT EXISTS (SELECT 1 FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.run_id=r.id AND i.status<>'succeeded')
            THEN p_completed_at ELSE NULL END
    WHERE r.account_id=p_account_id AND r.id=invocation_row.run_id;
    UPDATE spyglass.agent_result_projection_queue SET state='projected',lease_id=NULL,lease_expires_at=NULL,
        last_error_code=NULL,projected_at=p_now,updated_at=p_now
    WHERE account_id=p_account_id AND invocation_id=p_invocation_id;
    RETURN true;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_project_agent_invocation_success(
    uuid,uuid,uuid,uuid,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz
) FROM PUBLIC;

COMMIT;
