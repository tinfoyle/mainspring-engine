BEGIN;

-- A Work-linked Agent turn has exactly two successful outcomes: questions
-- pause the Work for owner input, while a complete answer closes the Work.
-- The prior function only implemented the first branch and left successful
-- no-question executions permanently in progress.
CREATE OR REPLACE FUNCTION public.spyglass_project_agent_invocation_success_v3(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_message_id uuid,p_provider text,p_selected_model text,
    p_response_model text,p_provider_response_id text,p_runner_result_digest bytea,p_result_digest bytea,p_result_payload jsonb,p_body text,
    p_input_tokens bigint,p_output_tokens bigint,p_total_tokens bigint,p_cost_micros bigint,p_completed_at timestamptz,p_now timestamptz,
    p_proposals jsonb,p_information_requests jsonb,p_work_event_id uuid
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE projected boolean; invocation_row record; work_row record; request jsonb; question text; ordinal integer:=0;
DECLARE v_request_id uuid; v_event_id uuid; v_fact_key text; v_question text; v_requester_id text;
DECLARE v_expected_fact_key text; existing record; existing_event record; question_count integer;
BEGIN
    IF jsonb_typeof(p_information_requests)<>'array' OR jsonb_typeof(p_result_payload->'questions')<>'array' OR
       jsonb_array_length(p_information_requests)>8 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent owner questions';
    END IF;
    question_count:=jsonb_array_length(p_information_requests);
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT i.result_policy_version,i.run_id,t.persona_id,e.work_item_id
    INTO invocation_row
    FROM spyglass.agent_invocations i
    JOIN spyglass.agent_run_plan_turns t ON t.account_id=i.account_id AND t.run_id=i.run_id AND t.turn=i.turn
    LEFT JOIN spyglass.work_agent_executions e ON e.account_id=i.account_id AND e.planned_run_id=i.run_id
    WHERE i.account_id=p_account_id AND i.id=p_invocation_id;
    IF NOT FOUND OR invocation_row.result_policy_version NOT IN (1,2,3) OR
       (invocation_row.result_policy_version<3 AND question_count<>0) OR
       (invocation_row.work_item_id IS NULL AND question_count<>0) OR
       (invocation_row.work_item_id IS NOT NULL AND invocation_row.result_policy_version=3 AND
        question_count<>jsonb_array_length(p_result_payload->'questions')) OR
       (invocation_row.work_item_id IS NULL AND p_work_event_id IS NOT NULL) OR
       (invocation_row.work_item_id IS NOT NULL AND invocation_row.result_policy_version=3 AND p_work_event_id IS NULL) THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent owner questions';
    END IF;

    SELECT public.spyglass_project_agent_invocation_success_v2(
        p_account_id,p_invocation_id,p_lease_id,p_message_id,p_provider,p_selected_model,p_response_model,p_provider_response_id,
        p_runner_result_digest,p_result_digest,p_result_payload,p_body,p_input_tokens,p_output_tokens,p_total_tokens,p_cost_micros,
        p_completed_at,p_now,p_proposals
    ) INTO projected;

    FOR request IN SELECT value FROM jsonb_array_elements(p_information_requests) LOOP
        ordinal:=ordinal+1;
        question:=(p_result_payload->'questions')->>(ordinal-1);
        BEGIN
            v_request_id:=(request->>'request_id')::uuid;
            v_event_id:=(request->>'event_id')::uuid;
            v_fact_key:=request->>'fact_key';
            v_question:=request->>'question';
            v_requester_id:=request->>'requester_id';
        EXCEPTION WHEN OTHERS THEN
            RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent owner question encoding';
        END;
        v_expected_fact_key:='agent.owner_question.'||substr(encode(sha256(convert_to(v_question,'UTF8')),'hex'),1,32);
        IF v_fact_key IS DISTINCT FROM v_expected_fact_key OR v_question IS DISTINCT FROM question OR
           char_length(v_question) NOT BETWEEN 3 AND 4000 OR v_requester_id<>('agent:'||invocation_row.persona_id::text) THEN
            RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='agent owner question denied';
        END IF;
        SELECT * INTO existing FROM spyglass.attention_information_requests r
        WHERE r.account_id=p_account_id AND r.id=v_request_id;
        IF FOUND THEN
            IF existing.parent_work_item_id<>invocation_row.work_item_id OR existing.fact_key<>v_fact_key OR existing.scope_kind<>'account' OR
               existing.question<>v_question OR existing.requested_by_kind<>'workload' OR existing.requested_by_id<>v_requester_id OR
               existing.created_at<>p_completed_at THEN
                RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent owner question conflicts with existing request';
            END IF;
            SELECT * INTO existing_event FROM spyglass.attention_events e
            WHERE e.account_id=p_account_id AND e.id=v_event_id;
            IF NOT FOUND OR existing_event.information_request_id<>v_request_id OR existing_event.event_type<>'information_requested' OR
               existing_event.actor_kind<>'workload' OR existing_event.actor_id<>v_requester_id OR
               existing_event.correlation_id<>p_invocation_id::text OR existing_event.occurred_at<>p_completed_at THEN
                RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent owner question conflicts with existing event';
            END IF;
        ELSIF projected THEN
            INSERT INTO spyglass.attention_information_requests
                (account_id,id,parent_work_item_id,fact_key,scope_kind,question,requested_by_kind,requested_by_id,state,reason,version,created_at,updated_at)
            VALUES (p_account_id,v_request_id,invocation_row.work_item_id,v_fact_key,'account',v_question,'workload',v_requester_id,'open','',1,p_completed_at,p_completed_at);
            INSERT INTO spyglass.attention_events
                (account_id,id,aggregate_kind,information_request_id,event_type,from_version,to_version,actor_kind,actor_id,reason,
                 correlation_id,redacted_payload,occurred_at)
            VALUES (p_account_id,v_event_id,'information_request',v_request_id,'information_requested',0,1,'workload',v_requester_id,'',
                    p_invocation_id::text,jsonb_build_object('state','open','scope_kind','account'),p_completed_at);
        ELSE
            RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent owner question request missing on replay';
        END IF;
    END LOOP;

    IF projected AND invocation_row.work_item_id IS NOT NULL AND invocation_row.result_policy_version=3 THEN
        SELECT w.* INTO work_row FROM spyglass.work_items w
        WHERE w.account_id=p_account_id AND w.id=invocation_row.work_item_id FOR UPDATE;
        IF NOT FOUND OR work_row.state<>'in_progress' OR work_row.run_id<>invocation_row.run_id THEN
            RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent Work outcome target unavailable';
        END IF;
        UPDATE spyglass.work_items SET
            state=CASE WHEN question_count>0 THEN 'waiting' ELSE 'done' END,
            completed_at=CASE WHEN question_count=0 THEN p_now ELSE NULL END,
            version=version+1,updated_at=p_now
        WHERE account_id=p_account_id AND id=invocation_row.work_item_id;
        INSERT INTO spyglass.work_item_events
            (account_id,id,work_item_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
        VALUES (p_account_id,p_work_event_id,invocation_row.work_item_id,'transitioned',work_row.version,work_row.version+1,'workload',
                'agent:'||invocation_row.persona_id::text,
                CASE WHEN question_count>0 THEN 'Agent requested owner input' ELSE 'Agent completed assigned Work' END,
                p_invocation_id::text,
                jsonb_build_object('state',CASE WHEN question_count>0 THEN 'waiting' ELSE 'done' END,
                    'priority',work_row.priority,'responsibility',work_row.responsibility),p_now);
        IF question_count>0 THEN
            UPDATE spyglass.agent_runs SET state='waiting_input',completed_at=NULL
            WHERE account_id=p_account_id AND id=invocation_row.run_id AND state='succeeded';
            IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent owner question Run target unavailable'; END IF;
        END IF;
    END IF;
    RETURN projected;
END;
$$;

-- Work execution receives the latest owner-confirmed Baseline facts as a
-- frozen Run snapshot. This lets the agent use what onboarding already
-- learned before it asks the owner to repeat anything.
CREATE OR REPLACE FUNCTION public.spyglass_start_link_work_agent_execution(
    p_execution_id uuid,p_account_id uuid,p_lease_id uuid,p_entitlement_version bigint,p_maximum_concurrent_runs bigint,
    p_plan_digest bytea,p_event_id uuid,p_user_message_id uuid,p_invocation_id uuid,p_profile text,p_model_targets text[],
    p_model_operation_ids uuid[],p_tool_operation_ids uuid[],p_request_expires_at timestamptz,p_now timestamptz
) RETURNS TABLE(created_run boolean,linked_run boolean,reconciled boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE expected_targets text[]; primary_operation_ids uuid[]:=ARRAY[]::uuid[]; target_count integer; step integer; result_row record;
DECLARE context_envelope jsonb; context_raw bytea; context_count integer;
BEGIN
    IF NOT spyglass.valid_agent_model_targets(p_model_targets) OR p_model_operation_ids IS NULL OR p_tool_operation_ids IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Work Agent model targets';
    END IF;
    target_count:=cardinality(p_model_targets);
    IF cardinality(p_model_operation_ids)<>(cardinality(p_tool_operation_ids)+1)*target_count THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Work Agent model operation shape';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT ARRAY[v.policy->>'model'] || ARRAY(
        SELECT jsonb_array_elements_text(COALESCE(v.policy->'fallback_models','[]'::jsonb))
    ) INTO expected_targets
    FROM spyglass.work_agent_executions e
    JOIN spyglass.agent_persona_versions v ON v.account_id=e.account_id AND v.id=e.persona_version_id
    WHERE e.account_id=p_account_id AND e.execution_id=p_execution_id;
    IF expected_targets IS NULL OR expected_targets IS DISTINCT FROM p_model_targets THEN
        RAISE EXCEPTION USING ERRCODE='P0004', MESSAGE='Work Agent execution Persona unavailable';
    END IF;
    FOR step IN 0..cardinality(p_tool_operation_ids) LOOP
        primary_operation_ids:=array_append(primary_operation_ids,p_model_operation_ids[step*target_count+1]);
    END LOOP;
    SELECT * INTO result_row FROM public.spyglass_start_link_work_agent_execution(
        p_execution_id,p_account_id,p_lease_id,p_entitlement_version,p_maximum_concurrent_runs,p_plan_digest,p_event_id,
        p_user_message_id,p_invocation_id,p_profile,primary_operation_ids,p_tool_operation_ids,p_request_expires_at,p_now
    );
    IF result_row.created_run THEN
        UPDATE spyglass.agent_invocations SET permitted_models=p_model_targets
        WHERE account_id=p_account_id AND id=p_invocation_id AND requested_model=p_model_targets[1];
        IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0004', MESSAGE='Work Agent execution Persona unavailable'; END IF;
        UPDATE spyglass.agent_invocation_execution_plans SET model_operation_ids=p_model_operation_ids
        WHERE account_id=p_account_id AND invocation_id=p_invocation_id;
        IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Work Agent execution plan unavailable'; END IF;

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
                        JOIN spyglass.work_agent_executions execution ON execution.account_id=request.account_id
                          AND execution.execution_id=p_execution_id AND execution.work_item_id=request.parent_work_item_id
                        WHERE request.account_id=f.account_id AND request.fact_id=f.id AND request.state='answered'
                    ) AS work_answer,
                    jsonb_build_object(
                    'scope',f.scope_kind,'key',f.fact_key,'state',f.state,
                    'value',convert_from(c.canonical_value,'UTF8')::jsonb,
                    'confidence',c.confidence,'sensitivity',c.sensitivity
                ) AS content
                FROM spyglass.knowledge_facts f
                JOIN spyglass.knowledge_claims c ON c.account_id=f.account_id AND c.id=f.current_claim_id
                WHERE f.account_id=p_account_id AND f.state='active' AND c.state='accepted' AND
                      c.sensitivity<>'restricted' AND (
                          f.fact_key LIKE 'baseline.%' OR EXISTS (
                              SELECT 1 FROM spyglass.attention_information_requests request
                              JOIN spyglass.work_agent_executions execution ON execution.account_id=request.account_id
                                AND execution.execution_id=p_execution_id AND execution.work_item_id=request.parent_work_item_id
                              WHERE request.account_id=f.account_id AND request.fact_id=f.id AND request.state='answered'
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
        WHERE account_id=p_account_id AND id=(SELECT planned_run_id FROM spyglass.work_agent_executions
            WHERE account_id=p_account_id AND execution_id=p_execution_id);
        IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Work Agent execution Run unavailable'; END IF;
    END IF;
    RETURN QUERY SELECT result_row.created_run,result_row.linked_run,result_row.reconciled;
END;
$$;

-- RC.36 created Baseline-approved Work by linking the onboarding conversation
-- before assigning the Persona. Repair only that exact shape, preserve the
-- immutable approving Agent message as the Work ID, and enqueue one execution.
CREATE TEMP TABLE baseline_work_dispatch_repairs ON COMMIT DROP AS
SELECT w.account_id,w.id AS work_item_id,w.version AS prior_version,w.version+1 AS work_version,
       w.created_by_actor_id::uuid AS initiating_user_id,w.assignee_persona_id AS persona_id,
       v.id AS persona_version_id,p.boardroom_id,b.version AS boardroom_version,w.title,w.description,
       (substr(md5(w.id::text||'/baseline-repair/execution'),1,8)||'-'||substr(md5(w.id::text||'/baseline-repair/execution'),9,4)||'-'||
        substr(md5(w.id::text||'/baseline-repair/execution'),13,4)||'-'||substr(md5(w.id::text||'/baseline-repair/execution'),17,4)||'-'||
        substr(md5(w.id::text||'/baseline-repair/execution'),21,12))::uuid AS execution_id,
       (substr(md5(w.id::text||'/baseline-repair/run'),1,8)||'-'||substr(md5(w.id::text||'/baseline-repair/run'),9,4)||'-'||
        substr(md5(w.id::text||'/baseline-repair/run'),13,4)||'-'||substr(md5(w.id::text||'/baseline-repair/run'),17,4)||'-'||
        substr(md5(w.id::text||'/baseline-repair/run'),21,12))::uuid AS planned_run_id,
       (substr(md5(w.id::text||'/baseline-repair/conversation'),1,8)||'-'||substr(md5(w.id::text||'/baseline-repair/conversation'),9,4)||'-'||
        substr(md5(w.id::text||'/baseline-repair/conversation'),13,4)||'-'||substr(md5(w.id::text||'/baseline-repair/conversation'),17,4)||'-'||
        substr(md5(w.id::text||'/baseline-repair/conversation'),21,12))::uuid AS planned_conversation_id,
       (substr(md5(w.id::text||'/baseline-repair/event'),1,8)||'-'||substr(md5(w.id::text||'/baseline-repair/event'),9,4)||'-'||
        substr(md5(w.id::text||'/baseline-repair/event'),13,4)||'-'||substr(md5(w.id::text||'/baseline-repair/event'),17,4)||'-'||
        substr(md5(w.id::text||'/baseline-repair/event'),21,12))::uuid AS event_id
FROM spyglass.work_items w
JOIN spyglass.agent_messages m ON m.account_id=w.account_id AND m.id=w.id AND m.conversation_id=w.conversation_id
JOIN spyglass.agent_personas p ON p.account_id=w.account_id AND p.id=w.assignee_persona_id AND p.state='active' AND p.latest_version>0
JOIN spyglass.agent_persona_versions v ON v.account_id=p.account_id AND v.persona_id=p.id AND v.version=p.latest_version
JOIN spyglass.agent_boardrooms b ON b.account_id=p.account_id AND b.id=p.boardroom_id AND b.state='active'
WHERE w.responsibility='persona' AND w.state='open' AND w.run_id IS NULL AND w.conversation_id IS NOT NULL
  AND w.created_by_actor_kind='user' AND w.created_by_actor_id ~ '^[0-9a-fA-F-]{36}$'
  AND w.description LIKE '%This is approved setup Work from the Business Baseline. Work on this task rather than continuing the onboarding interview.%'
  AND NOT EXISTS (SELECT 1 FROM spyglass.work_agent_executions e WHERE e.account_id=w.account_id AND e.work_item_id=w.id);

UPDATE spyglass.work_items w SET conversation_id=NULL,version=r.work_version,updated_at=statement_timestamp()
FROM baseline_work_dispatch_repairs r WHERE w.account_id=r.account_id AND w.id=r.work_item_id AND w.version=r.prior_version;

INSERT INTO spyglass.work_item_events
    (account_id,id,work_item_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
SELECT account_id,event_id,work_item_id,'assigned',prior_version,work_version,'workload','baseline-work-dispatch-repair',
       'Repaired Baseline-approved Agent dispatch',execution_id::text,
       jsonb_build_object('state','open','responsibility','persona'),statement_timestamp()
FROM baseline_work_dispatch_repairs;

INSERT INTO spyglass.work_agent_executions
    (account_id,execution_id,work_item_id,work_version,initiating_user_id,persona_id,persona_version_id,boardroom_id,boardroom_version,
     planned_run_id,planned_conversation_id,title,description,queued_at,updated_at)
SELECT account_id,execution_id,work_item_id,work_version,initiating_user_id,persona_id,persona_version_id,boardroom_id,boardroom_version,
       planned_run_id,planned_conversation_id,title,description,statement_timestamp(),statement_timestamp()
FROM baseline_work_dispatch_repairs;

INSERT INTO spyglass.work_agent_execution_queue
    (account_id,execution_id,work_item_id,state,next_attempt_at,queued_at,updated_at)
SELECT account_id,execution_id,work_item_id,'pending',statement_timestamp(),statement_timestamp(),statement_timestamp()
FROM baseline_work_dispatch_repairs;

COMMIT;
