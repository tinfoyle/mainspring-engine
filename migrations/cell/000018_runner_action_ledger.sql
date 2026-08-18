BEGIN;

-- Attention owns the human-facing approval aggregate. This table is only its
-- immutable, exact-byte authorization projection for the runner boundary.
CREATE TABLE spyglass.runner_action_authorizations (
    account_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    approval_id uuid NOT NULL,
    capability text NOT NULL CHECK (capability ~ '^[a-z][a-z0-9.:/-]{0,127}$'),
    input_sha256 bytea NOT NULL CHECK (octet_length(input_sha256)=32),
    hash_version smallint NOT NULL CHECK (hash_version=1),
    evidence_sha256 bytea NOT NULL CHECK (octet_length(evidence_sha256)=32),
    proposer_kind text NOT NULL CHECK (proposer_kind IN ('user','workload')),
    proposer_id text NOT NULL CHECK (char_length(proposer_id) BETWEEN 1 AND 200),
    approved_by_user_id uuid NOT NULL,
    policy_version bigint NOT NULL CHECK (policy_version>0),
    state text NOT NULL CHECK (state IN ('approved','canceled')),
    approved_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    canceled_at timestamptz,
    PRIMARY KEY (account_id,operation_id),
    UNIQUE (account_id,approval_id),
    FOREIGN KEY (account_id,invocation_id)
        REFERENCES spyglass.runner_invocation_queue(account_id,invocation_id) ON DELETE CASCADE,
    CHECK (expires_at>approved_at),
    CHECK ((state='approved' AND canceled_at IS NULL) OR (state='canceled' AND canceled_at IS NOT NULL AND canceled_at>=approved_at))
);

CREATE TABLE spyglass.runner_action_ledger (
    account_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    capability text NOT NULL CHECK (capability ~ '^[a-z][a-z0-9.:/-]{0,127}$'),
    input_sha256 bytea NOT NULL CHECK (octet_length(input_sha256)=32),
    idempotency_key uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('executing','reconciling','succeeded','failed','unknown','manual_resolution')),
    current_attempt_id uuid NOT NULL,
    attempt_count integer NOT NULL CHECK (attempt_count BETWEEN 1 AND 1000),
    last_error_code text CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    lease_expires_at timestamptz,
    started_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    PRIMARY KEY (account_id,operation_id),
    UNIQUE (account_id,idempotency_key),
    FOREIGN KEY (account_id,operation_id)
        REFERENCES spyglass.runner_action_authorizations(account_id,operation_id) ON DELETE CASCADE,
    CHECK (idempotency_key=operation_id),
    CHECK ((state IN ('executing','reconciling') AND lease_expires_at IS NOT NULL AND completed_at IS NULL) OR
           (state IN ('succeeded','failed','unknown','manual_resolution') AND lease_expires_at IS NULL AND completed_at IS NOT NULL)),
    CHECK ((state='succeeded' AND last_error_code IS NULL) OR state<>'succeeded'),
    CHECK (completed_at IS NULL OR completed_at>=started_at)
);

CREATE TABLE spyglass.runner_action_attempts (
    account_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    mode text NOT NULL CHECK (mode IN ('execute','reconcile')),
    outcome text CHECK (outcome IS NULL OR outcome IN ('succeeded','failed','unknown')),
    error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    started_at timestamptz NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    completed_at timestamptz,
    PRIMARY KEY (account_id,attempt_id),
    FOREIGN KEY (account_id,operation_id)
        REFERENCES spyglass.runner_action_ledger(account_id,operation_id) ON DELETE CASCADE,
    CHECK (lease_expires_at>started_at),
    CHECK (completed_at IS NULL OR completed_at>=started_at),
    CHECK ((outcome IS NULL AND error_code IS NULL AND completed_at IS NULL) OR
           (outcome='succeeded' AND error_code IS NULL AND completed_at IS NOT NULL) OR
           (outcome IN ('failed','unknown') AND error_code IS NOT NULL AND completed_at IS NOT NULL))
);

CREATE INDEX runner_action_authorizations_expiry
    ON spyglass.runner_action_authorizations(account_id,expires_at,operation_id) WHERE state='approved';
CREATE INDEX runner_action_ledger_state
    ON spyglass.runner_action_ledger(account_id,state,updated_at,operation_id);
CREATE INDEX runner_action_attempts_operation
    ON spyglass.runner_action_attempts(account_id,operation_id,started_at,attempt_id);

ALTER TABLE spyglass.runner_action_authorizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.runner_action_authorizations FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.runner_action_ledger ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.runner_action_ledger FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.runner_action_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.runner_action_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY runner_action_authorizations_isolation ON spyglass.runner_action_authorizations
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY runner_action_ledger_isolation ON spyglass.runner_action_ledger
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY runner_action_attempts_isolation ON spyglass.runner_action_attempts
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION public.spyglass_record_runner_action_authorization(
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
    PERFORM 1 FROM spyglass.runner_invocation_exchanges x
    WHERE x.account_id=p_account_id AND x.invocation_id=p_invocation_id AND x.request_expires_at>=p_expires_at;
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
    SELECT * INTO existing FROM spyglass.runner_action_authorizations
    WHERE account_id=p_account_id AND operation_id=p_operation_id;
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

CREATE FUNCTION public.spyglass_cancel_runner_action_authorization(
    p_account_id uuid,p_operation_id uuid,p_approval_id uuid,p_canceled_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE changed bigint; existing record;
BEGIN
    IF p_account_id IS NULL OR p_operation_id IS NULL OR p_approval_id IS NULL OR p_canceled_at IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner action authorization cancellation';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT a.* INTO existing FROM spyglass.runner_action_authorizations a
    WHERE a.account_id=p_account_id AND a.operation_id=p_operation_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P2001', MESSAGE='runner action approval is required'; END IF;
    IF existing.approval_id<>p_approval_id THEN RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action approval binding conflict'; END IF;
    IF p_canceled_at<existing.approved_at THEN RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='runner action cancellation predates approval'; END IF;
	IF existing.state='canceled' THEN
		IF existing.canceled_at=p_canceled_at THEN RETURN false; END IF;
		RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action cancellation conflicts with existing binding';
	END IF;
    IF EXISTS (SELECT 1 FROM spyglass.runner_action_ledger l WHERE l.account_id=p_account_id AND l.operation_id=p_operation_id) THEN
        RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='runner action already entered execution';
    END IF;
    UPDATE spyglass.runner_action_authorizations SET state='canceled',canceled_at=p_canceled_at
    WHERE account_id=p_account_id AND operation_id=p_operation_id AND state='approved';
    GET DIAGNOSTICS changed=ROW_COUNT;
    IF changed=0 THEN RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action cancellation conflict'; END IF;
    RETURN changed=1;
END;
$$;

CREATE FUNCTION public.spyglass_begin_runner_action(
    p_account_id uuid,p_invocation_id uuid,p_pod_uid uuid,p_operation_id uuid,p_capability text,p_input_sha256 bytea,
    p_attempt_id uuid,p_now timestamptz,p_lease_expires_at timestamptz
) RETURNS TABLE (
    account_id uuid,invocation_id uuid,operation_id uuid,attempt_id uuid,capability text,input_sha256 bytea,
    mode text,idempotency_key uuid,lease_expires_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE authorization_row record; action_row record; selected_mode text; previous_attempt uuid; invocation_expires_at timestamptz; effective_lease_expires_at timestamptz;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_pod_uid IS NULL OR p_operation_id IS NULL OR
       p_capability IS NULL OR p_capability !~ '^[a-z][a-z0-9.:/-]{0,127}$' OR
       p_input_sha256 IS NULL OR octet_length(p_input_sha256)<>32 OR p_attempt_id IS NULL OR p_now IS NULL OR
       p_lease_expires_at IS NULL OR p_lease_expires_at<=p_now OR p_lease_expires_at>p_now+interval '5 minutes' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner action begin';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT x.request_expires_at INTO invocation_expires_at FROM spyglass.runner_invocation_queue q
    JOIN spyglass.runner_invocation_exchanges x ON x.account_id=q.account_id AND x.invocation_id=q.invocation_id
    WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id AND q.processing_state IN ('launch_uncertain','launched')
      AND x.bound_pod_uid=p_pod_uid AND x.request_expires_at>p_now
    FOR SHARE OF q;
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

    SELECT l.* INTO action_row FROM spyglass.runner_action_ledger l
    WHERE l.account_id=p_account_id AND l.operation_id=p_operation_id FOR UPDATE;
    IF NOT FOUND THEN
        selected_mode:='execute';
        INSERT INTO spyglass.runner_action_ledger
            (account_id,operation_id,invocation_id,capability,input_sha256,idempotency_key,state,current_attempt_id,
             attempt_count,lease_expires_at,started_at,updated_at)
        VALUES
            (p_account_id,p_operation_id,p_invocation_id,p_capability,p_input_sha256,p_operation_id,'executing',p_attempt_id,
             1,effective_lease_expires_at,p_now,p_now);
    ELSE
        IF action_row.invocation_id<>p_invocation_id OR action_row.capability<>p_capability OR action_row.input_sha256<>p_input_sha256 OR action_row.idempotency_key<>p_operation_id THEN
            RAISE EXCEPTION USING ERRCODE='P2005', MESSAGE='runner action ledger binding conflict';
        END IF;
        IF action_row.state IN ('failed','manual_resolution') THEN
            RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='runner action requires explicit human resolution';
        END IF;
        IF action_row.state IN ('executing','reconciling') AND action_row.lease_expires_at>p_now THEN
            RAISE EXCEPTION USING ERRCODE='P2003', MESSAGE='runner action is already leased';
        END IF;
        IF action_row.attempt_count>=1000 THEN RAISE EXCEPTION USING ERRCODE='P2004', MESSAGE='runner action attempt limit reached'; END IF;
        IF action_row.state IN ('executing','reconciling') THEN
            previous_attempt:=action_row.current_attempt_id;
            UPDATE spyglass.runner_action_attempts AS a SET outcome='unknown',error_code='lease_expired',completed_at=p_now
            WHERE a.account_id=p_account_id AND a.attempt_id=previous_attempt AND a.outcome IS NULL;
        END IF;
        selected_mode:='reconcile';
        UPDATE spyglass.runner_action_ledger AS l SET state='reconciling',current_attempt_id=p_attempt_id,
            attempt_count=l.attempt_count+1,last_error_code=NULL,lease_expires_at=effective_lease_expires_at,updated_at=p_now,completed_at=NULL
        WHERE l.account_id=p_account_id AND l.operation_id=p_operation_id;
    END IF;
    INSERT INTO spyglass.runner_action_attempts
        (account_id,operation_id,attempt_id,mode,started_at,lease_expires_at)
    VALUES (p_account_id,p_operation_id,p_attempt_id,selected_mode,p_now,effective_lease_expires_at);
    RETURN QUERY SELECT p_account_id,p_invocation_id,p_operation_id,p_attempt_id,p_capability,p_input_sha256,
        selected_mode,p_operation_id,effective_lease_expires_at;
END;
$$;

CREATE FUNCTION public.spyglass_complete_runner_action(
    p_account_id uuid,p_invocation_id uuid,p_operation_id uuid,p_attempt_id uuid,p_capability text,p_input_sha256 bytea,
    p_mode text,p_idempotency_key uuid,p_lease_expires_at timestamptz,p_outcome text,p_error_code text,p_completed_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE action_row record; attempt_row record;
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
    SELECT l.* INTO action_row FROM spyglass.runner_action_ledger l
    WHERE l.account_id=p_account_id AND l.operation_id=p_operation_id FOR UPDATE;
    SELECT a.* INTO attempt_row FROM spyglass.runner_action_attempts a
    WHERE a.account_id=p_account_id AND a.attempt_id=p_attempt_id FOR UPDATE;
    IF NOT FOUND OR action_row.operation_id IS NULL OR action_row.invocation_id<>p_invocation_id OR action_row.capability<>p_capability OR
       action_row.input_sha256<>p_input_sha256 OR action_row.idempotency_key<>p_idempotency_key OR
       attempt_row.operation_id<>p_operation_id OR attempt_row.mode<>p_mode OR attempt_row.lease_expires_at<>p_lease_expires_at THEN
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
    UPDATE spyglass.runner_action_ledger SET state=p_outcome,last_error_code=p_error_code,lease_expires_at=NULL,
        updated_at=p_completed_at,completed_at=p_completed_at
    WHERE account_id=p_account_id AND operation_id=p_operation_id;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_record_runner_action_authorization(uuid,uuid,uuid,uuid,text,bytea,smallint,bytea,text,text,uuid,bigint,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_cancel_runner_action_authorization(uuid,uuid,uuid,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_begin_runner_action(uuid,uuid,uuid,uuid,text,bytea,uuid,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_complete_runner_action(uuid,uuid,uuid,uuid,text,bytea,text,uuid,timestamptz,text,text,timestamptz) FROM PUBLIC;

-- Extend Account erasure/replay without changing their stable public ABI.
ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz)
    RENAME TO spyglass_erase_account_cell_without_runner_actions;
ALTER FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea)
    RENAME TO spyglass_replay_account_cell_erasure_without_runner_actions;

CREATE FUNCTION spyglass.add_runner_action_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.runner_action_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_erasure_runner_action_counts
BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_runner_action_erasure_counts();

CREATE FUNCTION public.spyglass_erase_account_cell(
    p_request_id uuid,p_account_id uuid,p_placement_generation bigint,p_account_fingerprint bytea,
    p_policy_version bigint,p_request_version bigint,p_environment text,p_export_sha256 bytea,
    p_operator_evidence_sha256 bytea,p_backup_expires_at timestamptz
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE authorization_count bigint; action_count bigint; attempt_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT count(*) INTO authorization_count FROM spyglass.runner_action_authorizations WHERE account_id=p_account_id;
    SELECT count(*) INTO action_count FROM spyglass.runner_action_ledger WHERE account_id=p_account_id;
    SELECT count(*) INTO attempt_count FROM spyglass.runner_action_attempts WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.runner_action_erasure_counts',jsonb_build_object(
        'runner_action_authorizations',authorization_count,'runner_action_ledger',action_count,'runner_action_attempts',attempt_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_erase_account_cell_without_runner_actions(
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
DECLARE authorization_count bigint; action_count bigint; attempt_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT count(*) INTO authorization_count FROM spyglass.runner_action_authorizations WHERE account_id=p_account_id;
    SELECT count(*) INTO action_count FROM spyglass.runner_action_ledger WHERE account_id=p_account_id;
    SELECT count(*) INTO attempt_count FROM spyglass.runner_action_attempts WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.runner_action_erasure_counts',jsonb_build_object(
        'runner_action_authorizations',authorization_count,'runner_action_ledger',action_count,'runner_action_attempts',attempt_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_replay_account_cell_erasure_without_runner_actions(
        p_request_id,p_account_id,p_restored_placement_generation,p_tombstone_placement_generation,p_account_fingerprint,
        p_policy_version,p_request_version,p_environment,p_erased_at,p_export_sha256,p_operator_evidence_sha256,
        p_backup_expires_at,p_previous_ledger_sequence,p_previous_ledger_root,p_expected_ledger_sequence,p_expected_ledger_root);
END;
$$;

REVOKE ALL ON FUNCTION spyglass.add_runner_action_erasure_counts() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell_without_runner_actions(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure_without_runner_actions(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;

COMMIT;
