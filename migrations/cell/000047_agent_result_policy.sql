BEGIN;

-- Result policy is frozen independently from code deployment so a queued or
-- replayed invocation is never reinterpreted by a later policy implementation.
ALTER TABLE spyglass.agent_invocations
    ADD COLUMN result_policy_version bigint NOT NULL DEFAULT 1 CHECK (result_policy_version>0);

-- The v2 claim remains content-free: it returns immutable policy, later-turn
-- Persona identities and exact frozen document/chunk identities, never prompt,
-- result, document text or provider detail.
CREATE FUNCTION public.spyglass_claim_agent_result_projection_v2(
    p_lease_id uuid,p_now timestamptz,p_lease_seconds integer
) RETURNS TABLE (
    account_id uuid,invocation_id uuid,lease_id uuid,attempt_count integer,
    expected_provider text,requested_model text,permitted_models text[],result_policy_version bigint,
    citation_policy text,action_policy text,current_persona_id uuid,delegate_persona_ids uuid[],citation_bindings text[],
    pod_uid uuid,result_outcome text,result_ciphertext bytea,result_nonce bytea,
    result_key_version integer,result_digest bytea,result_submitted_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE claim_row record; policy_version bigint; frozen_citation_policy text; frozen_action_policy text;
DECLARE frozen_persona_id uuid; frozen_run_id uuid; current_turn integer; delegates uuid[]; bindings text[];
BEGIN
    SELECT * INTO claim_row FROM public.spyglass_claim_agent_result_projection(p_lease_id,p_now,p_lease_seconds);
    IF NOT FOUND THEN RETURN; END IF;
    PERFORM set_config('app.account_id',claim_row.account_id::text,true);
    SELECT i.result_policy_version,COALESCE(v.policy->>'citation_policy','none'),COALESCE(v.policy->>'action_policy','none'),t.persona_id,i.run_id,i.turn
    INTO policy_version,frozen_citation_policy,frozen_action_policy,frozen_persona_id,frozen_run_id,current_turn
    FROM spyglass.agent_invocations i
    JOIN spyglass.agent_run_plan_turns t ON t.account_id=i.account_id AND t.run_id=i.run_id AND t.turn=i.turn
    JOIN spyglass.agent_persona_versions v ON v.account_id=i.account_id AND v.id=i.persona_version_id
    WHERE i.account_id=claim_row.account_id AND i.id=claim_row.invocation_id;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent result policy unavailable'; END IF;
    SELECT ARRAY(
        SELECT t.persona_id FROM spyglass.agent_run_plan_turns t
        WHERE t.account_id=claim_row.account_id AND t.run_id=frozen_run_id AND t.turn>current_turn
        ORDER BY t.turn
    ) INTO delegates;
    SELECT ARRAY(
        SELECT (item->>'id')||':'||(chunk->>'id')
        FROM spyglass.agent_runs r
        CROSS JOIN LATERAL jsonb_array_elements(convert_from(r.context_payload,'UTF8')::jsonb->'items') item
        CROSS JOIN LATERAL jsonb_array_elements(item->'content'->'chunks') chunk
        WHERE r.account_id=claim_row.account_id AND r.id=frozen_run_id AND item->>'kind'='knowledge_document'
        ORDER BY item->>'id',chunk->>'id'
    ) INTO bindings;
    RETURN QUERY SELECT claim_row.account_id,claim_row.invocation_id,claim_row.lease_id,claim_row.attempt_count,
        claim_row.expected_provider,claim_row.requested_model,claim_row.permitted_models,policy_version,
        frozen_citation_policy,frozen_action_policy,frozen_persona_id,delegates,bindings,
        claim_row.pod_uid,claim_row.result_outcome,claim_row.result_ciphertext,claim_row.result_nonce,
        claim_row.result_key_version,claim_row.result_digest,claim_row.result_submitted_at;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_claim_agent_result_projection_v2(uuid,timestamptz,integer) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='spyglass_agent_projection_worker') THEN
        REVOKE EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection(uuid,timestamptz,integer) FROM spyglass_agent_projection_worker;
        GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection_v2(uuid,timestamptz,integer) TO spyglass_agent_projection_worker;
    END IF;
END;
$$;

COMMIT;
