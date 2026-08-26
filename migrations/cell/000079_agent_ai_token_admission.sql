BEGIN;

ALTER TABLE spyglass.agent_invocations
    ADD COLUMN ai_token_reservation_id uuid,
    ADD COLUMN ai_token_rate_snapshot jsonb,
    ADD COLUMN reasoning_effort text,
    ADD COLUMN execution_adapter_version bigint,
    ADD COLUMN execution_model_policy_version bigint,
    ADD CONSTRAINT agent_invocations_ai_token_admission_complete CHECK (
        (ai_token_reservation_id IS NULL AND ai_token_rate_snapshot IS NULL AND reasoning_effort IS NULL AND execution_adapter_version IS NULL AND execution_model_policy_version IS NULL) OR
        (ai_token_reservation_id IS NOT NULL AND jsonb_typeof(ai_token_rate_snapshot)='object' AND octet_length(ai_token_rate_snapshot::text)<=8192 AND
         (reasoning_effort IS NULL OR reasoning_effort ~ '^[a-z][a-z0-9._:-]{0,127}$') AND execution_adapter_version>0 AND execution_model_policy_version>0)
    );

CREATE FUNCTION public.spyglass_admit_agent_invocation_tokens(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_reservation_id uuid,p_rate_snapshot jsonb,
    p_provider text,p_model_targets text[],p_reasoning_effort text,p_adapter_version bigint,p_model_policy_version bigint,
    p_model_operation_ids uuid[],p_now timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record; invocation_row record; expected_fallbacks jsonb;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_lease_id IS NULL OR p_reservation_id IS NULL OR
       p_rate_snapshot IS NULL OR jsonb_typeof(p_rate_snapshot)<>'object' OR octet_length(p_rate_snapshot::text)>8192 OR
       p_provider IS NULL OR p_provider !~ '^[a-z][a-z0-9._:-]{0,127}$' OR NOT spyglass.valid_agent_model_targets(p_model_targets) OR
       (p_reasoning_effort IS NOT NULL AND p_reasoning_effort !~ '^[a-z][a-z0-9._:-]{0,127}$') OR
       p_adapter_version IS NULL OR p_adapter_version<1 OR p_model_policy_version IS NULL OR p_model_policy_version<1 OR
       p_model_operation_ids IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Agent AI Token admission';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.agent_dispatch_queue q
    WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id FOR UPDATE;
    IF queue_row.invocation_id IS NULL OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent dispatch lease lost';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT i.* INTO invocation_row FROM spyglass.agent_invocations i
    WHERE i.account_id=p_account_id AND i.id=p_invocation_id FOR UPDATE;
    IF NOT FOUND OR invocation_row.status<>'queued' THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Agent invocation cannot be admitted';
    END IF;
    IF cardinality(p_model_operation_ids)<>(SELECT (cardinality(e.tool_operation_ids)+1)*cardinality(p_model_targets)
        FROM spyglass.agent_invocation_execution_plans e WHERE e.account_id=p_account_id AND e.invocation_id=p_invocation_id) THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='Agent admitted model operation shape is invalid';
    END IF;
    expected_fallbacks:=to_jsonb(COALESCE(p_model_targets[2:cardinality(p_model_targets)],ARRAY[]::text[]));
    IF p_rate_snapshot->>'internal_provider' IS DISTINCT FROM p_provider OR p_rate_snapshot->>'internal_model' IS DISTINCT FROM p_model_targets[1] OR
       COALESCE(p_rate_snapshot->'internal_fallback_models','[]'::jsonb) IS DISTINCT FROM expected_fallbacks OR
       COALESCE(p_rate_snapshot->>'internal_reasoning_effort','') IS DISTINCT FROM COALESCE(p_reasoning_effort,'') OR
       (p_rate_snapshot->>'internal_adapter_version')::bigint IS DISTINCT FROM p_adapter_version OR
       (p_rate_snapshot->>'internal_model_policy_version')::bigint IS DISTINCT FROM p_model_policy_version THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='Agent AI Token rate does not bind the execution target';
    END IF;
    IF invocation_row.ai_token_reservation_id IS NOT NULL THEN
        IF invocation_row.ai_token_reservation_id=p_reservation_id AND invocation_row.ai_token_rate_snapshot=p_rate_snapshot AND
           invocation_row.expected_provider=p_provider AND invocation_row.permitted_models=p_model_targets AND
           COALESCE(invocation_row.reasoning_effort,'')=COALESCE(p_reasoning_effort,'') AND
           (SELECT e.model_operation_ids=p_model_operation_ids FROM spyglass.agent_invocation_execution_plans e
            WHERE e.account_id=p_account_id AND e.invocation_id=p_invocation_id) THEN
            RETURN false;
        END IF;
        RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='Agent AI Token admission conflicts with its frozen target';
    END IF;
    UPDATE spyglass.agent_invocations SET ai_token_reservation_id=p_reservation_id,ai_token_rate_snapshot=p_rate_snapshot,
        expected_provider=p_provider,requested_model=p_model_targets[1],permitted_models=p_model_targets,reasoning_effort=NULLIF(p_reasoning_effort,''),
        execution_adapter_version=p_adapter_version,execution_model_policy_version=p_model_policy_version
    WHERE account_id=p_account_id AND id=p_invocation_id;
    UPDATE spyglass.agent_invocation_execution_plans SET model_operation_ids=p_model_operation_ids
    WHERE account_id=p_account_id AND invocation_id=p_invocation_id;
    RETURN true;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_admit_agent_invocation_tokens(uuid,uuid,uuid,uuid,jsonb,text,text[],text,bigint,bigint,uuid[],timestamptz) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='spyglass_agent_dispatch_worker') THEN
        GRANT EXECUTE ON FUNCTION public.spyglass_admit_agent_invocation_tokens(uuid,uuid,uuid,uuid,jsonb,text,text[],text,bigint,bigint,uuid[],timestamptz) TO spyglass_agent_dispatch_worker;
    END IF;
END;
$$;

COMMIT;
