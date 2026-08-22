BEGIN;

-- Policy v3 gives Work-originated Agent questions a durable pause/resume
-- meaning. Older invocations preserve their original publication behavior.
ALTER TABLE spyglass.agent_invocations ALTER COLUMN result_policy_version SET DEFAULT 3;

DO $$
DECLARE item record;
BEGIN
    FOR item IN SELECT conname FROM pg_constraint
        WHERE conrelid='spyglass.agent_runs'::regclass AND contype='c'
          AND pg_get_constraintdef(oid) LIKE '%state%' AND pg_get_constraintdef(oid) LIKE '%planned%'
    LOOP
        EXECUTE format('ALTER TABLE spyglass.agent_runs DROP CONSTRAINT %I',item.conname);
    END LOOP;
END;
$$;
ALTER TABLE spyglass.agent_runs
    ADD CONSTRAINT agent_runs_state_v3_check CHECK (state IN ('planned','running','waiting_input','succeeded','partially_failed','failed','canceled')),
    ADD CONSTRAINT agent_runs_timestamps_v3_check CHECK (
        (state='planned' AND started_at IS NULL AND completed_at IS NULL) OR
        (state IN ('running','waiting_input') AND started_at IS NOT NULL AND completed_at IS NULL) OR
        (state IN ('succeeded','partially_failed','failed','canceled') AND completed_at IS NOT NULL));

CREATE FUNCTION public.spyglass_claim_agent_result_projection_v4(
    p_lease_id uuid,p_now timestamptz,p_lease_seconds integer
) RETURNS TABLE (
    account_id uuid,invocation_id uuid,lease_id uuid,attempt_count integer,
    expected_provider text,requested_model text,permitted_models text[],result_policy_version bigint,
    citation_policy text,action_policy text,action_capabilities text[],current_persona_id uuid,work_item_id text,
    delegate_persona_ids uuid[],citation_bindings text[],pod_uid uuid,result_outcome text,
    result_ciphertext bytea,result_nonce bytea,result_key_version integer,result_digest bytea,result_submitted_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE claim_row record; linked_work text;
BEGIN
    SELECT * INTO claim_row FROM public.spyglass_claim_agent_result_projection_v3(p_lease_id,p_now,p_lease_seconds);
    IF NOT FOUND THEN RETURN; END IF;
    PERFORM set_config('app.account_id',claim_row.account_id::text,true);
    SELECT COALESCE((SELECT e.work_item_id::text FROM spyglass.work_agent_executions e
        JOIN spyglass.agent_invocations i ON i.account_id=e.account_id AND i.run_id=e.planned_run_id
        WHERE e.account_id=claim_row.account_id AND i.id=claim_row.invocation_id),'') INTO linked_work;
    RETURN QUERY SELECT claim_row.account_id,claim_row.invocation_id,claim_row.lease_id,claim_row.attempt_count,
        claim_row.expected_provider,claim_row.requested_model,claim_row.permitted_models,claim_row.result_policy_version,
        claim_row.citation_policy,claim_row.action_policy,claim_row.action_capabilities,claim_row.current_persona_id,linked_work,
        claim_row.delegate_persona_ids,claim_row.citation_bindings,claim_row.pod_uid,claim_row.result_outcome,
        claim_row.result_ciphertext,claim_row.result_nonce,claim_row.result_key_version,claim_row.result_digest,claim_row.result_submitted_at;
END;
$$;

-- Upgrade the v2 governed-action transaction to admit policy v3 without
-- changing policy-v2 replay. The approval policy version must always equal
-- the invocation's frozen result-policy version.
CREATE OR REPLACE FUNCTION public.spyglass_project_agent_invocation_success_v2(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_message_id uuid,p_provider text,p_selected_model text,
    p_response_model text,p_provider_response_id text,p_runner_result_digest bytea,p_result_digest bytea,p_result_payload jsonb,p_body text,
    p_input_tokens bigint,p_output_tokens bigint,p_total_tokens bigint,p_cost_micros bigint,p_completed_at timestamptz,p_now timestamptz,
    p_proposals jsonb
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE projected boolean; invocation_row record; proposal jsonb; action jsonb; ordinal integer:=0;
DECLARE v_approval_id uuid; v_operation_id uuid; v_event_id uuid; v_capability text; v_payload bytea; v_input_digest bytea;
DECLARE v_evidence_digest bytea; v_proposer_id text; v_policy_version bigint; v_expires_at timestamptz; work_id uuid; existing record;
DECLARE frozen_capabilities text[]; existing_event record;
BEGIN
    IF jsonb_typeof(p_proposals)<>'array' OR jsonb_typeof(p_result_payload->'proposed_actions')<>'array' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent action proposals';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT i.result_policy_version,i.run_id,ARRAY(
        SELECT jsonb_array_elements_text(COALESCE(v.policy->'action_capabilities','[]'::jsonb))
    ) AS action_capabilities
    INTO invocation_row
    FROM spyglass.agent_invocations i
    JOIN spyglass.agent_persona_versions v ON v.account_id=i.account_id AND v.id=i.persona_version_id
    WHERE i.account_id=p_account_id AND i.id=p_invocation_id;
    IF NOT FOUND OR invocation_row.result_policy_version NOT IN (1,2,3) OR
       (invocation_row.result_policy_version=1 AND jsonb_array_length(p_proposals)<>0) OR
       (invocation_row.result_policy_version>=2 AND jsonb_array_length(p_proposals)<>jsonb_array_length(p_result_payload->'proposed_actions')) OR
       jsonb_array_length(p_proposals)>8 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent action proposals';
    END IF;
    frozen_capabilities:=invocation_row.action_capabilities;
    SELECT e.work_item_id INTO work_id FROM spyglass.work_agent_executions e
    WHERE e.account_id=p_account_id AND e.planned_run_id=invocation_row.run_id;

    SELECT public.spyglass_project_agent_invocation_success(
        p_account_id,p_invocation_id,p_lease_id,p_message_id,p_provider,p_selected_model,p_response_model,p_provider_response_id,
        p_runner_result_digest,p_result_digest,p_result_payload,p_body,p_input_tokens,p_output_tokens,p_total_tokens,p_cost_micros,p_completed_at,p_now
    ) INTO projected;

    FOR proposal IN SELECT value FROM jsonb_array_elements(p_proposals) LOOP
        ordinal:=ordinal+1;
        action:=(p_result_payload->'proposed_actions')->(ordinal-1);
        BEGIN
            v_approval_id:=(proposal->>'approval_id')::uuid;
            v_operation_id:=(proposal->>'operation_id')::uuid;
            v_event_id:=(proposal->>'event_id')::uuid;
            v_capability:=proposal->>'capability';
            v_payload:=decode(proposal->>'payload_base64','base64');
            v_input_digest:=decode(proposal->>'input_sha256_base64','base64');
            v_evidence_digest:=decode(proposal->>'evidence_sha256_base64','base64');
            v_proposer_id:=proposal->>'proposer_id';
            v_policy_version:=(proposal->>'policy_version')::bigint;
            v_expires_at:=(proposal->>'expires_at')::timestamptz;
        EXCEPTION WHEN OTHERS THEN
            RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent action proposal encoding';
        END;
        IF v_capability IS NULL OR v_capability<>action->>'kind' OR NOT v_capability=ANY(frozen_capabilities) OR
           convert_from(v_payload,'UTF8')::jsonb IS DISTINCT FROM action->'payload' OR octet_length(v_payload) NOT BETWEEN 2 AND 65536 OR
           octet_length(v_input_digest)<>32 OR octet_length(v_evidence_digest)<>32 OR v_proposer_id IS NULL OR
           v_proposer_id<>('agent:'||(SELECT t.persona_id::text FROM spyglass.agent_invocations i
               JOIN spyglass.agent_run_plan_turns t ON t.account_id=i.account_id AND t.run_id=i.run_id AND t.turn=i.turn
               WHERE i.account_id=p_account_id AND i.id=p_invocation_id)) OR v_policy_version<>invocation_row.result_policy_version OR
           v_expires_at<>p_completed_at+interval '24 hours' THEN
            RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='agent action proposal denied';
        END IF;
        SELECT * INTO existing FROM spyglass.attention_consequential_approvals a
        WHERE a.account_id=p_account_id AND (a.id=v_approval_id OR a.operation_id=v_operation_id);
        IF FOUND THEN
            IF existing.id<>v_approval_id OR existing.operation_id<>v_operation_id OR existing.invocation_id<>p_invocation_id OR
               existing.work_item_id IS DISTINCT FROM work_id OR existing.capability<>v_capability OR existing.canonical_payload<>v_payload OR
               existing.input_sha256<>v_input_digest OR existing.evidence_sha256<>v_evidence_digest OR existing.proposer_kind<>'workload' OR
               existing.proposer_id<>v_proposer_id OR existing.policy_version<>v_policy_version OR NOT existing.require_independent_review OR
               existing.expires_at<>v_expires_at OR existing.created_at<>p_completed_at THEN
                RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent action proposal conflicts with existing approval';
            END IF;
            SELECT * INTO existing_event FROM spyglass.attention_events e
            WHERE e.account_id=p_account_id AND (e.id=v_event_id OR e.consequential_approval_id=v_approval_id);
            IF NOT FOUND OR existing_event.id<>v_event_id OR existing_event.aggregate_kind<>'consequential_approval' OR
               existing_event.event_type<>'approval_requested' OR existing_event.from_version<>0 OR existing_event.to_version<>1 OR
               existing_event.actor_kind<>'workload' OR existing_event.actor_id<>v_proposer_id OR existing_event.correlation_id<>p_invocation_id::text OR
               existing_event.occurred_at<>p_completed_at THEN
                RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent action proposal conflicts with existing event';
            END IF;
        ELSE
            INSERT INTO spyglass.attention_consequential_approvals
                (account_id,id,operation_id,invocation_id,work_item_id,capability,canonical_payload,input_sha256,hash_version,evidence_sha256,
                 proposer_kind,proposer_id,policy_version,require_independent_review,expires_at,state,version,created_at,updated_at)
            VALUES (p_account_id,v_approval_id,v_operation_id,p_invocation_id,work_id,v_capability,v_payload,v_input_digest,1,v_evidence_digest,
                    'workload',v_proposer_id,v_policy_version,true,v_expires_at,'open',1,p_completed_at,p_completed_at);
            INSERT INTO spyglass.attention_events
                (account_id,id,aggregate_kind,consequential_approval_id,event_type,from_version,to_version,actor_kind,actor_id,reason,
                 correlation_id,redacted_payload,occurred_at)
            VALUES (p_account_id,v_event_id,'consequential_approval',v_approval_id,'approval_requested',0,1,'workload',v_proposer_id,'',
                    p_invocation_id::text,jsonb_build_object('state','open','capability',v_capability,'hash_version',1,'policy_version',v_policy_version),p_completed_at);
        END IF;
    END LOOP;
    RETURN projected;
END;
$$;

CREATE FUNCTION public.spyglass_project_agent_invocation_success_v3(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_message_id uuid,p_provider text,p_selected_model text,
    p_response_model text,p_provider_response_id text,p_runner_result_digest bytea,p_result_digest bytea,p_result_payload jsonb,p_body text,
    p_input_tokens bigint,p_output_tokens bigint,p_total_tokens bigint,p_cost_micros bigint,p_completed_at timestamptz,p_now timestamptz,
    p_proposals jsonb,p_information_requests jsonb,p_work_event_id uuid
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE projected boolean; invocation_row record; work_row record; request jsonb; question text; ordinal integer:=0;
DECLARE v_request_id uuid; v_event_id uuid; v_fact_key text; v_question text; v_requester_id text;
DECLARE v_expected_fact_key text; existing record; existing_event record;
BEGIN
    IF jsonb_typeof(p_information_requests)<>'array' OR jsonb_typeof(p_result_payload->'questions')<>'array' OR
       jsonb_array_length(p_information_requests)>8 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent owner questions';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT i.result_policy_version,i.run_id,t.persona_id,e.work_item_id
    INTO invocation_row
    FROM spyglass.agent_invocations i
    JOIN spyglass.agent_run_plan_turns t ON t.account_id=i.account_id AND t.run_id=i.run_id AND t.turn=i.turn
    LEFT JOIN spyglass.work_agent_executions e ON e.account_id=i.account_id AND e.planned_run_id=i.run_id
    WHERE i.account_id=p_account_id AND i.id=p_invocation_id;
    IF NOT FOUND OR invocation_row.result_policy_version NOT IN (1,2,3) OR
       (invocation_row.result_policy_version<3 AND jsonb_array_length(p_information_requests)<>0) OR
       (invocation_row.work_item_id IS NULL AND jsonb_array_length(p_information_requests)<>0) OR
       (invocation_row.work_item_id IS NOT NULL AND invocation_row.result_policy_version=3 AND
        jsonb_array_length(p_information_requests)<>jsonb_array_length(p_result_payload->'questions')) OR
       (jsonb_array_length(p_information_requests)=0 AND p_work_event_id IS NOT NULL) OR
       (jsonb_array_length(p_information_requests)>0 AND p_work_event_id IS NULL) THEN
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
               existing_event.actor_kind<>'workload' OR existing_event.actor_id<>v_requester_id OR existing_event.correlation_id<>p_invocation_id::text OR
               existing_event.occurred_at<>p_completed_at THEN
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

    IF projected AND jsonb_array_length(p_information_requests)>0 THEN
        SELECT w.* INTO work_row FROM spyglass.work_items w
        WHERE w.account_id=p_account_id AND w.id=invocation_row.work_item_id FOR UPDATE;
        IF NOT FOUND OR work_row.state<>'in_progress' OR work_row.run_id<>invocation_row.run_id THEN
            RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent owner question Work target unavailable';
        END IF;
        UPDATE spyglass.work_items SET state='waiting',version=version+1,updated_at=p_now
        WHERE account_id=p_account_id AND id=invocation_row.work_item_id;
        INSERT INTO spyglass.work_item_events
            (account_id,id,work_item_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
        VALUES (p_account_id,p_work_event_id,invocation_row.work_item_id,'transitioned',work_row.version,work_row.version+1,'workload',
                'agent:'||invocation_row.persona_id::text,'Agent requested owner input',p_invocation_id::text,
                jsonb_build_object('state','waiting','priority',work_row.priority,'responsibility',work_row.responsibility),p_now);
        UPDATE spyglass.agent_runs SET state='waiting_input',completed_at=NULL
        WHERE account_id=p_account_id AND id=invocation_row.run_id AND state='succeeded';
        IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent owner question Run target unavailable'; END IF;
    END IF;
    RETURN projected;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_claim_agent_result_projection_v4(uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_project_agent_invocation_success_v3(uuid,uuid,uuid,uuid,text,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz,jsonb,jsonb,uuid) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='spyglass_agent_projection_worker') THEN
        REVOKE EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection_v3(uuid,timestamptz,integer) FROM spyglass_agent_projection_worker;
        REVOKE EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_success_v2(uuid,uuid,uuid,uuid,text,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz,jsonb) FROM spyglass_agent_projection_worker;
        GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection_v4(uuid,timestamptz,integer) TO spyglass_agent_projection_worker;
        GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_success_v3(uuid,uuid,uuid,uuid,text,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz,jsonb,jsonb,uuid) TO spyglass_agent_projection_worker;
    END IF;
END;
$$;

COMMIT;
