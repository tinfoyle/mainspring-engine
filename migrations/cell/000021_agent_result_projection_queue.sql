BEGIN;

-- Agent result projection is cell-wide coordination metadata. It contains no
-- prompt, model output, credential, or customer-authored content and remains
-- outside Account RLS so one shared worker can claim fairly across Accounts.
CREATE TABLE spyglass.agent_result_projection_queue (
    account_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('pending','leased','retry','projected','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
    next_attempt_at timestamptz NOT NULL,
    lease_id uuid,
    lease_expires_at timestamptz,
    last_error_code text CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    projected_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,invocation_id),
    FOREIGN KEY (account_id,invocation_id) REFERENCES spyglass.agent_invocations(account_id,id) ON DELETE CASCADE,
    CHECK ((state='leased' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR
           (state<>'leased' AND lease_id IS NULL AND lease_expires_at IS NULL)),
    CHECK ((state='projected' AND projected_at IS NOT NULL) OR (state<>'projected' AND projected_at IS NULL))
);

CREATE INDEX agent_result_projection_ready
    ON spyglass.agent_result_projection_queue(next_attempt_at,created_at,invocation_id)
    WHERE state IN ('pending','retry','leased');

-- Every future Agent invocation receives a projection job in the same
-- transaction. This trigger also keeps non-HTTP producers from bypassing the
-- projection path.
CREATE FUNCTION spyglass.enqueue_agent_result_projection() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
BEGIN
    INSERT INTO spyglass.agent_result_projection_queue
        (account_id,invocation_id,state,next_attempt_at,projected_at,created_at,updated_at)
    VALUES (NEW.account_id,NEW.id,
        CASE WHEN NEW.status IN ('succeeded','failed','canceled') THEN 'projected' ELSE 'pending' END,
        NEW.queued_at,CASE WHEN NEW.status IN ('succeeded','failed','canceled') THEN NEW.completed_at END,
        NEW.queued_at,COALESCE(NEW.completed_at,NEW.queued_at))
    ON CONFLICT (account_id,invocation_id) DO NOTHING;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_invocations_enqueue_result_projection
AFTER INSERT ON spyglass.agent_invocations
FOR EACH ROW EXECUTE FUNCTION spyglass.enqueue_agent_result_projection();

INSERT INTO spyglass.agent_result_projection_queue
    (account_id,invocation_id,state,next_attempt_at,last_error_code,projected_at,created_at,updated_at)
SELECT i.account_id,i.id,
       CASE WHEN i.status IN ('succeeded','failed','canceled') THEN 'projected'
            WHEN x.terminal_payload_purged_at IS NOT NULL THEN 'dead_letter'
            ELSE 'pending' END,
       i.queued_at,
       CASE WHEN i.status NOT IN ('succeeded','failed','canceled') AND x.terminal_payload_purged_at IS NOT NULL THEN 'payload_unavailable' END,
       CASE WHEN i.status IN ('succeeded','failed','canceled') THEN i.completed_at END,
       i.queued_at,COALESCE(i.completed_at,i.queued_at)
FROM spyglass.agent_invocations i
LEFT JOIN spyglass.runner_invocation_exchanges x
  ON x.account_id=i.account_id AND x.invocation_id=i.id;

-- A claim returns only encrypted material. The worker supplies the lease UUID;
-- the database binds it to exactly one invocation before exposing ciphertext.
CREATE FUNCTION public.spyglass_claim_agent_result_projection(
    p_lease_id uuid,p_now timestamptz,p_lease_seconds integer
) RETURNS TABLE (
    account_id uuid,invocation_id uuid,lease_id uuid,attempt_count integer,
    expected_provider text,requested_model text,
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
    SELECT i.expected_provider,i.requested_model INTO agent_row
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
        agent_row.expected_provider,agent_row.requested_model,
        exchange_row.bound_pod_uid,exchange_row.result_outcome,exchange_row.result_ciphertext,exchange_row.result_nonce,
        exchange_row.result_key_version,exchange_row.result_digest,exchange_row.result_submitted_at;
END;
$$;

-- Remove the pre-lease projector objects entirely. DROP removes any explicit
-- grants already attached to those objects; the new overloads require a live
-- claim token and cannot inherit the old authority.
DROP FUNCTION public.spyglass_project_agent_invocation_success(uuid,uuid,uuid,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,timestamptz);
DROP FUNCTION public.spyglass_project_agent_invocation_failure(uuid,uuid,bytea,text,timestamptz);

CREATE FUNCTION public.spyglass_project_agent_invocation_success(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_message_id uuid,p_provider text,p_response_model text,p_provider_response_id text,
    p_runner_result_digest bytea,p_result_digest bytea,p_result_payload jsonb,p_body text,
    p_input_tokens bigint,p_output_tokens bigint,p_total_tokens bigint,p_completed_at timestamptz,p_now timestamptz
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
       p_total_tokens IS NULL OR p_total_tokens<>p_input_tokens+p_output_tokens OR p_completed_at IS NULL OR p_now IS NULL OR p_completed_at>p_now THEN
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
           existing_message.result_digest=p_result_digest AND existing_message.body=btrim(p_body) THEN RETURN false; END IF;
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
        input_tokens=p_input_tokens,output_tokens=p_output_tokens,total_tokens=p_total_tokens,completed_at=p_completed_at
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

CREATE FUNCTION public.spyglass_project_agent_invocation_failure(
    p_account_id uuid,p_invocation_id uuid,p_lease_id uuid,p_runner_result_digest bytea,p_failure_code text,
    p_completed_at timestamptz,p_now timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE invocation_row record; exchange_row record; queue_row record; succeeded_count bigint; invocation_count bigint; unfinished_count bigint; terminal_state text;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_lease_id IS NULL OR p_runner_result_digest IS NULL OR octet_length(p_runner_result_digest)<>32 OR
       p_failure_code IS NULL OR p_failure_code !~ '^[a-z][a-z0-9_]{0,99}$' OR p_completed_at IS NULL OR p_now IS NULL OR p_completed_at>p_now THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent invocation failure projection';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.agent_result_projection_queue q
    WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id FOR UPDATE;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    IF FOUND AND queue_row.state='projected' THEN
        SELECT i.* INTO invocation_row FROM spyglass.agent_invocations i
        WHERE i.account_id=p_account_id AND i.id=p_invocation_id;
        IF invocation_row.status='failed' AND invocation_row.runner_result_digest=p_runner_result_digest AND
           invocation_row.failure_code=p_failure_code THEN RETURN false; END IF;
        RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent invocation failure projection conflicts with existing result';
    END IF;
    IF queue_row.invocation_id IS NULL OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id OR queue_row.lease_expires_at<statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent result projection lease lost';
    END IF;
    SELECT i.* INTO invocation_row FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.id=p_invocation_id FOR UPDATE;
    SELECT x.result_outcome,x.result_digest INTO exchange_row FROM spyglass.runner_invocation_exchanges x
    WHERE x.account_id=p_account_id AND x.invocation_id=p_invocation_id FOR SHARE;
    IF invocation_row.id IS NULL OR exchange_row.result_outcome<>'execution_failed' OR exchange_row.result_digest<>p_runner_result_digest THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent invocation failure binding denied';
    END IF;
    IF invocation_row.status NOT IN ('queued','running') OR p_completed_at<invocation_row.queued_at THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent invocation cannot be failed from its current state';
    END IF;
    UPDATE spyglass.agent_invocations SET status='failed',runner_result_digest=p_runner_result_digest,failure_code=p_failure_code,completed_at=p_completed_at
    WHERE account_id=p_account_id AND id=p_invocation_id;
    SELECT count(*) INTO succeeded_count FROM spyglass.agent_invocations WHERE account_id=p_account_id AND run_id=invocation_row.run_id AND status='succeeded';
    SELECT count(*),count(*) FILTER (WHERE status IN ('queued','running')) INTO invocation_count,unfinished_count
    FROM spyglass.agent_invocations WHERE account_id=p_account_id AND run_id=invocation_row.run_id;
    SELECT CASE WHEN succeeded_count>0 THEN 'partially_failed' ELSE 'failed' END INTO terminal_state;
    UPDATE spyglass.agent_runs SET state=CASE WHEN invocation_count=turn_count AND unfinished_count=0 THEN terminal_state ELSE 'running' END,
        started_at=COALESCE(started_at,p_completed_at),completed_at=CASE WHEN invocation_count=turn_count AND unfinished_count=0 THEN p_completed_at ELSE NULL END
    WHERE account_id=p_account_id AND id=invocation_row.run_id;
    UPDATE spyglass.agent_result_projection_queue SET state='projected',lease_id=NULL,lease_expires_at=NULL,
        last_error_code=NULL,projected_at=p_now,updated_at=p_now
    WHERE account_id=p_account_id AND invocation_id=p_invocation_id;
    RETURN true;
END;
$$;

CREATE FUNCTION public.spyglass_fail_agent_result_projection(
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
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent result projection failure';
    END IF;
    SELECT q.* INTO queue_row FROM spyglass.agent_result_projection_queue q
    WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id FOR UPDATE;
    IF NOT FOUND OR queue_row.state<>'leased' OR queue_row.lease_id<>p_lease_id THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent result projection lease lost';
    END IF;
    next_state:=CASE WHEN p_retry AND queue_row.attempt_count<p_max_attempts THEN 'retry' ELSE 'dead_letter' END;
    UPDATE spyglass.agent_result_projection_queue SET state=next_state,next_attempt_at=p_next_attempt_at,
        lease_id=NULL,lease_expires_at=NULL,last_error_code=p_error_code,updated_at=p_now
    WHERE account_id=p_account_id AND invocation_id=p_invocation_id;
    RETURN next_state;
END;
$$;

CREATE FUNCTION public.spyglass_agent_result_projection_stats(p_now timestamptz)
RETURNS TABLE(pending bigint,ready bigint,leased bigint,retrying bigint,dead_letter bigint,oldest_ready_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
    SELECT count(*) FILTER (WHERE state='pending'),
           count(*) FILTER (WHERE state IN ('pending','retry') AND next_attempt_at<=p_now OR state='leased' AND lease_expires_at<=p_now),
           count(*) FILTER (WHERE state='leased' AND lease_expires_at>p_now),
           count(*) FILTER (WHERE state='retry'),
           count(*) FILTER (WHERE state='dead_letter'),
           min(COALESCE(lease_expires_at,next_attempt_at)) FILTER (WHERE state IN ('pending','retry') AND next_attempt_at<=p_now OR state='leased' AND lease_expires_at<=p_now)
    FROM spyglass.agent_result_projection_queue
$$;

REVOKE ALL ON FUNCTION spyglass.enqueue_agent_result_projection() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_agent_result_projection(uuid,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_project_agent_invocation_success(uuid,uuid,uuid,uuid,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_project_agent_invocation_failure(uuid,uuid,uuid,bytea,text,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_fail_agent_result_projection(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_agent_result_projection_stats(timestamptz) FROM PUBLIC;

-- Preserve encrypted payloads until a valid projection commits. Dead letters
-- intentionally remain retained for manual recovery rather than being erased
-- by the generic terminal-payload window.
CREATE OR REPLACE FUNCTION public.spyglass_prune_runner_terminal_payloads(
    p_cutoff timestamptz,p_pruned_at timestamptz,p_limit integer
) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE candidate record; pruned_count bigint:=0;
BEGIN
    IF p_cutoff IS NULL OR p_pruned_at IS NULL OR p_cutoff>p_pruned_at OR p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 1000 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner terminal payload retention request';
    END IF;
    FOR candidate IN
        SELECT q.account_id,q.invocation_id
        FROM spyglass.runner_invocation_queue q
        WHERE q.processing_state IN ('completed','execution_failed','canceled')
          AND q.completed_at<=p_cutoff AND q.terminal_payload_purged_at IS NULL
          AND NOT EXISTS (
              SELECT 1 FROM spyglass.agent_result_projection_queue p
              WHERE p.account_id=q.account_id AND p.invocation_id=q.invocation_id AND p.state<>'projected'
          )
        ORDER BY q.completed_at,q.invocation_id
        FOR UPDATE SKIP LOCKED LIMIT p_limit
    LOOP
        PERFORM set_config('app.account_id',candidate.account_id::text,true);
        UPDATE spyglass.runner_invocation_exchanges x SET
            request_ciphertext=decode(repeat('00',17),'hex'),request_nonce=decode(repeat('00',12),'hex'),request_key_version=1,
            result_ciphertext=CASE WHEN x.result_ciphertext IS NULL THEN NULL ELSE decode(repeat('00',17),'hex') END,
            result_nonce=CASE WHEN x.result_nonce IS NULL THEN NULL ELSE decode(repeat('00',12),'hex') END,
            result_key_version=CASE WHEN x.result_key_version IS NULL THEN NULL ELSE 1 END,terminal_payload_purged_at=p_pruned_at
        WHERE x.account_id=candidate.account_id AND x.invocation_id=candidate.invocation_id AND x.terminal_payload_purged_at IS NULL;
        UPDATE spyglass.runner_invocation_queue q SET terminal_payload_purged_at=p_pruned_at
        WHERE q.account_id=candidate.account_id AND q.invocation_id=candidate.invocation_id AND q.terminal_payload_purged_at IS NULL;
        pruned_count:=pruned_count+1;
    END LOOP;
    RETURN pruned_count;
END;
$$;

-- Extend the stable erasure ABI with the identifier-only projection queue.
-- The inner Agents eraser removes queue rows through the invocation FK; this
-- wrapper captures their exact count before that cascade occurs.
ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz)
    RENAME TO spyglass_erase_account_cell_without_agent_projection;
ALTER FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea)
    RENAME TO spyglass_replay_account_cell_erasure_without_agent_projection;

CREATE FUNCTION spyglass.add_agent_projection_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.agent_projection_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_erasure_agent_projection_counts BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_agent_projection_erasure_counts();

CREATE FUNCTION public.spyglass_erase_account_cell(
    p_request_id uuid,p_account_id uuid,p_placement_generation bigint,p_account_fingerprint bytea,
    p_policy_version bigint,p_request_version bigint,p_environment text,p_export_sha256 bytea,
    p_operator_evidence_sha256 bytea,p_backup_expires_at timestamptz
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE projection_count bigint;
BEGIN
    SELECT count(*) INTO projection_count FROM spyglass.agent_result_projection_queue WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.agent_projection_erasure_counts',jsonb_build_object(
        'agent_result_projection_queue',projection_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_erase_account_cell_without_agent_projection(
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
DECLARE projection_count bigint;
BEGIN
    SELECT count(*) INTO projection_count FROM spyglass.agent_result_projection_queue WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.agent_projection_erasure_counts',jsonb_build_object(
        'agent_result_projection_queue',projection_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_replay_account_cell_erasure_without_agent_projection(
        p_request_id,p_account_id,p_restored_placement_generation,p_tombstone_placement_generation,p_account_fingerprint,
        p_policy_version,p_request_version,p_environment,p_erased_at,p_export_sha256,p_operator_evidence_sha256,
        p_backup_expires_at,p_previous_ledger_sequence,p_previous_ledger_root,p_expected_ledger_sequence,p_expected_ledger_root);
END;
$$;

REVOKE ALL ON FUNCTION spyglass.add_agent_projection_erasure_counts() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell_without_agent_projection(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure_without_agent_projection(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;

COMMIT;
