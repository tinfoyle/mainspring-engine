BEGIN;

-- A Persona version freezes an ordered, same-provider model target set. The
-- primary target remains requested_model for compatibility; selected_model is
-- written only when a successful result is projected.
CREATE FUNCTION spyglass.valid_agent_model_targets(p_targets text[]) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE SET search_path=pg_catalog
AS $$
DECLARE target text; seen text[] := ARRAY[]::text[];
BEGIN
    IF cardinality(p_targets) NOT BETWEEN 1 AND 3 THEN RETURN false; END IF;
    FOREACH target IN ARRAY p_targets LOOP
        IF target IS NULL OR target !~ '^[a-z][a-z0-9._:-]{0,127}$' OR target=ANY(seen) THEN RETURN false; END IF;
        seen:=array_append(seen,target);
    END LOOP;
    RETURN true;
END;
$$;

ALTER TABLE spyglass.agent_invocations
    ADD COLUMN permitted_models text[],
    ADD COLUMN selected_model text;
UPDATE spyglass.agent_invocations SET permitted_models=ARRAY[requested_model];
UPDATE spyglass.agent_invocations SET selected_model=requested_model WHERE status='succeeded';
ALTER TABLE spyglass.agent_invocations
    ALTER COLUMN permitted_models SET NOT NULL,
    ADD CONSTRAINT agent_invocations_permitted_models_valid CHECK (spyglass.valid_agent_model_targets(permitted_models)),
    ADD CONSTRAINT agent_invocations_primary_model_frozen CHECK (permitted_models[1]=requested_model),
    ADD CONSTRAINT agent_invocations_selected_model_permitted CHECK (selected_model IS NULL OR selected_model=ANY(permitted_models)),
    ADD CONSTRAINT agent_invocations_selected_model_terminal CHECK ((status='succeeded')=(selected_model IS NOT NULL));

-- Direct internal producers that predate this migration remain primary-only.
-- Current repositories always supply the complete frozen target set.
CREATE FUNCTION spyglass.default_agent_invocation_model_targets() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,spyglass
AS $$
BEGIN
    IF NEW.permitted_models IS NULL OR cardinality(NEW.permitted_models)=0 THEN
        NEW.permitted_models:=ARRAY[NEW.requested_model];
    END IF;
	IF NEW.status='succeeded' AND NEW.selected_model IS NULL THEN
		NEW.selected_model:=NEW.requested_model;
	END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER agent_invocations_default_model_targets
BEFORE INSERT ON spyglass.agent_invocations
FOR EACH ROW EXECUTE FUNCTION spyglass.default_agent_invocation_model_targets();

-- Each model step receives one operation identity per permitted target. Tool
-- identities remain one per possible tool step.
DO $$
DECLARE constraint_row record;
BEGIN
    FOR constraint_row IN
        SELECT conname FROM pg_constraint
        WHERE conrelid='spyglass.agent_invocation_execution_plans'::regclass AND contype='c'
          AND pg_get_constraintdef(oid) LIKE '%cardinality(model_operation_ids)%'
    LOOP
        EXECUTE format('ALTER TABLE spyglass.agent_invocation_execution_plans DROP CONSTRAINT %I',constraint_row.conname);
    END LOOP;
END;
$$;
ALTER TABLE spyglass.agent_invocation_execution_plans
    ADD CONSTRAINT agent_execution_plan_model_operations_bounded CHECK (cardinality(model_operation_ids) BETWEEN 1 AND 18),
    ADD CONSTRAINT agent_execution_plan_model_target_shape CHECK (
        cardinality(model_operation_ids)%(cardinality(tool_operation_ids)+1)=0 AND
        cardinality(model_operation_ids)/(cardinality(tool_operation_ids)+1) BETWEEN 1 AND 3);

-- Return the frozen target set with the encrypted result claim. Changing a
-- TABLE return signature requires replacing the function object explicitly.
DROP FUNCTION public.spyglass_claim_agent_result_projection(uuid,timestamptz,integer);
CREATE FUNCTION public.spyglass_claim_agent_result_projection(
    p_lease_id uuid,p_now timestamptz,p_lease_seconds integer
) RETURNS TABLE (
    account_id uuid,invocation_id uuid,lease_id uuid,attempt_count integer,
    expected_provider text,requested_model text,permitted_models text[],
    pod_uid uuid,result_outcome text,result_ciphertext bytea,result_nonce bytea,
    result_key_version integer,result_digest bytea,result_submitted_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE candidate record; exchange_row record; agent_row record; claimed_attempt integer;
BEGIN
    IF p_lease_id IS NULL OR p_now IS NULL OR p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent result projection claim';
    END IF;
    SELECT q.account_id,q.invocation_id INTO candidate
    FROM spyglass.agent_result_projection_queue q
    JOIN spyglass.runner_invocation_queue r ON r.account_id=q.account_id AND r.invocation_id=q.invocation_id
    WHERE ((q.state IN ('pending','retry') AND q.next_attempt_at<=p_now) OR
           (q.state='leased' AND q.lease_expires_at<=p_now))
      AND r.processing_state IN ('completed','execution_failed')
    ORDER BY COALESCE(q.lease_expires_at,q.next_attempt_at),q.created_at,q.invocation_id
    FOR UPDATE OF q SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;
    UPDATE spyglass.agent_result_projection_queue q SET
        state='leased',lease_id=p_lease_id,lease_expires_at=p_now+(p_lease_seconds*interval '1 second'),
        attempt_count=q.attempt_count+1,last_error_code=NULL,updated_at=p_now
    WHERE q.account_id=candidate.account_id AND q.invocation_id=candidate.invocation_id
    RETURNING q.attempt_count INTO claimed_attempt;
    PERFORM set_config('app.account_id',candidate.account_id::text,true);
    SELECT i.expected_provider,i.requested_model,i.permitted_models INTO agent_row
    FROM spyglass.agent_invocations i
    WHERE i.account_id=candidate.account_id AND i.id=candidate.invocation_id AND i.status IN ('queued','running') FOR SHARE;
    IF NOT FOUND THEN
        UPDATE spyglass.agent_result_projection_queue q SET state='dead_letter',lease_id=NULL,lease_expires_at=NULL,
            last_error_code='projection_target_invalid',updated_at=p_now
        WHERE q.account_id=candidate.account_id AND q.invocation_id=candidate.invocation_id;
        RETURN;
    END IF;
    SELECT x.bound_pod_uid,x.result_outcome,x.result_ciphertext,x.result_nonce,x.result_key_version,x.result_digest,x.result_submitted_at
    INTO exchange_row FROM spyglass.runner_invocation_exchanges x
    WHERE x.account_id=candidate.account_id AND x.invocation_id=candidate.invocation_id
      AND x.result_digest IS NOT NULL AND x.terminal_payload_purged_at IS NULL FOR SHARE;
    IF NOT FOUND THEN
        UPDATE spyglass.agent_result_projection_queue q SET state='dead_letter',lease_id=NULL,lease_expires_at=NULL,
            last_error_code='payload_unavailable',updated_at=p_now
        WHERE q.account_id=candidate.account_id AND q.invocation_id=candidate.invocation_id;
        RETURN;
    END IF;
    RETURN QUERY SELECT candidate.account_id,candidate.invocation_id,p_lease_id,claimed_attempt,
        agent_row.expected_provider,agent_row.requested_model,agent_row.permitted_models,
        exchange_row.bound_pod_uid,exchange_row.result_outcome,exchange_row.result_ciphertext,exchange_row.result_nonce,
        exchange_row.result_key_version,exchange_row.result_digest,exchange_row.result_submitted_at;
END;
$$;

-- The already-shipped projector owns the sequential message/run transaction.
-- A narrow trigger lets the new wrapper bind selected_model inside that same
-- UPDATE statement, so no transient succeeded-without-selection state exists.
CREATE FUNCTION spyglass.bind_agent_invocation_selected_model() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,spyglass
AS $$
DECLARE selected text;
BEGIN
    IF NEW.status='succeeded' AND NEW.selected_model IS NULL THEN
        selected:=nullif(current_setting('spyglass.selected_agent_model',true),'');
        IF selected IS NULL OR NOT selected=ANY(NEW.permitted_models) THEN
            RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='agent selected model binding missing';
        END IF;
        NEW.selected_model:=selected;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER agent_invocations_bind_selected_model
BEFORE UPDATE OF status ON spyglass.agent_invocations
FOR EACH ROW WHEN (NEW.status='succeeded')
EXECUTE FUNCTION spyglass.bind_agent_invocation_selected_model();

CREATE FUNCTION public.spyglass_project_agent_invocation_success(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_message_id uuid,p_provider text,p_selected_model text,
    p_response_model text,p_provider_response_id text,p_runner_result_digest bytea,p_result_digest bytea,p_result_payload jsonb,p_body text,
    p_input_tokens bigint,p_output_tokens bigint,p_total_tokens bigint,p_cost_micros bigint,p_completed_at timestamptz,p_now timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE projected boolean; invocation_row record;
BEGIN
    IF p_selected_model IS NULL OR p_selected_model !~ '^[a-z][a-z0-9._:-]{0,127}$' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent invocation selected model';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT i.permitted_models,i.selected_model INTO invocation_row
    FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.id=p_invocation_id;
    IF NOT FOUND OR NOT p_selected_model=ANY(invocation_row.permitted_models) OR
       (invocation_row.selected_model IS NOT NULL AND invocation_row.selected_model<>p_selected_model) THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent invocation selected model denied';
    END IF;
    PERFORM set_config('spyglass.selected_agent_model',p_selected_model,true);
    SELECT public.spyglass_project_agent_invocation_success(
        p_account_id,p_invocation_id,p_lease_id,p_message_id,p_provider,p_response_model,p_provider_response_id,
        p_runner_result_digest,p_result_digest,p_result_payload,p_body,p_input_tokens,p_output_tokens,p_total_tokens,p_cost_micros,p_completed_at,p_now
    ) INTO projected;
    SELECT i.selected_model INTO invocation_row
    FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.id=p_invocation_id;
    IF invocation_row.selected_model IS DISTINCT FROM p_selected_model THEN
        RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent invocation selected model conflicts with existing result';
    END IF;
    RETURN projected;
END;
$$;

-- Preserve the previously audited Work-to-Agent transaction by wrapping it.
-- The old function receives the primary operation for each step; before commit
-- this overload expands the frozen invocation and plan to the complete target
-- set. Dispatch cannot observe the intermediate rows outside this transaction.
CREATE FUNCTION public.spyglass_start_link_work_agent_execution(
    p_execution_id uuid,p_account_id uuid,p_lease_id uuid,p_entitlement_version bigint,p_maximum_concurrent_runs bigint,
    p_plan_digest bytea,p_event_id uuid,p_user_message_id uuid,p_invocation_id uuid,p_profile text,p_model_targets text[],
    p_model_operation_ids uuid[],p_tool_operation_ids uuid[],p_request_expires_at timestamptz,p_now timestamptz
) RETURNS TABLE(created_run boolean,linked_run boolean,reconciled boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE expected_targets text[]; primary_operation_ids uuid[]:=ARRAY[]::uuid[]; target_count integer; step integer; result_row record;
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
    END IF;
    RETURN QUERY SELECT result_row.created_run,result_row.linked_run,result_row.reconciled;
END;
$$;

REVOKE ALL ON FUNCTION spyglass.valid_agent_model_targets(text[]) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.default_agent_invocation_model_targets() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.bind_agent_invocation_selected_model() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_agent_result_projection(uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_project_agent_invocation_success(uuid,uuid,uuid,uuid,text,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_start_link_work_agent_execution(uuid,uuid,uuid,bigint,bigint,bytea,uuid,uuid,uuid,text,text[],uuid[],uuid[],timestamptz,timestamptz) FROM PUBLIC;

-- Upgrade an already-provisioned environment without requiring the role file
-- to be replayed; fresh installations receive these grants from that file.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='spyglass_agent_projection_worker') THEN
        REVOKE EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_success(uuid,uuid,uuid,uuid,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz) FROM spyglass_agent_projection_worker;
        GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection(uuid,timestamptz,integer) TO spyglass_agent_projection_worker;
        GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_success(uuid,uuid,uuid,uuid,text,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz) TO spyglass_agent_projection_worker;
	END IF;
	IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='spyglass_agent_dispatch_worker') THEN
		REVOKE EXECUTE ON FUNCTION public.spyglass_start_link_work_agent_execution(uuid,uuid,uuid,bigint,bigint,bytea,uuid,uuid,uuid,text,uuid[],uuid[],timestamptz,timestamptz) FROM spyglass_agent_dispatch_worker;
		GRANT EXECUTE ON FUNCTION public.spyglass_start_link_work_agent_execution(uuid,uuid,uuid,bigint,bigint,bytea,uuid,uuid,uuid,text,text[],uuid[],uuid[],timestamptz,timestamptz) TO spyglass_agent_dispatch_worker;
	END IF;
END;
$$;

COMMIT;
