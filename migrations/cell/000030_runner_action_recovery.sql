BEGIN;

-- Versioned, content-free executor registry. Every operation freezes these
-- values before the first provider call.
CREATE TABLE spyglass.runner_action_executors (
    capability text NOT NULL CHECK (capability ~ '^[a-z][a-z0-9.:/-]{0,127}$'),
    executor_id text NOT NULL CHECK (executor_id ~ '^[a-z][a-z0-9._/-]{0,127}$'),
    executor_version bigint NOT NULL CHECK (executor_version>0),
    policy_version bigint NOT NULL CHECK (policy_version>0),
    enabled boolean NOT NULL,
    max_definite_attempts integer NOT NULL CHECK (max_definite_attempts BETWEEN 1 AND 20),
    retry_base_delay interval NOT NULL CHECK (retry_base_delay>=interval '1 second' AND retry_base_delay<=interval '1 hour'),
    retryable_error_codes text[] NOT NULL CHECK (cardinality(retryable_error_codes)<=32 AND array_position(retryable_error_codes,NULL) IS NULL),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (capability,executor_version,policy_version)
);
CREATE UNIQUE INDEX runner_action_executor_enabled_capability
    ON spyglass.runner_action_executors(capability) WHERE enabled;
INSERT INTO spyglass.runner_action_executors
    (capability,executor_id,executor_version,policy_version,enabled,max_definite_attempts,retry_base_delay,retryable_error_codes,updated_at)
VALUES ('stripe.customer.create','stripe.customer',1,1,true,3,interval '30 seconds',ARRAY['stripe_rate_limited'],statement_timestamp());

ALTER TABLE spyglass.runner_action_ledger
    ADD COLUMN executor_id text,
    ADD COLUMN executor_version bigint,
    ADD COLUMN executor_policy_version bigint,
    ADD COLUMN next_attempt_at timestamptz;
UPDATE spyglass.runner_action_ledger SET executor_id='legacy',executor_version=1,executor_policy_version=1;
INSERT INTO spyglass.runner_action_executors
    (capability,executor_id,executor_version,policy_version,enabled,max_definite_attempts,retry_base_delay,retryable_error_codes,updated_at)
SELECT DISTINCT capability,'legacy',1,1,false,1,interval '1 minute',ARRAY[]::text[],statement_timestamp()
FROM spyglass.runner_action_ledger
ON CONFLICT (capability,executor_version,policy_version) DO NOTHING;
ALTER TABLE spyglass.runner_action_ledger
    ALTER COLUMN executor_id SET NOT NULL, ALTER COLUMN executor_version SET NOT NULL,
    ALTER COLUMN executor_policy_version SET NOT NULL,
    ADD CHECK (executor_id ~ '^[a-z][a-z0-9._/-]{0,127}$'), ADD CHECK (executor_version>0),
    ADD CHECK (executor_policy_version>0);

ALTER TABLE spyglass.runner_action_attempts
    ADD COLUMN executor_id text, ADD COLUMN executor_version bigint, ADD COLUMN executor_policy_version bigint;
UPDATE spyglass.runner_action_attempts AS attempt
SET executor_id=ledger.executor_id,executor_version=ledger.executor_version,executor_policy_version=ledger.executor_policy_version
FROM spyglass.runner_action_ledger AS ledger
WHERE ledger.account_id=attempt.account_id AND ledger.operation_id=attempt.operation_id;
ALTER TABLE spyglass.runner_action_attempts
    ALTER COLUMN executor_id SET NOT NULL, ALTER COLUMN executor_version SET NOT NULL,
    ALTER COLUMN executor_policy_version SET NOT NULL,
    ADD CHECK (executor_id ~ '^[a-z][a-z0-9._/-]{0,127}$'), ADD CHECK (executor_version>0),
    ADD CHECK (executor_policy_version>0);

ALTER TABLE spyglass.runner_action_ledger DROP CONSTRAINT runner_action_ledger_state_check;
ALTER TABLE spyglass.runner_action_ledger DROP CONSTRAINT runner_action_ledger_check;
ALTER TABLE spyglass.runner_action_ledger DROP CONSTRAINT runner_action_ledger_check1;
ALTER TABLE spyglass.runner_action_ledger
    ADD CHECK (idempotency_key=operation_id),
    ADD CHECK (state IN ('executing','reconciling','retry_wait','succeeded','failed','unknown','manual_resolution')),
    ADD CHECK (
        (state IN ('executing','reconciling') AND lease_expires_at IS NOT NULL AND next_attempt_at IS NULL AND completed_at IS NULL) OR
        (state='retry_wait' AND lease_expires_at IS NULL AND next_attempt_at IS NOT NULL AND completed_at IS NULL) OR
        (state IN ('succeeded','failed','unknown','manual_resolution') AND lease_expires_at IS NULL AND next_attempt_at IS NULL AND completed_at IS NOT NULL)
    );
CREATE INDEX runner_action_ledger_retry ON spyglass.runner_action_ledger(next_attempt_at,account_id,operation_id) WHERE state='retry_wait';

CREATE TABLE spyglass.runner_action_manual_resolutions (
    account_id uuid NOT NULL,
    resolution_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    requested_outcome text NOT NULL CHECK (requested_outcome IN ('succeeded','failed')),
    reason_sha256 bytea NOT NULL CHECK (octet_length(reason_sha256)=32),
    requested_by_user_id uuid NOT NULL,
    requested_at timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('pending','applied')),
    confirmed_by_user_id uuid,
    confirmed_at timestamptz,
    PRIMARY KEY (account_id,resolution_id),
    FOREIGN KEY (account_id,operation_id) REFERENCES spyglass.runner_action_ledger(account_id,operation_id) ON DELETE CASCADE,
    CHECK ((state='pending' AND confirmed_by_user_id IS NULL AND confirmed_at IS NULL) OR
           (state='applied' AND confirmed_by_user_id IS NOT NULL AND confirmed_at IS NOT NULL AND confirmed_by_user_id<>requested_by_user_id))
);
CREATE UNIQUE INDEX runner_action_manual_resolution_pending ON spyglass.runner_action_manual_resolutions(account_id,operation_id) WHERE state='pending';
CREATE INDEX runner_action_manual_resolution_queue ON spyglass.runner_action_manual_resolutions(account_id,state,requested_at,resolution_id);
ALTER TABLE spyglass.runner_action_manual_resolutions ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.runner_action_manual_resolutions FORCE ROW LEVEL SECURITY;
CREATE POLICY runner_action_manual_resolutions_isolation ON spyglass.runner_action_manual_resolutions
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE TRIGGER account_namespace_write_fence
BEFORE INSERT OR UPDATE OR DELETE ON spyglass.runner_action_manual_resolutions
FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence();

CREATE FUNCTION public.spyglass_begin_runner_action_v2(
    p_account_id uuid,p_invocation_id uuid,p_pod_uid uuid,p_operation_id uuid,p_capability text,p_input_sha256 bytea,
    p_attempt_id uuid,p_now timestamptz,p_lease_expires_at timestamptz
) RETURNS TABLE (account_id uuid,invocation_id uuid,operation_id uuid,attempt_id uuid,capability text,input_sha256 bytea,
    mode text,idempotency_key uuid,lease_expires_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE authorization_row record; action_row record; executor_row record; selected_mode text; previous_attempt uuid; action_exists boolean;
        invocation_expires_at timestamptz; effective_lease_expires_at timestamptz;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_pod_uid IS NULL OR p_operation_id IS NULL OR
       p_capability IS NULL OR p_capability !~ '^[a-z][a-z0-9.:/-]{0,127}$' OR p_input_sha256 IS NULL OR octet_length(p_input_sha256)<>32 OR
       p_attempt_id IS NULL OR p_now IS NULL OR p_lease_expires_at IS NULL OR p_lease_expires_at<=p_now OR p_lease_expires_at>p_now+interval '5 minutes' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner action begin';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT x.request_expires_at INTO invocation_expires_at FROM spyglass.runner_invocation_queue q
    JOIN spyglass.runner_invocation_exchanges x ON x.account_id=q.account_id AND x.invocation_id=q.invocation_id
    WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id AND q.processing_state IN ('launch_uncertain','launched')
      AND x.bound_pod_uid=p_pod_uid AND x.request_expires_at>p_now FOR SHARE OF q;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action identity or invocation state denied'; END IF;
    SELECT a.* INTO authorization_row FROM spyglass.runner_action_authorizations a
    WHERE a.account_id=p_account_id AND a.operation_id=p_operation_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P2001', MESSAGE='runner action approval is required'; END IF;
    IF authorization_row.invocation_id<>p_invocation_id OR authorization_row.capability<>p_capability OR authorization_row.input_sha256<>p_input_sha256 THEN
        RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action approval binding conflict';
    END IF;
    IF authorization_row.state<>'approved' THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='runner action approval was canceled'; END IF;
    IF authorization_row.expires_at<=p_now THEN RAISE EXCEPTION USING ERRCODE='P2002', MESSAGE='runner action approval expired'; END IF;
    effective_lease_expires_at:=LEAST(p_lease_expires_at,authorization_row.expires_at,invocation_expires_at);
    IF effective_lease_expires_at<=p_now THEN RAISE EXCEPTION USING ERRCODE='P2002', MESSAGE='runner action authority expired'; END IF;

    SELECT l.* INTO action_row FROM spyglass.runner_action_ledger l WHERE l.account_id=p_account_id AND l.operation_id=p_operation_id FOR UPDATE;
    action_exists:=FOUND;
    IF action_exists THEN
        SELECT e.* INTO executor_row FROM spyglass.runner_action_executors e
        WHERE e.capability=p_capability AND e.executor_id=action_row.executor_id AND e.executor_version=action_row.executor_version
          AND e.policy_version=action_row.executor_policy_version FOR SHARE;
    ELSE
        SELECT e.* INTO executor_row FROM spyglass.runner_action_executors e WHERE e.capability=p_capability AND e.enabled FOR SHARE;
    END IF;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='runner action executor is not registered'; END IF;
    IF NOT action_exists THEN
        IF NOT executor_row.enabled THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='runner action executor is disabled'; END IF;
        selected_mode:='execute';
        INSERT INTO spyglass.runner_action_ledger
            (account_id,operation_id,invocation_id,capability,input_sha256,idempotency_key,state,current_attempt_id,attempt_count,
             lease_expires_at,started_at,updated_at,executor_id,executor_version,executor_policy_version)
        VALUES (p_account_id,p_operation_id,p_invocation_id,p_capability,p_input_sha256,p_operation_id,'executing',p_attempt_id,1,
             effective_lease_expires_at,p_now,p_now,executor_row.executor_id,executor_row.executor_version,executor_row.policy_version);
    ELSE
        IF action_row.invocation_id<>p_invocation_id OR action_row.capability<>p_capability OR action_row.input_sha256<>p_input_sha256 OR
           action_row.idempotency_key<>p_operation_id OR action_row.executor_id<>executor_row.executor_id OR
           action_row.executor_version<>executor_row.executor_version OR action_row.executor_policy_version<>executor_row.policy_version THEN
            RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action ledger binding conflict';
        END IF;
        IF action_row.state IN ('failed','manual_resolution') THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='runner action requires explicit human resolution'; END IF;
        IF action_row.state IN ('executing','reconciling') AND action_row.lease_expires_at>p_now THEN RAISE EXCEPTION USING ERRCODE='P2003', MESSAGE='runner action is already leased'; END IF;
        IF action_row.state='retry_wait' AND action_row.next_attempt_at>p_now THEN RAISE EXCEPTION USING ERRCODE='P2003', MESSAGE='runner action retry is not due'; END IF;
        IF action_row.attempt_count>=1000 THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='runner action attempt limit reached'; END IF;
        IF action_row.state IN ('executing','reconciling') THEN
            previous_attempt:=action_row.current_attempt_id;
            UPDATE spyglass.runner_action_attempts AS prior SET outcome='unknown',error_code='lease_expired',completed_at=p_now
            WHERE prior.account_id=p_account_id AND prior.attempt_id=previous_attempt AND prior.outcome IS NULL;
        END IF;
        IF action_row.state='retry_wait' THEN
            IF NOT executor_row.enabled THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='runner action executor is disabled'; END IF;
            selected_mode:='execute';
        ELSE selected_mode:='reconcile'; END IF;
        UPDATE spyglass.runner_action_ledger AS ledger SET state=CASE WHEN selected_mode='execute' THEN 'executing' ELSE 'reconciling' END,
            current_attempt_id=p_attempt_id,attempt_count=ledger.attempt_count+1,last_error_code=NULL,lease_expires_at=effective_lease_expires_at,
            next_attempt_at=NULL,updated_at=p_now,completed_at=NULL
        WHERE ledger.account_id=p_account_id AND ledger.operation_id=p_operation_id;
    END IF;
    INSERT INTO spyglass.runner_action_attempts
        (account_id,operation_id,attempt_id,mode,started_at,lease_expires_at,executor_id,executor_version,executor_policy_version)
    VALUES (p_account_id,p_operation_id,p_attempt_id,selected_mode,p_now,effective_lease_expires_at,
        executor_row.executor_id,executor_row.executor_version,executor_row.policy_version);
    RETURN QUERY SELECT p_account_id,p_invocation_id,p_operation_id,p_attempt_id,p_capability,p_input_sha256,
        selected_mode,p_operation_id,effective_lease_expires_at;
END;
$$;

CREATE FUNCTION public.spyglass_complete_runner_action_v2(
    p_account_id uuid,p_invocation_id uuid,p_operation_id uuid,p_attempt_id uuid,p_capability text,p_input_sha256 bytea,
    p_mode text,p_idempotency_key uuid,p_lease_expires_at timestamptz,p_outcome text,p_error_code text,p_completed_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE action_row record; attempt_row record; executor_row record; retry_at timestamptz;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_operation_id IS NULL OR p_attempt_id IS NULL OR
       p_capability IS NULL OR p_capability !~ '^[a-z][a-z0-9.:/-]{0,127}$' OR p_input_sha256 IS NULL OR octet_length(p_input_sha256)<>32 OR
       p_mode NOT IN ('execute','reconcile') OR p_idempotency_key<>p_operation_id OR p_lease_expires_at IS NULL OR
       p_outcome NOT IN ('succeeded','failed','unknown') OR p_completed_at IS NULL OR
       (p_outcome='succeeded' AND p_error_code IS NOT NULL) OR
       (p_outcome IN ('failed','unknown') AND (p_error_code IS NULL OR p_error_code !~ '^[a-z][a-z0-9_]{0,99}$')) THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner action completion';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT l.* INTO action_row FROM spyglass.runner_action_ledger l WHERE l.account_id=p_account_id AND l.operation_id=p_operation_id FOR UPDATE;
    SELECT a.* INTO attempt_row FROM spyglass.runner_action_attempts a WHERE a.account_id=p_account_id AND a.attempt_id=p_attempt_id FOR UPDATE;
    IF NOT FOUND OR action_row.operation_id IS NULL OR action_row.invocation_id<>p_invocation_id OR action_row.capability<>p_capability OR
       action_row.input_sha256<>p_input_sha256 OR action_row.idempotency_key<>p_idempotency_key OR attempt_row.operation_id<>p_operation_id OR
       attempt_row.mode<>p_mode OR attempt_row.lease_expires_at<>p_lease_expires_at OR attempt_row.executor_id<>action_row.executor_id OR
       attempt_row.executor_version<>action_row.executor_version OR attempt_row.executor_policy_version<>action_row.executor_policy_version THEN
        RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action completion binding conflict';
    END IF;
    IF p_completed_at<attempt_row.started_at THEN RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='runner action completion predates attempt'; END IF;
    IF attempt_row.outcome IS NOT NULL THEN
        IF attempt_row.outcome=p_outcome AND attempt_row.error_code IS NOT DISTINCT FROM p_error_code THEN RETURN; END IF;
        RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action completion conflicts with settled attempt';
    END IF;
    IF action_row.current_attempt_id<>p_attempt_id OR action_row.state NOT IN ('executing','reconciling') THEN
        RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action completion is stale';
    END IF;
    UPDATE spyglass.runner_action_attempts SET outcome=p_outcome,error_code=p_error_code,completed_at=p_completed_at
    WHERE account_id=p_account_id AND attempt_id=p_attempt_id;
    IF p_mode='execute' AND p_outcome='failed' THEN
        SELECT e.* INTO executor_row FROM spyglass.runner_action_executors e
        WHERE e.capability=p_capability AND e.executor_id=action_row.executor_id AND e.executor_version=action_row.executor_version
          AND e.policy_version=action_row.executor_policy_version FOR SHARE;
        IF FOUND AND executor_row.enabled AND p_error_code=ANY(executor_row.retryable_error_codes)
           AND action_row.attempt_count<executor_row.max_definite_attempts THEN
            retry_at:=p_completed_at + LEAST(executor_row.retry_base_delay * power(2::numeric,(action_row.attempt_count-1)::numeric),interval '1 hour');
        END IF;
    END IF;
    IF retry_at IS NOT NULL THEN
        UPDATE spyglass.runner_action_ledger SET state='retry_wait',last_error_code=p_error_code,lease_expires_at=NULL,
            next_attempt_at=retry_at,updated_at=p_completed_at,completed_at=NULL WHERE account_id=p_account_id AND operation_id=p_operation_id;
    ELSE
        UPDATE spyglass.runner_action_ledger SET state=p_outcome,last_error_code=p_error_code,lease_expires_at=NULL,
            next_attempt_at=NULL,updated_at=p_completed_at,completed_at=p_completed_at WHERE account_id=p_account_id AND operation_id=p_operation_id;
    END IF;
END;
$$;

CREATE FUNCTION public.spyglass_request_runner_action_resolution(
    p_account_id uuid,p_operation_id uuid,p_resolution_id uuid,p_requested_outcome text,p_reason_sha256 bytea,
    p_requested_by_user_id uuid,p_requested_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE action_row record; existing record;
BEGIN
    IF p_account_id IS NULL OR p_operation_id IS NULL OR p_resolution_id IS NULL OR p_requested_outcome NOT IN ('succeeded','failed') OR
       p_reason_sha256 IS NULL OR octet_length(p_reason_sha256)<>32 OR p_requested_by_user_id IS NULL OR p_requested_at IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner action resolution request';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT r.* INTO existing FROM spyglass.runner_action_manual_resolutions r WHERE r.account_id=p_account_id AND r.resolution_id=p_resolution_id;
    IF FOUND THEN
        IF existing.operation_id=p_operation_id AND existing.requested_outcome=p_requested_outcome AND existing.reason_sha256=p_reason_sha256 AND
           existing.requested_by_user_id=p_requested_by_user_id AND existing.requested_at=p_requested_at THEN RETURN false; END IF;
        RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action resolution request conflicts';
    END IF;
    SELECT l.* INTO action_row FROM spyglass.runner_action_ledger l WHERE l.account_id=p_account_id AND l.operation_id=p_operation_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P2001', MESSAGE='runner action was not found'; END IF;
    IF action_row.state<>'unknown' THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='runner action is not eligible for manual resolution'; END IF;
    INSERT INTO spyglass.runner_action_manual_resolutions
        (account_id,resolution_id,operation_id,requested_outcome,reason_sha256,requested_by_user_id,requested_at,state)
    VALUES (p_account_id,p_resolution_id,p_operation_id,p_requested_outcome,p_reason_sha256,p_requested_by_user_id,p_requested_at,'pending');
    UPDATE spyglass.runner_action_ledger SET state='manual_resolution',last_error_code='manual_resolution_pending',updated_at=p_requested_at
    WHERE account_id=p_account_id AND operation_id=p_operation_id;
    RETURN true;
END;
$$;

CREATE FUNCTION public.spyglass_confirm_runner_action_resolution(
    p_account_id uuid,p_operation_id uuid,p_resolution_id uuid,p_confirmed_by_user_id uuid,p_confirmed_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE resolution_row record; action_row record;
BEGIN
    IF p_account_id IS NULL OR p_operation_id IS NULL OR p_resolution_id IS NULL OR p_confirmed_by_user_id IS NULL OR p_confirmed_at IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner action resolution confirmation';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT r.* INTO resolution_row FROM spyglass.runner_action_manual_resolutions r
    WHERE r.account_id=p_account_id AND r.resolution_id=p_resolution_id FOR UPDATE;
    IF NOT FOUND OR resolution_row.operation_id<>p_operation_id THEN RAISE EXCEPTION USING ERRCODE='P2001', MESSAGE='runner action resolution was not found'; END IF;
    IF resolution_row.state='applied' THEN
        IF resolution_row.confirmed_by_user_id=p_confirmed_by_user_id AND resolution_row.confirmed_at=p_confirmed_at THEN RETURN false; END IF;
        RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action resolution confirmation conflicts';
    END IF;
    IF resolution_row.requested_by_user_id=p_confirmed_by_user_id THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='runner action resolution requires a distinct confirmer'; END IF;
    IF p_confirmed_at<resolution_row.requested_at THEN RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='runner action resolution confirmation predates request'; END IF;
    SELECT l.* INTO action_row FROM spyglass.runner_action_ledger l WHERE l.account_id=p_account_id AND l.operation_id=p_operation_id FOR UPDATE;
    IF NOT FOUND OR action_row.state<>'manual_resolution' THEN RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action resolution ledger conflict'; END IF;
    UPDATE spyglass.runner_action_manual_resolutions SET state='applied',confirmed_by_user_id=p_confirmed_by_user_id,confirmed_at=p_confirmed_at
    WHERE account_id=p_account_id AND resolution_id=p_resolution_id;
    UPDATE spyglass.runner_action_ledger SET state=resolution_row.requested_outcome,
        last_error_code=CASE WHEN resolution_row.requested_outcome='succeeded' THEN NULL ELSE 'manual_resolution_failed' END,
        updated_at=p_confirmed_at,completed_at=p_confirmed_at WHERE account_id=p_account_id AND operation_id=p_operation_id;
    RETURN true;
END;
$$;

CREATE FUNCTION public.spyglass_runner_action_stats(p_now timestamptz)
RETURNS TABLE (executing bigint,reconciling bigint,retry_wait bigint,unknown bigint,manual_resolution bigint,failed bigint,succeeded bigint,oldest_retry_due_age_seconds bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
BEGIN
    IF p_now IS NULL THEN RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner action stats time'; END IF;
    RETURN QUERY SELECT count(*) FILTER (WHERE state='executing'),count(*) FILTER (WHERE state='reconciling'),
        count(*) FILTER (WHERE state='retry_wait'),count(*) FILTER (WHERE state='unknown'),count(*) FILTER (WHERE state='manual_resolution'),
        count(*) FILTER (WHERE state='failed'),count(*) FILTER (WHERE state='succeeded'),
        COALESCE(GREATEST(floor(extract(epoch FROM p_now-min(next_attempt_at)))::bigint,0),0)
    FROM spyglass.runner_action_ledger;
END;
$$;

CREATE FUNCTION spyglass.capture_runner_action_resolution_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts jsonb; current_count bigint;
BEGIN
    IF current_setting('spyglass.erasure_request_id',true)<>'' AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        counts:=COALESCE(NULLIF(current_setting('spyglass.runner_action_resolution_erasure_counts',true),'')::jsonb,'{}'::jsonb);
        current_count:=COALESCE((counts->>TG_TABLE_NAME)::bigint,0)+1;
        PERFORM set_config('spyglass.runner_action_resolution_erasure_counts',(counts||jsonb_build_object(TG_TABLE_NAME,current_count))::text,true);
    END IF;
    RETURN OLD;
END;
$$;
CREATE FUNCTION spyglass.add_runner_action_resolution_erasure_count() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.runner_action_resolution_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER runner_action_resolution_erasure_count BEFORE DELETE ON spyglass.runner_action_manual_resolutions
FOR EACH ROW EXECUTE FUNCTION spyglass.capture_runner_action_resolution_erasure_count();
CREATE TRIGGER account_erasure_runner_action_resolution_count BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_runner_action_resolution_erasure_count();

REVOKE ALL ON TABLE spyglass.runner_action_executors FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_begin_runner_action_v2(uuid,uuid,uuid,uuid,text,bytea,uuid,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_complete_runner_action_v2(uuid,uuid,uuid,uuid,text,bytea,text,uuid,timestamptz,text,text,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_request_runner_action_resolution(uuid,uuid,uuid,text,bytea,uuid,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_confirm_runner_action_resolution(uuid,uuid,uuid,uuid,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_runner_action_stats(timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.capture_runner_action_resolution_erasure_count() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.add_runner_action_resolution_erasure_count() FROM PUBLIC;

COMMIT;
