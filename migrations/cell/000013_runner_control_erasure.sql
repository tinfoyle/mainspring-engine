BEGIN;

-- Preserve the already-shipped erasure implementations behind internal names.
-- The public wrappers below fence and remove runner-control state before those
-- implementations remove the remaining Account rows and create the tombstone.
ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz)
    RENAME TO spyglass_erase_account_cell_without_runner_control;
ALTER FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea)
    RENAME TO spyglass_replay_account_cell_erasure_without_runner_control;

-- PostgreSQL orders triggers of the same kind by name. This trigger runs before
-- account_erasure_tombstone_checkpoint, so runner counts are part of the
-- immutable tombstone before the restore-ledger root is derived.
CREATE FUNCTION spyglass.add_runner_control_erasure_counts() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, spyglass
AS $$
DECLARE
    counts text;
BEGIN
    counts := current_setting('spyglass.runner_control_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN
        NEW.row_counts := NEW.row_counts || counts::jsonb;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_erasure_runner_control_counts
BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_runner_control_erasure_counts();

CREATE OR REPLACE FUNCTION public.spyglass_attest_account_erasure_readiness(
    p_account_id uuid,
    p_placement_generation bigint
) RETURNS TABLE (
    placement_generation bigint,
    namespace_state text,
    unfinished_release_count bigint,
    observed_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    target record;
    unfinished_work_releases bigint;
    unfinished_runners bigint;
BEGIN
    IF p_account_id IS NULL OR p_placement_generation IS NULL OR p_placement_generation <= 0 THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid Account erasure readiness target';
    END IF;
    SELECT n.placement_generation,n.state INTO target
    FROM spyglass.account_namespaces n
    WHERE n.account_id=p_account_id FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = 'P0002', MESSAGE = 'Account namespace not found';
    END IF;
    IF target.placement_generation<>p_placement_generation OR target.state NOT IN ('frozen','disabled') THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'Account namespace is not ready for erasure preparation';
    END IF;
    SELECT count(*) INTO unfinished_work_releases
    FROM spyglass.work_capacity_release_queue q
    WHERE q.account_id=p_account_id AND q.processing_state<>'completed';
    SELECT count(*) INTO unfinished_runners
    FROM spyglass.runner_invocation_queue q
    WHERE q.account_id=p_account_id AND q.processing_state NOT IN ('completed','execution_failed','canceled');
    placement_generation := target.placement_generation;
    namespace_state := target.state;
    -- Keep the stable result contract while extending its meaning to all
    -- unfinished cell-control work.
    unfinished_release_count := unfinished_work_releases+unfinished_runners;
    observed_at := statement_timestamp();
    RETURN NEXT;
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
    invocation_count bigint;
    scheduling_count bigint;
BEGIN
    IF EXISTS (
        SELECT 1 FROM spyglass.runner_invocation_queue q
        WHERE q.account_id=p_account_id
          AND q.processing_state NOT IN ('completed','execution_failed','canceled')
    ) THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='cell Account has unfinished runner invocations';
    END IF;
    DELETE FROM spyglass.runner_invocation_queue WHERE account_id=p_account_id;
    GET DIAGNOSTICS invocation_count=ROW_COUNT;
    DELETE FROM spyglass.runner_account_scheduling WHERE account_id=p_account_id;
    GET DIAGNOSTICS scheduling_count=ROW_COUNT;
    PERFORM set_config('spyglass.runner_control_erasure_counts',jsonb_build_object(
        'runner_invocation_queue',invocation_count,
        'runner_account_scheduling',scheduling_count
    )::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_erase_account_cell_without_runner_control(
        p_request_id,p_account_id,p_placement_generation,p_account_fingerprint,p_policy_version,
        p_request_version,p_environment,p_export_sha256,p_operator_evidence_sha256,p_backup_expires_at
    );
END;
$$;

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
    invocation_count bigint;
    scheduling_count bigint;
BEGIN
    DELETE FROM spyglass.runner_invocation_queue WHERE account_id=p_account_id;
    GET DIAGNOSTICS invocation_count=ROW_COUNT;
    DELETE FROM spyglass.runner_account_scheduling WHERE account_id=p_account_id;
    GET DIAGNOSTICS scheduling_count=ROW_COUNT;
    PERFORM set_config('spyglass.runner_control_erasure_counts',jsonb_build_object(
        'runner_invocation_queue',invocation_count,
        'runner_account_scheduling',scheduling_count
    )::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_replay_account_cell_erasure_without_runner_control(
        p_request_id,p_account_id,p_restored_placement_generation,p_tombstone_placement_generation,
        p_account_fingerprint,p_policy_version,p_request_version,p_environment,p_erased_at,p_export_sha256,
        p_operator_evidence_sha256,p_backup_expires_at,p_previous_ledger_sequence,p_previous_ledger_root,
        p_expected_ledger_sequence,p_expected_ledger_root
    );
END;
$$;

REVOKE ALL ON FUNCTION spyglass.add_runner_control_erasure_counts() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell_without_runner_control(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure_without_runner_control(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;

COMMIT;
