BEGIN;

-- Human conversation input is immutable and sequenced in the same stream as
-- persona output. Separate tables keep the already-shipped persona-message
-- constraints narrow while next_message_sequence prevents cross-table order
-- collisions.
CREATE TABLE spyglass.agent_user_messages (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    sequence bigint NOT NULL CHECK (sequence>0),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 65536),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,conversation_id,sequence),
    FOREIGN KEY (account_id,conversation_id) REFERENCES spyglass.agent_conversations(account_id,id) ON DELETE CASCADE
);

-- A run freezes the context watermark and every operation identity needed by
-- the bounded runner loop. Customer text remains in Account-owned RLS tables;
-- the cross-Account dispatch queue below carries identifiers only.
CREATE TABLE spyglass.agent_invocation_execution_plans (
    account_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    context_sequence bigint NOT NULL CHECK (context_sequence>0),
    profile text NOT NULL CHECK (profile ~ '^[a-z][a-z0-9-]{0,49}$'),
    model_operation_ids uuid[] NOT NULL,
    tool_operation_ids uuid[] NOT NULL,
    request_expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,invocation_id),
    FOREIGN KEY (account_id,invocation_id) REFERENCES spyglass.agent_invocations(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,conversation_id) REFERENCES spyglass.agent_conversations(account_id,id) ON DELETE CASCADE,
    CHECK (cardinality(model_operation_ids) BETWEEN 1 AND 6),
    CHECK (cardinality(tool_operation_ids) BETWEEN 0 AND 5),
    CHECK (cardinality(model_operation_ids)=cardinality(tool_operation_ids)+1),
    CHECK (request_expires_at>created_at AND request_expires_at<=created_at+interval '24 hours')
);

CREATE TABLE spyglass.agent_dispatch_queue (
    account_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('pending','leased','retry','provisioned','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at timestamptz NOT NULL,
    lease_id uuid,
    lease_expires_at timestamptz,
    request_digest bytea CHECK (request_digest IS NULL OR octet_length(request_digest)=32),
    last_error_code text CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    provisioned_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,invocation_id),
    FOREIGN KEY (account_id,invocation_id) REFERENCES spyglass.agent_invocation_execution_plans(account_id,invocation_id) ON DELETE CASCADE,
    CHECK ((state='leased' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR
           (state<>'leased' AND lease_id IS NULL AND lease_expires_at IS NULL)),
    CHECK ((state='provisioned' AND request_digest IS NOT NULL AND provisioned_at IS NOT NULL) OR
           (state<>'provisioned' AND request_digest IS NULL AND provisioned_at IS NULL))
);

CREATE INDEX agent_user_messages_conversation ON spyglass.agent_user_messages(account_id,conversation_id,sequence);
CREATE INDEX agent_dispatch_ready ON spyglass.agent_dispatch_queue(next_attempt_at,created_at,invocation_id)
    WHERE state IN ('pending','retry','leased');

ALTER TABLE spyglass.agent_user_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_user_messages FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_invocation_execution_plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_invocation_execution_plans FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_user_messages_isolation ON spyglass.agent_user_messages
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY agent_invocation_execution_plans_isolation ON spyglass.agent_invocation_execution_plans
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION spyglass.enqueue_agent_dispatch() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
BEGIN
    INSERT INTO spyglass.agent_dispatch_queue
        (account_id,invocation_id,state,next_attempt_at,created_at,updated_at)
    VALUES (NEW.account_id,NEW.invocation_id,'pending',NEW.created_at,NEW.created_at,NEW.created_at)
    ON CONFLICT (account_id,invocation_id) DO NOTHING;
    RETURN NEW;
END;
$$;
CREATE TRIGGER agent_execution_plans_enqueue_dispatch
AFTER INSERT ON spyglass.agent_invocation_execution_plans
FOR EACH ROW EXECUTE FUNCTION spyglass.enqueue_agent_dispatch();

CREATE FUNCTION public.spyglass_claim_agent_dispatch(
    p_lease_id uuid,p_now timestamptz,p_lease_seconds integer
) RETURNS TABLE(account_id uuid,invocation_id uuid,lease_id uuid,attempt_count integer)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE candidate record; claimed_attempt integer;
BEGIN
    IF p_lease_id IS NULL OR p_now IS NULL OR p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 1 AND 1800 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent dispatch claim';
    END IF;
    SELECT q.account_id,q.invocation_id INTO candidate
    FROM spyglass.agent_dispatch_queue q
    WHERE (q.state IN ('pending','retry') AND q.next_attempt_at<=p_now)
       OR (q.state='leased' AND q.lease_expires_at<=p_now)
    ORDER BY COALESCE(q.lease_expires_at,q.next_attempt_at),q.created_at,q.invocation_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;
    UPDATE spyglass.agent_dispatch_queue q SET
        state='leased',lease_id=p_lease_id,lease_expires_at=p_now+(p_lease_seconds*interval '1 second'),
        attempt_count=q.attempt_count+1,last_error_code=NULL,updated_at=p_now
    WHERE q.account_id=candidate.account_id AND q.invocation_id=candidate.invocation_id
    RETURNING q.attempt_count INTO claimed_attempt;
    RETURN QUERY SELECT candidate.account_id,candidate.invocation_id,p_lease_id,claimed_attempt;
END;
$$;

CREATE FUNCTION public.spyglass_complete_agent_dispatch(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_request_digest bytea,p_now timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record; exchange_digest bytea;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_lease_id IS NULL OR
       p_request_digest IS NULL OR octet_length(p_request_digest)<>32 OR p_now IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent dispatch completion';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.agent_dispatch_queue q
    WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id FOR UPDATE;
    IF queue_row.state='provisioned' THEN
        IF queue_row.request_digest=p_request_digest THEN RETURN false; END IF;
        RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent dispatch conflicts with provisioned request';
    END IF;
    IF queue_row.invocation_id IS NULL OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR
       queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent dispatch lease lost';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT x.request_digest INTO exchange_digest FROM spyglass.runner_invocation_exchanges x
    WHERE x.account_id=p_account_id AND x.invocation_id=p_invocation_id FOR SHARE;
    IF NOT FOUND OR exchange_digest<>p_request_digest THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent dispatch runner request binding denied';
    END IF;
    UPDATE spyglass.agent_dispatch_queue SET state='provisioned',lease_id=NULL,lease_expires_at=NULL,
        request_digest=p_request_digest,last_error_code=NULL,provisioned_at=p_now,updated_at=p_now
    WHERE account_id=p_account_id AND invocation_id=p_invocation_id;
    RETURN true;
END;
$$;

CREATE FUNCTION public.spyglass_fail_agent_dispatch(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_retry boolean,p_next_attempt_at timestamptz,
    p_error_code text,p_now timestamptz,p_max_attempts integer
) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record; next_state text;
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
    RETURN next_state;
END;
$$;

CREATE FUNCTION public.spyglass_agent_dispatch_stats(p_now timestamptz)
RETURNS TABLE(pending bigint,ready bigint,leased bigint,retrying bigint,provisioned bigint,dead_letter bigint,oldest_ready_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
    SELECT count(*) FILTER (WHERE state='pending'),
           count(*) FILTER (WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now)),
           count(*) FILTER (WHERE state='leased' AND lease_expires_at>p_now),
           count(*) FILTER (WHERE state='retry'),count(*) FILTER (WHERE state='provisioned'),
           count(*) FILTER (WHERE state='dead_letter'),
           min(COALESCE(lease_expires_at,next_attempt_at)) FILTER (
               WHERE (state IN ('pending','retry') AND next_attempt_at<=p_now) OR (state='leased' AND lease_expires_at<=p_now))
    FROM spyglass.agent_dispatch_queue
$$;

REVOKE ALL ON FUNCTION spyglass.enqueue_agent_dispatch() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_agent_dispatch(uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_complete_agent_dispatch(uuid,uuid,uuid,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_fail_agent_dispatch(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_agent_dispatch_stats(timestamptz) FROM PUBLIC;

-- Add all new Account-attributed rows to exact erasure accounting while
-- retaining the stable public function signatures.
ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz)
    RENAME TO spyglass_erase_account_cell_without_agent_dispatch;
ALTER FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea)
    RENAME TO spyglass_replay_account_cell_erasure_without_agent_dispatch;

CREATE FUNCTION spyglass.add_agent_dispatch_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.agent_dispatch_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_erasure_agent_dispatch_counts BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_agent_dispatch_erasure_counts();

CREATE FUNCTION public.spyglass_erase_account_cell(
    p_request_id uuid,p_account_id uuid,p_placement_generation bigint,p_account_fingerprint bytea,
    p_policy_version bigint,p_request_version bigint,p_environment text,p_export_sha256 bytea,
    p_operator_evidence_sha256 bytea,p_backup_expires_at timestamptz
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE user_message_count bigint; execution_plan_count bigint; dispatch_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT count(*) INTO user_message_count FROM spyglass.agent_user_messages WHERE account_id=p_account_id;
    SELECT count(*) INTO execution_plan_count FROM spyglass.agent_invocation_execution_plans WHERE account_id=p_account_id;
    SELECT count(*) INTO dispatch_count FROM spyglass.agent_dispatch_queue WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.agent_dispatch_erasure_counts',jsonb_build_object(
        'agent_user_messages',user_message_count,'agent_invocation_execution_plans',execution_plan_count,
        'agent_dispatch_queue',dispatch_count)::text,true);
    DELETE FROM spyglass.agent_user_messages WHERE account_id=p_account_id;
    RETURN QUERY SELECT * FROM public.spyglass_erase_account_cell_without_agent_dispatch(
        p_request_id,p_account_id,p_placement_generation,p_account_fingerprint,p_policy_version,p_request_version,
        p_environment,p_export_sha256,p_operator_evidence_sha256,p_backup_expires_at);
END;
$$;

CREATE FUNCTION public.spyglass_replay_account_cell_erasure(
    p_request_id uuid,p_account_id uuid,p_restored_placement_generation bigint,p_tombstone_placement_generation bigint,
    p_account_fingerprint bytea,p_policy_version bigint,p_request_version bigint,p_environment text,p_erased_at timestamptz,
    p_export_sha256 bytea,p_operator_evidence_sha256 bytea,p_backup_expires_at timestamptz,
    p_previous_ledger_sequence bigint,p_previous_ledger_root bytea,p_expected_ledger_sequence bigint,p_expected_ledger_root bytea
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE user_message_count bigint; execution_plan_count bigint; dispatch_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT count(*) INTO user_message_count FROM spyglass.agent_user_messages WHERE account_id=p_account_id;
    SELECT count(*) INTO execution_plan_count FROM spyglass.agent_invocation_execution_plans WHERE account_id=p_account_id;
    SELECT count(*) INTO dispatch_count FROM spyglass.agent_dispatch_queue WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.agent_dispatch_erasure_counts',jsonb_build_object(
        'agent_user_messages',user_message_count,'agent_invocation_execution_plans',execution_plan_count,
        'agent_dispatch_queue',dispatch_count)::text,true);
    DELETE FROM spyglass.agent_user_messages WHERE account_id=p_account_id;
    RETURN QUERY SELECT * FROM public.spyglass_replay_account_cell_erasure_without_agent_dispatch(
        p_request_id,p_account_id,p_restored_placement_generation,p_tombstone_placement_generation,p_account_fingerprint,
        p_policy_version,p_request_version,p_environment,p_erased_at,p_export_sha256,p_operator_evidence_sha256,
        p_backup_expires_at,p_previous_ledger_sequence,p_previous_ledger_root,p_expected_ledger_sequence,p_expected_ledger_root);
END;
$$;

REVOKE ALL ON FUNCTION spyglass.add_agent_dispatch_erasure_counts() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell_without_agent_dispatch(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure_without_agent_dispatch(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;

COMMIT;
