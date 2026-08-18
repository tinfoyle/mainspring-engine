BEGIN;

CREATE OR REPLACE FUNCTION public.reject_account_erasure_operator_event_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text;
BEGIN
    SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner FROM pg_class c WHERE c.oid='public.account_erasure_tombstones'::regclass;
    IF TG_OP='DELETE' AND current_user=tombstone_owner AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text AND
       (current_setting('spyglass.erasure_request_id',true)=OLD.request_id::text OR current_setting('spyglass.erasure_restore_replay',true)='on') THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'Account erasure operator events are immutable';
END;
$$;

CREATE FUNCTION public.spyglass_resolve_account_erasure_restore(
    p_request_id uuid,
    p_account_id uuid,
    p_account_fingerprint bytea
) RETURNS TABLE (completed boolean, cell_id text, placement_generation bigint)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE existing public.account_erasure_tombstones%ROWTYPE;
BEGIN
    IF p_request_id IS NULL OR p_account_id IS NULL OR p_account_fingerprint IS NULL OR octet_length(p_account_fingerprint)<>32 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Account erasure restore target';
    END IF;
    SELECT * INTO existing FROM public.account_erasure_tombstones WHERE request_id=p_request_id;
    IF FOUND THEN
        IF existing.account_fingerprint<>p_account_fingerprint THEN
            RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='global Account erasure restore tombstone fingerprint mismatch';
        END IF;
        completed := true; cell_id := NULL; placement_generation := NULL; RETURN NEXT; RETURN;
    END IF;
    SELECT false,d.cell_id,d.placement_generation INTO completed,cell_id,placement_generation
    FROM public.accounts a JOIN public.account_directory d ON d.account_id=a.id WHERE a.id=p_account_id FOR SHARE OF a,d;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='restored global Account is missing without tombstone';
    END IF;
    RETURN NEXT;
END;
$$;

CREATE FUNCTION public.spyglass_replay_global_account_erasure(
    p_request_id uuid,
    p_account_id uuid,
    p_restored_cell_id text,
    p_restored_placement_generation bigint,
    p_account_fingerprint bytea,
    p_policy_version bigint,
    p_final_request_version bigint,
    p_environment text,
    p_prepared_at timestamptz,
    p_approved_at timestamptz,
    p_cell_erased_at timestamptz,
    p_completed_at timestamptz,
    p_export_sha256 bytea,
    p_cell_tombstone_sha256 bytea,
    p_operator_evidence_sha256 bytea,
    p_backup_expires_at timestamptz,
    p_previous_ledger_sequence bigint,
    p_previous_ledger_root bytea,
    p_expected_ledger_sequence bigint,
    p_expected_ledger_root bytea
) RETURNS SETOF public.account_erasure_tombstones
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    existing public.account_erasure_tombstones%ROWTYPE;
    restored record;
    ledger public.account_erasure_restore_ledger%ROWTYPE;
    notification_count bigint; inbox_count bigint; recompute_count bigint; reservation_count bigint; counter_count bigint;
    reconciliation_count bigint; checkout_count bigint; subscription_count bigint; profile_count bigint; snapshot_count bigint; grant_count bigint;
    invitation_count bigint; membership_event_count bigint; lifecycle_event_count bigint; operator_event_count bigint; closure_count bigint;
    membership_count bigint; cursor_count bigint; directory_count bigint; request_count bigint; account_count bigint; capacity_count bigint;
    result public.account_erasure_tombstones%ROWTYPE;
BEGIN
    IF p_request_id IS NULL OR p_account_id IS NULL OR p_restored_cell_id IS NULL OR btrim(p_restored_cell_id)='' OR
       p_restored_placement_generation IS NULL OR p_restored_placement_generation<=0 OR
       p_account_fingerprint IS NULL OR octet_length(p_account_fingerprint)<>32 OR p_policy_version IS NULL OR p_policy_version<=0 OR
       p_final_request_version IS NULL OR p_final_request_version<=0 OR p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR
       p_prepared_at IS NULL OR p_approved_at IS NULL OR p_approved_at<p_prepared_at OR
       p_cell_erased_at IS NULL OR p_cell_erased_at<p_approved_at OR p_completed_at IS NULL OR p_completed_at<p_cell_erased_at OR
       (p_export_sha256 IS NOT NULL AND octet_length(p_export_sha256)<>32) OR
       p_cell_tombstone_sha256 IS NULL OR octet_length(p_cell_tombstone_sha256)<>32 OR
       p_operator_evidence_sha256 IS NULL OR octet_length(p_operator_evidence_sha256)<>32 OR
       p_backup_expires_at IS NULL OR p_backup_expires_at<=p_completed_at OR
       p_previous_ledger_sequence IS NULL OR p_previous_ledger_sequence<0 OR p_previous_ledger_root IS NULL OR octet_length(p_previous_ledger_root)<>32 OR
       p_expected_ledger_sequence IS NULL OR p_expected_ledger_sequence<>p_previous_ledger_sequence+1 OR
       p_expected_ledger_root IS NULL OR octet_length(p_expected_ledger_root)<>32 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid global Account erasure restore directive';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('spyglass:account-erasure:' || p_account_id::text,0));
    PERFORM pg_advisory_xact_lock(hashtextextended('spyglass:global-erasure-restore-ledger',0));
    SELECT * INTO existing FROM public.account_erasure_tombstones WHERE request_id=p_request_id;
    IF FOUND THEN
        IF existing.account_fingerprint<>p_account_fingerprint OR existing.policy_version<>p_policy_version OR
           existing.final_request_version<>p_final_request_version OR existing.environment<>p_environment OR
           existing.prepared_at<>p_prepared_at OR existing.approved_at<>p_approved_at OR existing.cell_erased_at<>p_cell_erased_at OR
           existing.completed_at<>p_completed_at OR existing.export_sha256 IS DISTINCT FROM p_export_sha256 OR
           existing.cell_tombstone_sha256<>p_cell_tombstone_sha256 OR existing.operator_evidence_sha256<>p_operator_evidence_sha256 OR
           existing.backup_expires_at<>p_backup_expires_at OR existing.ledger_sequence<>p_expected_ledger_sequence OR existing.ledger_root<>p_expected_ledger_root THEN
            RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='global Account erasure restore tombstone conflicts with directive';
        END IF;
        RETURN NEXT existing; RETURN;
    END IF;
    IF EXISTS (SELECT 1 FROM public.account_erasure_tombstones WHERE account_fingerprint=p_account_fingerprint) THEN
        RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='global Account erasure restore fingerprint conflicts with another request';
    END IF;
    SELECT * INTO ledger FROM public.account_erasure_restore_ledger ORDER BY sequence DESC LIMIT 1;
    IF ledger.sequence<>p_previous_ledger_sequence OR ledger.root<>p_previous_ledger_root THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='global Account erasure restore directive is out of order';
    END IF;
    SELECT a.cell_id,a.placement_generation,d.cell_id AS directory_cell_id,d.placement_generation AS directory_generation
    INTO restored FROM public.accounts a JOIN public.account_directory d ON d.account_id=a.id WHERE a.id=p_account_id FOR UPDATE OF a,d;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='restored global Account is missing without tombstone';
    END IF;
    IF restored.cell_id<>p_restored_cell_id OR restored.directory_cell_id<>p_restored_cell_id OR
       restored.placement_generation<>p_restored_placement_generation OR restored.directory_generation<>p_restored_placement_generation THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='restored global Account placement changed during replay';
    END IF;
    PERFORM set_config('spyglass.erasure_request_id',p_request_id::text,true);
    PERFORM set_config('spyglass.erasure_account_id',p_account_id::text,true);
    PERFORM set_config('spyglass.erasure_restore_replay','on',true);

    DELETE FROM public.identity_notification_outbox WHERE account_id=p_account_id; GET DIAGNOSTICS notification_count=ROW_COUNT;
    DELETE FROM public.billing_event_inbox WHERE account_id=p_account_id; GET DIAGNOSTICS inbox_count=ROW_COUNT;
    DELETE FROM public.entitlement_recompute_queue WHERE account_id=p_account_id; GET DIAGNOSTICS recompute_count=ROW_COUNT;
    DELETE FROM public.entitlement_usage_reservations WHERE account_id=p_account_id; GET DIAGNOSTICS reservation_count=ROW_COUNT;
    DELETE FROM public.entitlement_usage_counters WHERE account_id=p_account_id; GET DIAGNOSTICS counter_count=ROW_COUNT;
    DELETE FROM public.billing_reconciliation_queue WHERE provider_subscription_id IN (SELECT provider_subscription_id FROM public.subscriptions WHERE account_id=p_account_id); GET DIAGNOSTICS reconciliation_count=ROW_COUNT;
    DELETE FROM public.billing_checkout_attempts WHERE account_id=p_account_id; GET DIAGNOSTICS checkout_count=ROW_COUNT;
    DELETE FROM public.subscriptions WHERE account_id=p_account_id; GET DIAGNOSTICS subscription_count=ROW_COUNT;
    DELETE FROM public.billing_profiles WHERE account_id=p_account_id; GET DIAGNOSTICS profile_count=ROW_COUNT;
    DELETE FROM public.entitlement_snapshots WHERE account_id=p_account_id; GET DIAGNOSTICS snapshot_count=ROW_COUNT;
    DELETE FROM public.entitlement_grants WHERE account_id=p_account_id; GET DIAGNOSTICS grant_count=ROW_COUNT;
    DELETE FROM public.invitations WHERE account_id=p_account_id; GET DIAGNOSTICS invitation_count=ROW_COUNT;
    DELETE FROM public.account_membership_events WHERE account_id=p_account_id; GET DIAGNOSTICS membership_event_count=ROW_COUNT;
    DELETE FROM public.account_lifecycle_events WHERE account_id=p_account_id; GET DIAGNOSTICS lifecycle_event_count=ROW_COUNT;
    DELETE FROM public.account_erasure_operator_events WHERE account_id=p_account_id; GET DIAGNOSTICS operator_event_count=ROW_COUNT;
    DELETE FROM public.account_erasure_requests WHERE account_id=p_account_id; GET DIAGNOSTICS request_count=ROW_COUNT;
    DELETE FROM public.account_closure_requests WHERE account_id=p_account_id; GET DIAGNOSTICS closure_count=ROW_COUNT;
    DELETE FROM public.memberships WHERE account_id=p_account_id; GET DIAGNOSTICS membership_count=ROW_COUNT;
    UPDATE public.entitlement_catalog_rollouts SET cursor_created_at=NULL,cursor_account_id=NULL WHERE cursor_account_id=p_account_id; GET DIAGNOSTICS cursor_count=ROW_COUNT;
    DELETE FROM public.account_directory WHERE account_id=p_account_id; GET DIAGNOSTICS directory_count=ROW_COUNT;
    UPDATE public.cells SET assigned_accounts=assigned_accounts-1 WHERE id=p_restored_cell_id AND assigned_accounts>0; GET DIAGNOSTICS capacity_count=ROW_COUNT;
    IF directory_count<>1 OR capacity_count<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='restored global Account invariant count is invalid';
    END IF;
    DELETE FROM public.accounts WHERE id=p_account_id; GET DIAGNOSTICS account_count=ROW_COUNT;
    IF account_count<>1 THEN RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='restored global Account deletion count is invalid'; END IF;

    INSERT INTO public.account_erasure_tombstones
        (request_id,account_fingerprint,policy_version,final_request_version,environment,prepared_at,approved_at,cell_erased_at,completed_at,
         cell_row_counts,global_row_counts,export_sha256,cell_tombstone_sha256,operator_evidence_sha256,backup_expires_at)
    VALUES
        (p_request_id,p_account_fingerprint,p_policy_version,p_final_request_version,p_environment,p_prepared_at,p_approved_at,p_cell_erased_at,p_completed_at,
         '{}'::jsonb,jsonb_build_object('identity_notification_outbox',notification_count,'billing_event_inbox',inbox_count,
            'entitlement_recompute_queue',recompute_count,'entitlement_usage_reservations',reservation_count,
            'entitlement_usage_counters',counter_count,'billing_reconciliation_queue',reconciliation_count,
            'billing_checkout_attempts',checkout_count,'subscriptions',subscription_count,'billing_profiles',profile_count,
            'entitlement_snapshots',snapshot_count,'entitlement_grants',grant_count,'invitations',invitation_count,
            'account_membership_events',membership_event_count,'account_lifecycle_events',lifecycle_event_count,
            'account_erasure_operator_events',operator_event_count,'account_erasure_requests',request_count,
            'account_closure_requests',closure_count,'memberships',membership_count,'entitlement_catalog_rollout_cursors',cursor_count,
            'account_directory',directory_count,'accounts',account_count,'cell_capacity',capacity_count),
         p_export_sha256,p_cell_tombstone_sha256,p_operator_evidence_sha256,p_backup_expires_at)
    RETURNING * INTO result;
    IF result.ledger_sequence<>p_expected_ledger_sequence OR result.ledger_root<>p_expected_ledger_root THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='global Account erasure restore checkpoint does not match directive';
    END IF;
    RETURN NEXT result;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_resolve_account_erasure_restore(uuid,uuid,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_global_account_erasure(uuid,uuid,text,bigint,bytea,bigint,bigint,text,timestamptz,timestamptz,timestamptz,timestamptz,bytea,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;

COMMIT;
