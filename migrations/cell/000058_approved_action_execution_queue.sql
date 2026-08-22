BEGIN;

INSERT INTO spyglass.runner_action_executors
    (capability,executor_id,executor_version,policy_version,enabled,max_definite_attempts,retry_base_delay,retryable_error_codes,updated_at)
VALUES ('finance.entry.post','finance.entry',1,1,true,1,interval '30 seconds',ARRAY[]::text[],statement_timestamp());

-- Durable proposals intentionally outlive their ephemeral runner exchange.
-- Authorization still binds the exact invocation and approval bytes, but its
-- expiry no longer has to fit inside a runner credential that is never reused
-- by the post-approval worker.
CREATE OR REPLACE FUNCTION public.spyglass_record_runner_action_authorization(
    p_account_id uuid,p_operation_id uuid,p_invocation_id uuid,p_approval_id uuid,p_capability text,
    p_input_sha256 bytea,p_hash_version smallint,p_evidence_sha256 bytea,p_proposer_kind text,p_proposer_id text,
    p_approved_by_user_id uuid,p_policy_version bigint,p_approved_at timestamptz,p_expires_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE inserted_count bigint; existing record;
BEGIN
    IF p_account_id IS NULL OR p_operation_id IS NULL OR p_invocation_id IS NULL OR p_approval_id IS NULL OR
       p_capability IS NULL OR p_capability !~ '^[a-z][a-z0-9.:/-]{0,127}$' OR
       p_input_sha256 IS NULL OR octet_length(p_input_sha256)<>32 OR p_hash_version<>1 OR
       p_evidence_sha256 IS NULL OR octet_length(p_evidence_sha256)<>32 OR
       p_proposer_kind NOT IN ('user','workload') OR p_proposer_id IS NULL OR char_length(btrim(p_proposer_id)) NOT BETWEEN 1 AND 200 OR
       p_approved_by_user_id IS NULL OR p_policy_version IS NULL OR p_policy_version<=0 OR
       p_approved_at IS NULL OR p_expires_at IS NULL OR p_expires_at<=p_approved_at OR p_expires_at>p_approved_at+interval '24 hours' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner action authorization';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    PERFORM 1 FROM spyglass.runner_invocation_exchanges invocation_exchange
    WHERE invocation_exchange.account_id=p_account_id AND invocation_exchange.invocation_id=p_invocation_id;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action authorization conflicts with invocation'; END IF;
    INSERT INTO spyglass.runner_action_authorizations
        (account_id,operation_id,invocation_id,approval_id,capability,input_sha256,hash_version,evidence_sha256,
         proposer_kind,proposer_id,approved_by_user_id,policy_version,state,approved_at,expires_at)
    VALUES
        (p_account_id,p_operation_id,p_invocation_id,p_approval_id,p_capability,p_input_sha256,p_hash_version,p_evidence_sha256,
         p_proposer_kind,btrim(p_proposer_id),p_approved_by_user_id,p_policy_version,'approved',p_approved_at,p_expires_at)
    ON CONFLICT (account_id,operation_id) DO NOTHING;
    GET DIAGNOSTICS inserted_count=ROW_COUNT;
    IF inserted_count=1 THEN RETURN true; END IF;
    SELECT * INTO existing FROM spyglass.runner_action_authorizations auth
    WHERE auth.account_id=p_account_id AND auth.operation_id=p_operation_id;
    IF NOT FOUND OR existing.invocation_id<>p_invocation_id OR existing.approval_id<>p_approval_id OR
       existing.capability<>p_capability OR existing.input_sha256<>p_input_sha256 OR existing.hash_version<>p_hash_version OR
       existing.evidence_sha256<>p_evidence_sha256 OR existing.proposer_kind<>p_proposer_kind OR existing.proposer_id<>btrim(p_proposer_id) OR
       existing.approved_by_user_id<>p_approved_by_user_id OR existing.policy_version<>p_policy_version OR
       existing.approved_at<>p_approved_at OR existing.expires_at<>p_expires_at OR existing.state<>'approved' THEN
        RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action authorization conflicts with existing binding';
    END IF;
    RETURN false;
END;
$$;

-- Approved actions execute after the proposing runner has terminated. This
-- content-free queue lets the broker claim work across Accounts without
-- granting it BYPASSRLS access to Attention payloads or Finance records.
CREATE TABLE spyglass.runner_action_execution_queue (
    account_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    available_at timestamptz NOT NULL,
    lease_expires_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,operation_id),
    FOREIGN KEY (account_id,operation_id)
        REFERENCES spyglass.runner_action_authorizations(account_id,operation_id) ON DELETE CASCADE,
    CHECK (lease_expires_at IS NULL OR lease_expires_at>updated_at)
);
CREATE INDEX runner_action_execution_queue_due
    ON spyglass.runner_action_execution_queue(available_at,operation_id);

CREATE FUNCTION spyglass.enqueue_approved_runner_action() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
BEGIN
    INSERT INTO spyglass.runner_action_execution_queue(account_id,operation_id,available_at,updated_at)
    VALUES (NEW.account_id,NEW.operation_id,NEW.approved_at,NEW.approved_at)
    ON CONFLICT (account_id,operation_id) DO NOTHING;
    RETURN NEW;
END;
$$;
CREATE TRIGGER runner_action_authorization_execution_queue
AFTER INSERT ON spyglass.runner_action_authorizations
FOR EACH ROW WHEN (NEW.state='approved') EXECUTE FUNCTION spyglass.enqueue_approved_runner_action();

CREATE FUNCTION spyglass.sync_approved_runner_action_queue() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE authorization_expiry timestamptz; retry_at timestamptz;
BEGIN
    IF NEW.state IN ('executing','reconciling') THEN
        INSERT INTO spyglass.runner_action_execution_queue(account_id,operation_id,available_at,lease_expires_at,updated_at)
        VALUES (NEW.account_id,NEW.operation_id,NEW.lease_expires_at,NEW.lease_expires_at,NEW.updated_at)
        ON CONFLICT (account_id,operation_id) DO UPDATE SET
            available_at=EXCLUDED.available_at,lease_expires_at=EXCLUDED.lease_expires_at,updated_at=EXCLUDED.updated_at;
    ELSIF NEW.state='retry_wait' THEN
        INSERT INTO spyglass.runner_action_execution_queue(account_id,operation_id,available_at,lease_expires_at,updated_at)
        VALUES (NEW.account_id,NEW.operation_id,NEW.next_attempt_at,NULL,NEW.updated_at)
        ON CONFLICT (account_id,operation_id) DO UPDATE SET
            available_at=EXCLUDED.available_at,lease_expires_at=NULL,updated_at=EXCLUDED.updated_at;
    ELSIF NEW.state='unknown' AND NEW.attempt_count<3 THEN
        SELECT expires_at INTO authorization_expiry FROM spyglass.runner_action_authorizations
        WHERE account_id=NEW.account_id AND operation_id=NEW.operation_id;
        retry_at:=NEW.updated_at+interval '30 seconds';
        IF authorization_expiry>retry_at THEN
            INSERT INTO spyglass.runner_action_execution_queue(account_id,operation_id,available_at,lease_expires_at,updated_at)
            VALUES (NEW.account_id,NEW.operation_id,retry_at,NULL,NEW.updated_at)
            ON CONFLICT (account_id,operation_id) DO UPDATE SET
                available_at=EXCLUDED.available_at,lease_expires_at=NULL,updated_at=EXCLUDED.updated_at;
        ELSE
            DELETE FROM spyglass.runner_action_execution_queue WHERE account_id=NEW.account_id AND operation_id=NEW.operation_id;
        END IF;
    ELSE
        DELETE FROM spyglass.runner_action_execution_queue WHERE account_id=NEW.account_id AND operation_id=NEW.operation_id;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER runner_action_ledger_execution_queue
AFTER INSERT OR UPDATE OF state,lease_expires_at,next_attempt_at,updated_at ON spyglass.runner_action_ledger
FOR EACH ROW EXECUTE FUNCTION spyglass.sync_approved_runner_action_queue();

INSERT INTO spyglass.runner_action_execution_queue(account_id,operation_id,available_at,lease_expires_at,updated_at)
SELECT auth.account_id,auth.operation_id,
    CASE
        WHEN ledger.state IN ('executing','reconciling') THEN ledger.lease_expires_at
        WHEN ledger.state='retry_wait' THEN ledger.next_attempt_at
        WHEN ledger.state='unknown' THEN ledger.updated_at+interval '30 seconds'
        ELSE auth.approved_at
    END,
    CASE WHEN ledger.state IN ('executing','reconciling') THEN ledger.lease_expires_at ELSE NULL END,
    COALESCE(ledger.updated_at,auth.approved_at)
FROM spyglass.runner_action_authorizations auth
LEFT JOIN spyglass.runner_action_ledger ledger
  ON ledger.account_id=auth.account_id AND ledger.operation_id=auth.operation_id
WHERE auth.state='approved' AND auth.expires_at>statement_timestamp()
  AND (ledger.operation_id IS NULL OR ledger.state IN ('executing','reconciling','retry_wait') OR
       (ledger.state='unknown' AND ledger.attempt_count<3))
ON CONFLICT (account_id,operation_id) DO NOTHING;

CREATE FUNCTION public.spyglass_claim_approved_runner_action(
    p_attempt_id uuid,p_now timestamptz,p_lease_expires_at timestamptz
) RETURNS TABLE (
    account_id uuid,invocation_id uuid,operation_id uuid,attempt_id uuid,capability text,
    canonical_payload bytea,input_sha256 bytea,approved_by_user_id uuid,mode text,
    idempotency_key uuid,lease_expires_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE queue_row record; authorization_row record; approval_row record; action_row record; executor_row record;
        selected_mode text; previous_attempt uuid; action_exists boolean; effective_lease_expires_at timestamptz;
BEGIN
    IF p_attempt_id IS NULL OR p_now IS NULL OR p_lease_expires_at IS NULL OR p_lease_expires_at<=p_now OR
       p_lease_expires_at>p_now+interval '5 minutes' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid approved runner action claim';
    END IF;
    SELECT queue.account_id,queue.operation_id INTO queue_row
    FROM spyglass.runner_action_execution_queue queue
    WHERE queue.available_at<=p_now AND (queue.lease_expires_at IS NULL OR queue.lease_expires_at<=p_now)
    ORDER BY queue.available_at,queue.operation_id FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;

    PERFORM set_config('app.account_id',queue_row.account_id::text,true);
    SELECT auth.* INTO authorization_row FROM spyglass.runner_action_authorizations auth
    WHERE auth.account_id=queue_row.account_id AND auth.operation_id=queue_row.operation_id FOR UPDATE;
    IF NOT FOUND OR authorization_row.state<>'approved' OR authorization_row.expires_at<=p_now THEN
        DELETE FROM spyglass.runner_action_execution_queue queue
        WHERE queue.account_id=queue_row.account_id AND queue.operation_id=queue_row.operation_id;
        RETURN;
    END IF;
    SELECT approval.canonical_payload,approval.input_sha256 INTO approval_row
    FROM spyglass.attention_consequential_approvals approval
    WHERE approval.account_id=queue_row.account_id AND approval.id=authorization_row.approval_id FOR SHARE;
    IF NOT FOUND OR approval_row.input_sha256<>authorization_row.input_sha256 THEN
        RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='approved runner action payload binding conflict';
    END IF;

    effective_lease_expires_at:=LEAST(p_lease_expires_at,authorization_row.expires_at);
    IF effective_lease_expires_at<=p_now THEN
        DELETE FROM spyglass.runner_action_execution_queue queue
        WHERE queue.account_id=queue_row.account_id AND queue.operation_id=queue_row.operation_id;
        RETURN;
    END IF;

    SELECT ledger.* INTO action_row FROM spyglass.runner_action_ledger ledger
    WHERE ledger.account_id=queue_row.account_id AND ledger.operation_id=queue_row.operation_id FOR UPDATE;
    action_exists:=FOUND;
    IF action_exists THEN
        SELECT executor.* INTO executor_row FROM spyglass.runner_action_executors executor
        WHERE executor.capability=authorization_row.capability AND executor.executor_id=action_row.executor_id AND
          executor.executor_version=action_row.executor_version AND executor.policy_version=action_row.executor_policy_version FOR SHARE;
    ELSE
        SELECT executor.* INTO executor_row FROM spyglass.runner_action_executors executor
        WHERE executor.capability=authorization_row.capability AND executor.enabled FOR SHARE;
    END IF;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='approved runner action executor is not registered'; END IF;

    IF NOT action_exists THEN
        IF NOT executor_row.enabled THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='approved runner action executor is disabled'; END IF;
        selected_mode:='execute';
        INSERT INTO spyglass.runner_action_ledger
            (account_id,operation_id,invocation_id,capability,input_sha256,idempotency_key,state,current_attempt_id,attempt_count,
             lease_expires_at,started_at,updated_at,executor_id,executor_version,executor_policy_version)
        VALUES (authorization_row.account_id,authorization_row.operation_id,authorization_row.invocation_id,authorization_row.capability,
             authorization_row.input_sha256,authorization_row.operation_id,'executing',p_attempt_id,1,effective_lease_expires_at,
             p_now,p_now,executor_row.executor_id,executor_row.executor_version,executor_row.policy_version);
    ELSE
        IF action_row.invocation_id<>authorization_row.invocation_id OR action_row.capability<>authorization_row.capability OR
           action_row.input_sha256<>authorization_row.input_sha256 OR action_row.idempotency_key<>authorization_row.operation_id OR
           action_row.executor_id<>executor_row.executor_id OR action_row.executor_version<>executor_row.executor_version OR
           action_row.executor_policy_version<>executor_row.policy_version THEN
            RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='approved runner action ledger binding conflict';
        END IF;
        IF action_row.state IN ('succeeded','failed','manual_resolution') OR
           (action_row.state='unknown' AND action_row.attempt_count>=3) THEN
            DELETE FROM spyglass.runner_action_execution_queue queue
            WHERE queue.account_id=queue_row.account_id AND queue.operation_id=queue_row.operation_id;
            RETURN;
        END IF;
        IF action_row.state IN ('executing','reconciling') AND action_row.lease_expires_at>p_now THEN RETURN; END IF;
        IF action_row.state='retry_wait' AND action_row.next_attempt_at>p_now THEN RETURN; END IF;
        IF action_row.state IN ('executing','reconciling') THEN
            previous_attempt:=action_row.current_attempt_id;
            UPDATE spyglass.runner_action_attempts prior SET outcome='unknown',error_code='lease_expired',completed_at=p_now
            WHERE prior.account_id=authorization_row.account_id AND prior.attempt_id=previous_attempt AND prior.outcome IS NULL;
        END IF;
        IF action_row.state='retry_wait' THEN
            IF NOT executor_row.enabled THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='approved runner action executor is disabled'; END IF;
            selected_mode:='execute';
        ELSE
            selected_mode:='reconcile';
        END IF;
        UPDATE spyglass.runner_action_ledger ledger SET state=CASE WHEN selected_mode='execute' THEN 'executing' ELSE 'reconciling' END,
            current_attempt_id=p_attempt_id,attempt_count=ledger.attempt_count+1,last_error_code=NULL,
            lease_expires_at=effective_lease_expires_at,next_attempt_at=NULL,updated_at=p_now,completed_at=NULL
        WHERE ledger.account_id=authorization_row.account_id AND ledger.operation_id=authorization_row.operation_id;
    END IF;
    INSERT INTO spyglass.runner_action_attempts
        (account_id,operation_id,attempt_id,mode,started_at,lease_expires_at,executor_id,executor_version,executor_policy_version)
    VALUES (authorization_row.account_id,authorization_row.operation_id,p_attempt_id,selected_mode,p_now,effective_lease_expires_at,
        executor_row.executor_id,executor_row.executor_version,executor_row.policy_version);
    RETURN QUERY SELECT authorization_row.account_id,authorization_row.invocation_id,authorization_row.operation_id,p_attempt_id,
        authorization_row.capability,approval_row.canonical_payload,authorization_row.input_sha256,authorization_row.approved_by_user_id,
        selected_mode,authorization_row.operation_id,effective_lease_expires_at;
END;
$$;

CREATE FUNCTION spyglass.capture_runner_action_execution_queue_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts jsonb; current_count bigint;
BEGIN
    IF current_setting('spyglass.erasure_request_id',true)<>'' AND
       current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        counts:=COALESCE(NULLIF(current_setting('spyglass.runner_action_execution_queue_erasure_counts',true),'')::jsonb,'{}'::jsonb);
        current_count:=COALESCE((counts->>'runner_action_execution_queue')::bigint,0)+1;
        PERFORM set_config('spyglass.runner_action_execution_queue_erasure_counts',
            (counts||jsonb_build_object('runner_action_execution_queue',current_count))::text,true);
    END IF;
    RETURN OLD;
END;
$$;
CREATE FUNCTION spyglass.add_runner_action_execution_queue_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.runner_action_execution_queue_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER runner_action_execution_queue_erasure_count
BEFORE DELETE ON spyglass.runner_action_execution_queue
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_runner_action_execution_queue_erasure_count();
CREATE TRIGGER account_erasure_runner_action_execution_queue_count
BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_runner_action_execution_queue_erasure_count();

REVOKE ALL ON TABLE spyglass.runner_action_execution_queue FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.enqueue_approved_runner_action() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.sync_approved_runner_action_queue() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.capture_runner_action_execution_queue_erasure_count() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.add_runner_action_execution_queue_erasure_count() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_approved_runner_action(uuid,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_record_runner_action_authorization(uuid,uuid,uuid,uuid,text,bytea,smallint,bytea,text,text,uuid,bigint,timestamptz,timestamptz) FROM PUBLIC;

COMMIT;
