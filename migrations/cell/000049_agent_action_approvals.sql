BEGIN;

-- Policy v2 turns explicitly permitted model action proposals into durable
-- Attention approvals. Existing invocations retain v1 and remain message-only.
ALTER TABLE spyglass.agent_invocations ALTER COLUMN result_policy_version SET DEFAULT 2;

CREATE FUNCTION public.spyglass_claim_agent_result_projection_v3(
    p_lease_id uuid,p_now timestamptz,p_lease_seconds integer
) RETURNS TABLE (
    account_id uuid,invocation_id uuid,lease_id uuid,attempt_count integer,
    expected_provider text,requested_model text,permitted_models text[],result_policy_version bigint,
    citation_policy text,action_policy text,action_capabilities text[],current_persona_id uuid,
    delegate_persona_ids uuid[],citation_bindings text[],pod_uid uuid,result_outcome text,
    result_ciphertext bytea,result_nonce bytea,result_key_version integer,result_digest bytea,result_submitted_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE claim_row record; capabilities text[];
BEGIN
    SELECT * INTO claim_row FROM public.spyglass_claim_agent_result_projection_v2(p_lease_id,p_now,p_lease_seconds);
    IF NOT FOUND THEN RETURN; END IF;
    PERFORM set_config('app.account_id',claim_row.account_id::text,true);
    SELECT ARRAY(
        SELECT jsonb_array_elements_text(COALESCE(v.policy->'action_capabilities','[]'::jsonb))
    ) INTO capabilities
    FROM spyglass.agent_invocations i
    JOIN spyglass.agent_persona_versions v ON v.account_id=i.account_id AND v.id=i.persona_version_id
    WHERE i.account_id=claim_row.account_id AND i.id=claim_row.invocation_id;
    IF capabilities IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent action policy unavailable';
    END IF;
    RETURN QUERY SELECT claim_row.account_id,claim_row.invocation_id,claim_row.lease_id,claim_row.attempt_count,
        claim_row.expected_provider,claim_row.requested_model,claim_row.permitted_models,claim_row.result_policy_version,
        claim_row.citation_policy,claim_row.action_policy,capabilities,claim_row.current_persona_id,
        claim_row.delegate_persona_ids,claim_row.citation_bindings,claim_row.pod_uid,claim_row.result_outcome,
        claim_row.result_ciphertext,claim_row.result_nonce,claim_row.result_key_version,claim_row.result_digest,claim_row.result_submitted_at;
END;
$$;

CREATE FUNCTION public.spyglass_project_agent_invocation_success_v2(
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
    IF NOT FOUND OR invocation_row.result_policy_version NOT IN (1,2) OR
       (invocation_row.result_policy_version=1 AND jsonb_array_length(p_proposals)<>0) OR
       (invocation_row.result_policy_version=2 AND jsonb_array_length(p_proposals)<>jsonb_array_length(p_result_payload->'proposed_actions')) OR
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
               WHERE i.account_id=p_account_id AND i.id=p_invocation_id)) OR v_policy_version<>2 OR v_expires_at<>p_now+interval '24 hours' THEN
            RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='agent action proposal denied';
        END IF;
        SELECT * INTO existing FROM spyglass.attention_consequential_approvals a
        WHERE a.account_id=p_account_id AND (a.id=v_approval_id OR a.operation_id=v_operation_id);
        IF FOUND THEN
            IF existing.id<>v_approval_id OR existing.operation_id<>v_operation_id OR existing.invocation_id<>p_invocation_id OR
               existing.work_item_id IS DISTINCT FROM work_id OR existing.capability<>v_capability OR existing.canonical_payload<>v_payload OR
               existing.input_sha256<>v_input_digest OR existing.evidence_sha256<>v_evidence_digest OR existing.proposer_kind<>'workload' OR
               existing.proposer_id<>v_proposer_id OR existing.policy_version<>v_policy_version OR NOT existing.require_independent_review OR
               existing.expires_at<>v_expires_at OR existing.created_at<>p_now THEN
                RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent action proposal conflicts with existing approval';
            END IF;
            SELECT * INTO existing_event FROM spyglass.attention_events e
            WHERE e.account_id=p_account_id AND (e.id=v_event_id OR e.consequential_approval_id=v_approval_id);
            IF NOT FOUND OR existing_event.id<>v_event_id OR existing_event.aggregate_kind<>'consequential_approval' OR
               existing_event.event_type<>'approval_requested' OR existing_event.from_version<>0 OR existing_event.to_version<>1 OR
               existing_event.actor_kind<>'workload' OR existing_event.actor_id<>v_proposer_id OR existing_event.correlation_id<>p_invocation_id::text OR
               existing_event.occurred_at<>p_now THEN
                RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent action proposal conflicts with existing event';
            END IF;
        ELSE
            INSERT INTO spyglass.attention_consequential_approvals
                (account_id,id,operation_id,invocation_id,work_item_id,capability,canonical_payload,input_sha256,hash_version,evidence_sha256,
                 proposer_kind,proposer_id,policy_version,require_independent_review,expires_at,state,version,created_at,updated_at)
            VALUES (p_account_id,v_approval_id,v_operation_id,p_invocation_id,work_id,v_capability,v_payload,v_input_digest,1,v_evidence_digest,
                    'workload',v_proposer_id,v_policy_version,true,v_expires_at,'open',1,p_now,p_now);
            INSERT INTO spyglass.attention_events
                (account_id,id,aggregate_kind,consequential_approval_id,event_type,from_version,to_version,actor_kind,actor_id,reason,
                 correlation_id,redacted_payload,occurred_at)
            VALUES (p_account_id,v_event_id,'consequential_approval',v_approval_id,'approval_requested',0,1,'workload',v_proposer_id,'',
                    p_invocation_id::text,jsonb_build_object('state','open','capability',v_capability,'hash_version',1,'policy_version',v_policy_version),p_now);
        END IF;
    END LOOP;
    RETURN projected;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_claim_agent_result_projection_v3(uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_project_agent_invocation_success_v2(uuid,uuid,uuid,uuid,text,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz,jsonb) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='spyglass_agent_projection_worker') THEN
        REVOKE EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection_v2(uuid,timestamptz,integer) FROM spyglass_agent_projection_worker;
        REVOKE EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_success(uuid,uuid,uuid,uuid,text,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz) FROM spyglass_agent_projection_worker;
        GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection_v3(uuid,timestamptz,integer) TO spyglass_agent_projection_worker;
        GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_success_v2(uuid,uuid,uuid,uuid,text,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz,jsonb) TO spyglass_agent_projection_worker;
    END IF;
END;
$$;

COMMIT;
