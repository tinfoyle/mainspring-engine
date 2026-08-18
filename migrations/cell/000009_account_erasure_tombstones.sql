BEGIN;

CREATE TABLE spyglass.account_erasure_tombstones (
    request_id uuid PRIMARY KEY,
    account_fingerprint bytea NOT NULL UNIQUE CHECK (octet_length(account_fingerprint)=32),
    placement_generation bigint NOT NULL CHECK (placement_generation>0),
    policy_version bigint NOT NULL CHECK (policy_version>0),
    request_version bigint NOT NULL CHECK (request_version>0),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    erased_at timestamptz NOT NULL,
    row_counts jsonb NOT NULL CHECK (jsonb_typeof(row_counts)='object'),
    export_sha256 bytea CHECK (export_sha256 IS NULL OR octet_length(export_sha256)=32),
    operator_evidence_sha256 bytea NOT NULL CHECK (octet_length(operator_evidence_sha256)=32),
    backup_expires_at timestamptz NOT NULL CHECK (backup_expires_at>erased_at)
);
CREATE INDEX account_erasure_tombstones_erased_at
    ON spyglass.account_erasure_tombstones (erased_at,request_id);

CREATE OR REPLACE FUNCTION spyglass.reject_work_capacity_release_operator_event_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    tombstone_owner text;
BEGIN
    SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner
    FROM pg_class c WHERE c.oid='spyglass.account_erasure_tombstones'::regclass;
    IF TG_OP='DELETE' AND OLD.account_id IS NOT NULL AND
       current_user=tombstone_owner AND
       current_setting('spyglass.erasure_request_id',true) IS NOT NULL AND
       current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'Work capacity release operator events are immutable';
END;
$$;

CREATE FUNCTION public.spyglass_erase_account_cell(
    p_request_id uuid,
    p_account_id uuid,
    p_placement_generation bigint,
    p_account_fingerprint bytea,
    p_policy_version bigint,
    p_request_version bigint,
    p_environment text,
    p_export_sha256 bytea,
    p_operator_evidence_sha256 bytea,
    p_backup_expires_at timestamptz
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    existing spyglass.account_erasure_tombstones%ROWTYPE;
    namespace record;
    receipt_count bigint;
    cleanup_count bigint;
    release_count bigint;
    work_event_count bigint;
    work_item_count bigint;
    counter_count bigint;
    audit_count bigint;
    operator_event_count bigint;
    namespace_count bigint;
    result spyglass.account_erasure_tombstones%ROWTYPE;
BEGIN
    IF p_request_id IS NULL OR p_account_id IS NULL OR p_placement_generation IS NULL OR p_placement_generation<=0 OR
       p_account_fingerprint IS NULL OR octet_length(p_account_fingerprint)<>32 OR
       p_policy_version IS NULL OR p_policy_version<=0 OR p_request_version IS NULL OR p_request_version<=0 OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR
       (p_export_sha256 IS NOT NULL AND octet_length(p_export_sha256)<>32) OR
       p_operator_evidence_sha256 IS NULL OR octet_length(p_operator_evidence_sha256)<>32 OR
       p_backup_expires_at IS NULL OR p_backup_expires_at<=statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid cell Account erasure invocation';
    END IF;

    SELECT * INTO existing FROM spyglass.account_erasure_tombstones WHERE request_id=p_request_id;
    IF FOUND THEN
        IF existing.account_fingerprint<>p_account_fingerprint OR existing.placement_generation<>p_placement_generation OR
           existing.policy_version<>p_policy_version OR existing.request_version<>p_request_version OR
           existing.environment<>p_environment OR existing.export_sha256 IS DISTINCT FROM p_export_sha256 OR
           existing.operator_evidence_sha256<>p_operator_evidence_sha256 OR existing.backup_expires_at<>p_backup_expires_at THEN
            RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='cell Account erasure tombstone conflicts with invocation';
        END IF;
        RETURN NEXT existing;
        RETURN;
    END IF;
    IF EXISTS (SELECT 1 FROM spyglass.account_erasure_tombstones WHERE account_fingerprint=p_account_fingerprint) THEN
        RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='cell Account erasure fingerprint already completed under another request';
    END IF;

    -- Forced RLS remains active. This transaction-local scope permits the
    -- function to see and remove only the exact Account supplied above.
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT n.placement_generation,n.state INTO namespace
    FROM spyglass.account_namespaces n WHERE n.account_id=p_account_id FOR UPDATE;
    IF NOT FOUND THEN
        -- A concurrent identical invocation may have committed while this
        -- statement waited on the namespace lock. Re-read the tombstone in a
        -- new statement snapshot before classifying the namespace as missing.
        SELECT * INTO existing FROM spyglass.account_erasure_tombstones WHERE request_id=p_request_id;
        IF FOUND THEN
            IF existing.account_fingerprint<>p_account_fingerprint OR existing.placement_generation<>p_placement_generation OR
               existing.policy_version<>p_policy_version OR existing.request_version<>p_request_version OR
               existing.environment<>p_environment OR existing.export_sha256 IS DISTINCT FROM p_export_sha256 OR
               existing.operator_evidence_sha256<>p_operator_evidence_sha256 OR existing.backup_expires_at<>p_backup_expires_at THEN
                RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='cell Account erasure tombstone conflicts with invocation';
            END IF;
            RETURN NEXT existing;
            RETURN;
        END IF;
        IF EXISTS (SELECT 1 FROM spyglass.account_erasure_tombstones WHERE account_fingerprint=p_account_fingerprint) THEN
            RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='cell Account erasure fingerprint already completed under another request';
        END IF;
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='cell Account namespace missing without tombstone';
    END IF;
    IF namespace.placement_generation<>p_placement_generation OR namespace.state NOT IN ('frozen','disabled') OR
       EXISTS (SELECT 1 FROM spyglass.work_capacity_release_queue q WHERE q.account_id=p_account_id AND q.processing_state<>'completed') THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='cell Account is not eligible for erasure';
    END IF;

    PERFORM set_config('spyglass.erasure_request_id',p_request_id::text,true);
    PERFORM set_config('spyglass.erasure_account_id',p_account_id::text,true);

    DELETE FROM spyglass.route_context_receipts WHERE account_id=p_account_id;
    GET DIAGNOSTICS receipt_count=ROW_COUNT;
    DELETE FROM spyglass.route_context_receipt_cleanup_queue WHERE account_id=p_account_id;
    GET DIAGNOSTICS cleanup_count=ROW_COUNT;
    DELETE FROM spyglass.work_capacity_release_queue WHERE account_id=p_account_id;
    GET DIAGNOSTICS release_count=ROW_COUNT;
    DELETE FROM spyglass.work_item_events WHERE account_id=p_account_id;
    GET DIAGNOSTICS work_event_count=ROW_COUNT;
    DELETE FROM spyglass.work_items WHERE account_id=p_account_id;
    GET DIAGNOSTICS work_item_count=ROW_COUNT;
    DELETE FROM spyglass.work_item_number_counters WHERE account_id=p_account_id;
    GET DIAGNOSTICS counter_count=ROW_COUNT;
    DELETE FROM spyglass.account_audit_events WHERE account_id=p_account_id;
    GET DIAGNOSTICS audit_count=ROW_COUNT;
    DELETE FROM spyglass.work_capacity_release_operator_events WHERE account_id=p_account_id;
    GET DIAGNOSTICS operator_event_count=ROW_COUNT;
    DELETE FROM spyglass.account_namespaces WHERE account_id=p_account_id;
    GET DIAGNOSTICS namespace_count=ROW_COUNT;
    IF namespace_count<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='cell Account namespace erasure count is invalid';
    END IF;

    INSERT INTO spyglass.account_erasure_tombstones
        (request_id,account_fingerprint,placement_generation,policy_version,request_version,environment,erased_at,
         row_counts,export_sha256,operator_evidence_sha256,backup_expires_at)
    VALUES
        (p_request_id,p_account_fingerprint,p_placement_generation,p_policy_version,p_request_version,p_environment,
         statement_timestamp(),jsonb_build_object(
            'route_context_receipts',receipt_count,
            'route_context_receipt_cleanup_queue',cleanup_count,
            'work_capacity_release_queue',release_count,
            'work_item_events',work_event_count,
            'work_items',work_item_count,
            'work_item_number_counters',counter_count,
            'account_audit_events',audit_count,
            'work_capacity_release_operator_events',operator_event_count,
            'account_namespaces',namespace_count),
         p_export_sha256,p_operator_evidence_sha256,p_backup_expires_at)
    RETURNING * INTO result;
    RETURN NEXT result;
END;
$$;

CREATE FUNCTION public.spyglass_attest_account_cell_erasure(
    p_request_id uuid,
    p_account_fingerprint bytea
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    result spyglass.account_erasure_tombstones%ROWTYPE;
BEGIN
    IF p_request_id IS NULL OR p_account_fingerprint IS NULL OR octet_length(p_account_fingerprint)<>32 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid cell Account erasure attestation';
    END IF;
    SELECT * INTO result FROM spyglass.account_erasure_tombstones WHERE request_id=p_request_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='cell Account erasure tombstone not found';
    END IF;
    IF result.account_fingerprint<>p_account_fingerprint THEN
        RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='cell Account erasure tombstone fingerprint mismatch';
    END IF;
    RETURN NEXT result;
END;
$$;

REVOKE ALL ON TABLE spyglass.account_erasure_tombstones FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_attest_account_cell_erasure(uuid,bytea) FROM PUBLIC;

COMMIT;
