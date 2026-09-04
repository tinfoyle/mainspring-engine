BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='60s';

-- Work-created Runs use the same provider-neutral admission contract as Runs
-- created from the Agent conversation surface. The three placeholder targets
-- reserve the maximum operation-ID shape; AI Token admission later replaces
-- them with the current private provider/model targets for the Persona's
-- durable complexity choice.
CREATE OR REPLACE FUNCTION public.spyglass_start_link_work_agent_execution(
    p_execution_id uuid,p_account_id uuid,p_lease_id uuid,p_entitlement_version bigint,p_maximum_concurrent_runs bigint,
    p_plan_digest bytea,p_event_id uuid,p_user_message_id uuid,p_invocation_id uuid,p_profile text,p_model_targets text[],
    p_model_operation_ids uuid[],p_tool_operation_ids uuid[],p_request_expires_at timestamptz,p_now timestamptz
) RETURNS TABLE(created_run boolean,linked_run boolean,reconciled boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record; execution_row record; work_row record; persona_row record; boardroom_row record; active_runs bigint;
DECLARE context_sequence bigint; context_envelope jsonb; context_raw bytea; context_count integer;
BEGIN
    IF p_execution_id IS NULL OR p_account_id IS NULL OR p_lease_id IS NULL OR p_entitlement_version IS NULL OR p_entitlement_version<1 OR
       p_maximum_concurrent_runs IS NULL OR p_maximum_concurrent_runs<1 OR p_plan_digest IS NULL OR octet_length(p_plan_digest)<>32 OR
       p_event_id IS NULL OR p_user_message_id IS NULL OR p_invocation_id IS NULL OR p_profile IS NULL OR
       p_profile !~ '^[a-z][a-z0-9-]{0,49}$' OR p_model_targets IS DISTINCT FROM ARRAY['pending-a','pending-b','pending-c']::text[] OR
       p_model_operation_ids IS NULL OR p_tool_operation_ids IS NULL OR cardinality(p_tool_operation_ids) NOT BETWEEN 0 AND 5 OR
       cardinality(p_model_operation_ids)<>(cardinality(p_tool_operation_ids)+1)*3 OR p_request_expires_at IS NULL OR p_now IS NULL OR
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
        (account_id,id,run_id,turn,persona_version_id,status,expected_provider,requested_model,permitted_models,queued_at)
    VALUES (p_account_id,p_invocation_id,execution_row.planned_run_id,1,execution_row.persona_version_id,'queued',
            'pending','pending-a',p_model_targets,p_now);
    INSERT INTO spyglass.agent_invocation_execution_plans
        (account_id,invocation_id,conversation_id,context_sequence,profile,model_operation_ids,tool_operation_ids,request_expires_at,created_at)
    VALUES (p_account_id,p_invocation_id,execution_row.planned_conversation_id,context_sequence,p_profile,
            p_model_operation_ids,p_tool_operation_ids,p_request_expires_at,p_now);

    SELECT jsonb_build_object('schema_version',1,'items',COALESCE(jsonb_agg(item ORDER BY work_answer DESC,updated_at,id),'[]'::jsonb)),count(*)
    INTO context_envelope,context_count
    FROM (
        SELECT f.updated_at,f.id,f.work_answer,jsonb_build_object(
            'kind','knowledge_fact','id',f.id::text,'version',f.revision,
            'digest',encode(sha256(convert_to(content::text,'UTF8')),'hex'),'content',content
        ) AS item
        FROM (
            SELECT f.id,f.revision,f.updated_at,
                EXISTS (
                    SELECT 1 FROM spyglass.attention_information_requests request
                    WHERE request.account_id=f.account_id AND request.parent_work_item_id=execution_row.work_item_id
                      AND request.fact_id=f.id AND request.state='answered'
                ) AS work_answer,
                jsonb_build_object(
                    'scope',f.scope_kind,'key',f.fact_key,'state',f.state,
                    'value',convert_from(c.canonical_value,'UTF8')::jsonb,
                    'confidence',c.confidence,'sensitivity',c.sensitivity
                ) AS content
            FROM spyglass.knowledge_facts f
            JOIN spyglass.knowledge_claims c ON c.account_id=f.account_id AND c.id=f.current_claim_id
            WHERE f.account_id=p_account_id AND f.state='active' AND c.state='accepted' AND c.sensitivity<>'restricted' AND (
                f.fact_key LIKE 'baseline.%' OR EXISTS (
                    SELECT 1 FROM spyglass.attention_information_requests request
                    WHERE request.account_id=f.account_id AND request.parent_work_item_id=execution_row.work_item_id
                      AND request.fact_id=f.id AND request.state='answered'
                )
            )
            ORDER BY work_answer DESC,f.updated_at DESC,f.id DESC LIMIT 8
        ) f
    ) frozen;
    context_raw:=convert_to(context_envelope::text,'UTF8');
    IF octet_length(context_raw)>49152 THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Work Agent execution context is too large';
    END IF;
    UPDATE spyglass.agent_runs SET context_payload=context_raw,context_digest=sha256(context_raw),context_item_count=context_count
    WHERE account_id=p_account_id AND id=execution_row.planned_run_id;

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

-- RC.37 exposed provider-neutral Work to the legacy bridge before this
-- corrected function was installed. Retry only unchanged, unlinked intents
-- rejected for that exact compatibility reason.
UPDATE spyglass.work_agent_execution_queue queue SET
    state='pending',attempt_count=0,next_attempt_at=statement_timestamp(),lease_id=NULL,lease_expires_at=NULL,
    last_error_code=NULL,updated_at=statement_timestamp()
FROM spyglass.work_agent_executions execution
JOIN spyglass.agent_persona_versions version ON version.account_id=execution.account_id AND version.id=execution.persona_version_id
JOIN spyglass.work_items work ON work.account_id=execution.account_id AND work.id=execution.work_item_id
WHERE queue.account_id=execution.account_id AND queue.execution_id=execution.execution_id
  AND queue.state='dead_letter' AND queue.last_error_code='persona_unavailable'
  AND execution.linked_run_id IS NULL AND execution.linked_conversation_id IS NULL
  AND work.version=execution.work_version AND work.responsibility='persona' AND work.assignee_persona_id=execution.persona_id
  AND work.state IN ('open','in_progress','waiting') AND work.run_id IS NULL AND work.conversation_id IS NULL
  AND version.policy->>'complexity' IN ('simple','efficient','balanced','thorough','advanced')
  AND COALESCE(version.policy->>'provider','')='' AND COALESCE(version.policy->>'model','')='';

COMMIT;
