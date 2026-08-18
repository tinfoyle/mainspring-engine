BEGIN;

ALTER TABLE spyglass.runner_invocation_queue
    ADD CONSTRAINT runner_invocation_account_identity UNIQUE (account_id, invocation_id);

-- Payload bytes are encrypted in the application before this Account-owned
-- table is reached. The technical scheduling queue remains identifier-only.
CREATE TABLE spyglass.runner_invocation_exchanges (
    account_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    request_ciphertext bytea NOT NULL CHECK (octet_length(request_ciphertext) BETWEEN 17 AND 1048592),
    request_nonce bytea NOT NULL CHECK (octet_length(request_nonce)=12),
    request_key_version integer NOT NULL CHECK (request_key_version>0),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest)=32),
    request_expires_at timestamptz NOT NULL,
    bound_pod_uid uuid,
    bound_at timestamptz,
    last_fetched_at timestamptz,
    fetch_count integer NOT NULL DEFAULT 0 CHECK (fetch_count BETWEEN 0 AND 1000),
    result_outcome text CHECK (result_outcome IS NULL OR result_outcome IN ('completed','execution_failed')),
    result_ciphertext bytea CHECK (result_ciphertext IS NULL OR octet_length(result_ciphertext) BETWEEN 17 AND 1048592),
    result_nonce bytea CHECK (result_nonce IS NULL OR octet_length(result_nonce)=12),
    result_key_version integer CHECK (result_key_version IS NULL OR result_key_version>0),
    result_digest bytea CHECK (result_digest IS NULL OR octet_length(result_digest)=32),
    result_submitted_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, invocation_id),
    FOREIGN KEY (account_id, invocation_id)
        REFERENCES spyglass.runner_invocation_queue (account_id, invocation_id) ON DELETE CASCADE,
    CHECK ((bound_pod_uid IS NULL AND bound_at IS NULL AND last_fetched_at IS NULL AND fetch_count=0) OR
           (bound_pod_uid IS NOT NULL AND bound_at IS NOT NULL AND last_fetched_at IS NOT NULL AND fetch_count>0)),
    CHECK ((result_outcome IS NULL AND result_ciphertext IS NULL AND result_nonce IS NULL AND
            result_key_version IS NULL AND result_digest IS NULL AND result_submitted_at IS NULL) OR
           (result_outcome IS NOT NULL AND result_ciphertext IS NOT NULL AND result_nonce IS NOT NULL AND
            result_key_version IS NOT NULL AND result_digest IS NOT NULL AND result_submitted_at IS NOT NULL))
);

ALTER TABLE spyglass.runner_invocation_exchanges ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.runner_invocation_exchanges FORCE ROW LEVEL SECURITY;
CREATE POLICY runner_invocation_exchanges_isolation ON spyglass.runner_invocation_exchanges
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION public.spyglass_provision_runner_invocation(
    p_invocation_id uuid, p_account_id uuid, p_profile text, p_queued_at timestamptz,
    p_request_ciphertext bytea, p_request_nonce bytea, p_request_key_version integer,
    p_request_digest bytea, p_request_expires_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE
    queue_created boolean;
    existing record;
BEGIN
    IF p_request_ciphertext IS NULL OR octet_length(p_request_ciphertext) NOT BETWEEN 17 AND 1048592 OR
       p_request_nonce IS NULL OR octet_length(p_request_nonce)<>12 OR
       p_request_key_version IS NULL OR p_request_key_version<=0 OR
       p_request_digest IS NULL OR octet_length(p_request_digest)<>32 OR
       p_request_expires_at IS NULL OR p_request_expires_at<=p_queued_at OR
       p_request_expires_at>p_queued_at+interval '24 hours' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid encrypted runner request';
    END IF;
    queue_created := public.spyglass_enqueue_runner_invocation(p_invocation_id,p_account_id,p_profile,p_queued_at);
    PERFORM set_config('app.account_id',p_account_id::text,true);
    IF queue_created THEN
        INSERT INTO spyglass.runner_invocation_exchanges
            (account_id,invocation_id,request_ciphertext,request_nonce,request_key_version,request_digest,request_expires_at,created_at)
        VALUES
            (p_account_id,p_invocation_id,p_request_ciphertext,p_request_nonce,p_request_key_version,p_request_digest,p_request_expires_at,p_queued_at);
        RETURN true;
    END IF;
    SELECT request_digest,request_expires_at INTO existing
    FROM spyglass.runner_invocation_exchanges
    WHERE account_id=p_account_id AND invocation_id=p_invocation_id;
    IF NOT FOUND OR existing.request_digest<>p_request_digest OR existing.request_expires_at<>p_request_expires_at THEN
        RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='runner exchange conflicts with an existing record';
    END IF;
    RETURN false;
END;
$$;

CREATE FUNCTION public.spyglass_claim_runner_exchange(
    p_invocation_id uuid, p_pod_uid uuid, p_profile text, p_job_name text, p_now timestamptz
) RETURNS TABLE (
    invocation_id uuid, account_id uuid, profile text, request_ciphertext bytea,
    request_nonce bytea, request_key_version integer, request_digest bytea,
    created_at timestamptz, request_expires_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE
    target record;
    exchange_row record;
BEGIN
    IF p_invocation_id IS NULL OR p_pod_uid IS NULL OR p_profile IS NULL OR p_job_name IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner exchange claim';
    END IF;
    SELECT q.account_id,q.profile,q.job_name,q.processing_state INTO target
    FROM spyglass.runner_invocation_queue q WHERE q.invocation_id=p_invocation_id FOR SHARE;
    IF NOT FOUND OR target.profile<>p_profile THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='runner exchange identity denied';
    END IF;
    IF target.processing_state IN ('canceling','canceled') THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='runner exchange canceled';
    ELSIF target.processing_state NOT IN ('launch_uncertain','launched') THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='runner exchange not ready';
    END IF;
    IF target.job_name IS DISTINCT FROM p_job_name THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='runner exchange identity denied';
    END IF;
    PERFORM set_config('app.account_id',target.account_id::text,true);
    SELECT x.* INTO exchange_row FROM spyglass.runner_invocation_exchanges x
    WHERE x.account_id=target.account_id AND x.invocation_id=p_invocation_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='runner exchange identity denied';
    END IF;
    IF exchange_row.request_expires_at<=p_now THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='runner exchange expired';
    END IF;
    IF exchange_row.bound_pod_uid IS NOT NULL AND exchange_row.bound_pod_uid<>p_pod_uid THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='runner exchange Pod conflict';
    END IF;
    IF exchange_row.fetch_count>=1000 THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='runner exchange fetch limit reached';
    END IF;
    UPDATE spyglass.runner_invocation_exchanges x SET
        bound_pod_uid=COALESCE(x.bound_pod_uid,p_pod_uid),
        bound_at=COALESCE(x.bound_at,p_now), last_fetched_at=p_now, fetch_count=x.fetch_count+1
    WHERE x.account_id=target.account_id AND x.invocation_id=p_invocation_id;
    RETURN QUERY SELECT p_invocation_id,target.account_id,target.profile,
        exchange_row.request_ciphertext,exchange_row.request_nonce,exchange_row.request_key_version,
        exchange_row.request_digest,exchange_row.created_at,exchange_row.request_expires_at;
END;
$$;

CREATE FUNCTION public.spyglass_submit_runner_result(
    p_invocation_id uuid, p_pod_uid uuid, p_profile text, p_job_name text,
    p_outcome text, p_ciphertext bytea, p_nonce bytea, p_key_version integer,
    p_digest bytea, p_now timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE
    target record;
    exchange_row record;
BEGIN
    IF p_invocation_id IS NULL OR p_pod_uid IS NULL OR p_profile IS NULL OR p_job_name IS NULL OR
       p_outcome NOT IN ('completed','execution_failed') OR p_ciphertext IS NULL OR
       octet_length(p_ciphertext) NOT BETWEEN 17 AND 1048592 OR p_nonce IS NULL OR octet_length(p_nonce)<>12 OR
       p_key_version IS NULL OR p_key_version<=0 OR p_digest IS NULL OR octet_length(p_digest)<>32 OR p_now IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid encrypted runner result';
    END IF;
    SELECT q.account_id,q.profile,q.job_name,q.processing_state INTO target
    FROM spyglass.runner_invocation_queue q WHERE q.invocation_id=p_invocation_id FOR SHARE;
    IF NOT FOUND OR target.profile<>p_profile THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='runner exchange identity denied';
    END IF;
    IF target.processing_state IN ('canceling','canceled') THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='runner exchange canceled';
    ELSIF target.processing_state NOT IN ('launch_uncertain','launched') THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='runner exchange not ready';
    END IF;
    IF target.job_name IS DISTINCT FROM p_job_name THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='runner exchange identity denied';
    END IF;
    PERFORM set_config('app.account_id',target.account_id::text,true);
    SELECT x.* INTO exchange_row FROM spyglass.runner_invocation_exchanges x
    WHERE x.account_id=target.account_id AND x.invocation_id=p_invocation_id FOR UPDATE;
    IF NOT FOUND OR exchange_row.bound_pod_uid IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='runner exchange identity denied';
    END IF;
    IF exchange_row.request_expires_at<=p_now THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='runner exchange expired';
    END IF;
    IF exchange_row.bound_pod_uid<>p_pod_uid THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='runner exchange Pod conflict';
    END IF;
    IF exchange_row.result_digest IS NULL THEN
        UPDATE spyglass.runner_invocation_exchanges x SET
            result_outcome=p_outcome,result_ciphertext=p_ciphertext,result_nonce=p_nonce,
            result_key_version=p_key_version,result_digest=p_digest,result_submitted_at=p_now
        WHERE x.account_id=target.account_id AND x.invocation_id=p_invocation_id;
        RETURN true;
    END IF;
    IF exchange_row.result_digest<>p_digest OR exchange_row.result_outcome<>p_outcome THEN
        RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='runner exchange conflicts with an existing record';
    END IF;
    RETURN false;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_provision_runner_invocation(uuid,uuid,text,timestamptz,bytea,bytea,integer,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_runner_exchange(uuid,uuid,text,text,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_submit_runner_result(uuid,uuid,text,text,text,bytea,bytea,integer,bytea,timestamptz) FROM PUBLIC;

-- Extend the existing erasure wrapper without changing its stable public ABI.
ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz)
    RENAME TO spyglass_erase_account_cell_without_runner_exchange;
ALTER FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea)
    RENAME TO spyglass_replay_account_cell_erasure_without_runner_exchange;

CREATE FUNCTION spyglass.add_runner_exchange_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.runner_exchange_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_erasure_runner_exchange_counts
BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_runner_exchange_erasure_counts();

CREATE FUNCTION public.spyglass_erase_account_cell(
    p_request_id uuid,p_account_id uuid,p_placement_generation bigint,p_account_fingerprint bytea,
    p_policy_version bigint,p_request_version bigint,p_environment text,p_export_sha256 bytea,
    p_operator_evidence_sha256 bytea,p_backup_expires_at timestamptz
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE exchange_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT count(*) INTO exchange_count FROM spyglass.runner_invocation_exchanges WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.runner_exchange_erasure_counts',jsonb_build_object('runner_invocation_exchanges',exchange_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_erase_account_cell_without_runner_exchange(
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
DECLARE exchange_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT count(*) INTO exchange_count FROM spyglass.runner_invocation_exchanges WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.runner_exchange_erasure_counts',jsonb_build_object('runner_invocation_exchanges',exchange_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_replay_account_cell_erasure_without_runner_exchange(
        p_request_id,p_account_id,p_restored_placement_generation,p_tombstone_placement_generation,p_account_fingerprint,
        p_policy_version,p_request_version,p_environment,p_erased_at,p_export_sha256,p_operator_evidence_sha256,
        p_backup_expires_at,p_previous_ledger_sequence,p_previous_ledger_root,p_expected_ledger_sequence,p_expected_ledger_root);
END;
$$;

REVOKE ALL ON FUNCTION spyglass.add_runner_exchange_erasure_counts() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell_without_runner_exchange(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure_without_runner_exchange(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;

COMMIT;
