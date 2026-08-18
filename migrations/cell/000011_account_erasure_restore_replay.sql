BEGIN;

CREATE FUNCTION public.spyglass_replay_account_cell_erasure(
    p_request_id uuid,
    p_account_id uuid,
    p_restored_placement_generation bigint,
    p_tombstone_placement_generation bigint,
    p_account_fingerprint bytea,
    p_policy_version bigint,
    p_request_version bigint,
    p_environment text,
    p_erased_at timestamptz,
    p_export_sha256 bytea,
    p_operator_evidence_sha256 bytea,
    p_backup_expires_at timestamptz,
    p_previous_ledger_sequence bigint,
    p_previous_ledger_root bytea,
    p_expected_ledger_sequence bigint,
    p_expected_ledger_root bytea
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    existing spyglass.account_erasure_tombstones%ROWTYPE;
    namespace record;
    ledger spyglass.account_erasure_restore_ledger%ROWTYPE;
    receipt_count bigint; cleanup_count bigint; release_count bigint; work_event_count bigint; work_item_count bigint;
    counter_count bigint; audit_count bigint; operator_event_count bigint; namespace_count bigint;
    result spyglass.account_erasure_tombstones%ROWTYPE;
BEGIN
    IF p_request_id IS NULL OR p_account_id IS NULL OR p_restored_placement_generation IS NULL OR p_restored_placement_generation<=0 OR
       p_tombstone_placement_generation IS NULL OR p_tombstone_placement_generation<=0 OR
       p_account_fingerprint IS NULL OR octet_length(p_account_fingerprint)<>32 OR p_policy_version IS NULL OR p_policy_version<=0 OR
       p_request_version IS NULL OR p_request_version<=0 OR p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR
       p_erased_at IS NULL OR (p_export_sha256 IS NOT NULL AND octet_length(p_export_sha256)<>32) OR
       p_operator_evidence_sha256 IS NULL OR octet_length(p_operator_evidence_sha256)<>32 OR
       p_backup_expires_at IS NULL OR p_backup_expires_at<=p_erased_at OR
       p_previous_ledger_sequence IS NULL OR p_previous_ledger_sequence<0 OR p_previous_ledger_root IS NULL OR octet_length(p_previous_ledger_root)<>32 OR
       p_expected_ledger_sequence IS NULL OR p_expected_ledger_sequence<>p_previous_ledger_sequence+1 OR
       p_expected_ledger_root IS NULL OR octet_length(p_expected_ledger_root)<>32 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid cell Account erasure restore directive';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('spyglass:cell-erasure-restore-ledger',0));
    SELECT * INTO existing FROM spyglass.account_erasure_tombstones WHERE request_id=p_request_id;
    IF FOUND THEN
        IF existing.account_fingerprint<>p_account_fingerprint OR existing.placement_generation<>p_tombstone_placement_generation OR
           existing.policy_version<>p_policy_version OR existing.request_version<>p_request_version OR existing.environment<>p_environment OR
           existing.erased_at<>p_erased_at OR existing.export_sha256 IS DISTINCT FROM p_export_sha256 OR
           existing.operator_evidence_sha256<>p_operator_evidence_sha256 OR existing.backup_expires_at<>p_backup_expires_at OR
           existing.ledger_sequence<>p_expected_ledger_sequence OR existing.ledger_root<>p_expected_ledger_root THEN
            RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='cell Account erasure restore tombstone conflicts with directive';
        END IF;
        RETURN NEXT existing;
        RETURN;
    END IF;
    IF EXISTS (SELECT 1 FROM spyglass.account_erasure_tombstones WHERE account_fingerprint=p_account_fingerprint) THEN
        RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='cell Account erasure restore fingerprint conflicts with another request';
    END IF;
    SELECT * INTO ledger FROM spyglass.account_erasure_restore_ledger ORDER BY sequence DESC LIMIT 1;
    IF ledger.sequence<>p_previous_ledger_sequence OR ledger.root<>p_previous_ledger_root THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='cell Account erasure restore directive is out of order';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT placement_generation,state INTO namespace FROM spyglass.account_namespaces WHERE account_id=p_account_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='restored cell Account namespace is missing without tombstone';
    END IF;
    IF namespace.placement_generation<>p_restored_placement_generation THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='restored cell Account placement does not match directive invocation';
    END IF;
    PERFORM set_config('spyglass.erasure_request_id',p_request_id::text,true);
    PERFORM set_config('spyglass.erasure_account_id',p_account_id::text,true);

    DELETE FROM spyglass.route_context_receipts WHERE account_id=p_account_id; GET DIAGNOSTICS receipt_count=ROW_COUNT;
    DELETE FROM spyglass.route_context_receipt_cleanup_queue WHERE account_id=p_account_id; GET DIAGNOSTICS cleanup_count=ROW_COUNT;
    DELETE FROM spyglass.work_capacity_release_queue WHERE account_id=p_account_id; GET DIAGNOSTICS release_count=ROW_COUNT;
    DELETE FROM spyglass.work_item_events WHERE account_id=p_account_id; GET DIAGNOSTICS work_event_count=ROW_COUNT;
    DELETE FROM spyglass.work_items WHERE account_id=p_account_id; GET DIAGNOSTICS work_item_count=ROW_COUNT;
    DELETE FROM spyglass.work_item_number_counters WHERE account_id=p_account_id; GET DIAGNOSTICS counter_count=ROW_COUNT;
    DELETE FROM spyglass.account_audit_events WHERE account_id=p_account_id; GET DIAGNOSTICS audit_count=ROW_COUNT;
    DELETE FROM spyglass.work_capacity_release_operator_events WHERE account_id=p_account_id; GET DIAGNOSTICS operator_event_count=ROW_COUNT;
    DELETE FROM spyglass.account_namespaces WHERE account_id=p_account_id; GET DIAGNOSTICS namespace_count=ROW_COUNT;
    IF namespace_count<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='restored cell Account namespace deletion count is invalid';
    END IF;

    INSERT INTO spyglass.account_erasure_tombstones
        (request_id,account_fingerprint,placement_generation,policy_version,request_version,environment,erased_at,row_counts,
         export_sha256,operator_evidence_sha256,backup_expires_at)
    VALUES
        (p_request_id,p_account_fingerprint,p_tombstone_placement_generation,p_policy_version,p_request_version,p_environment,p_erased_at,
         jsonb_build_object('route_context_receipts',receipt_count,'route_context_receipt_cleanup_queue',cleanup_count,
            'work_capacity_release_queue',release_count,'work_item_events',work_event_count,'work_items',work_item_count,
            'work_item_number_counters',counter_count,'account_audit_events',audit_count,
            'work_capacity_release_operator_events',operator_event_count,'account_namespaces',namespace_count),
         p_export_sha256,p_operator_evidence_sha256,p_backup_expires_at)
    RETURNING * INTO result;
    IF result.ledger_sequence<>p_expected_ledger_sequence OR result.ledger_root<>p_expected_ledger_root THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='cell Account erasure restore checkpoint does not match directive';
    END IF;
    RETURN NEXT result;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;

COMMIT;
