BEGIN;

ALTER TABLE public.account_erasure_requests
    DROP CONSTRAINT account_erasure_requests_state_check,
    DROP CONSTRAINT account_erasure_state_shape;
ALTER TABLE public.account_erasure_requests
    ADD COLUMN account_fingerprint bytea CHECK (account_fingerprint IS NULL OR octet_length(account_fingerprint)=32),
    ADD COLUMN operator_evidence_sha256 bytea CHECK (operator_evidence_sha256 IS NULL OR octet_length(operator_evidence_sha256)=32),
    ADD COLUMN cell_request_version bigint CHECK (cell_request_version IS NULL OR cell_request_version>0),
    ADD COLUMN execution_lease_id uuid,
    ADD COLUMN lease_expires_at timestamptz,
    ADD COLUMN cell_erased_at timestamptz,
    ADD COLUMN cell_row_counts jsonb CHECK (cell_row_counts IS NULL OR jsonb_typeof(cell_row_counts)='object'),
    ADD COLUMN cell_tombstone_sha256 bytea CHECK (cell_tombstone_sha256 IS NULL OR octet_length(cell_tombstone_sha256)=32),
    ADD CONSTRAINT account_erasure_requests_state_check CHECK (state IN ('prepared','approved','canceled','cell_erasing','cell_erased','global_erasing')),
    ADD CONSTRAINT account_erasure_state_shape CHECK (
        (state='prepared' AND approved_by IS NULL AND approve_reason IS NULL AND approved_at IS NULL AND canceled_by IS NULL AND cancel_reason IS NULL AND canceled_at IS NULL AND account_fingerprint IS NULL AND execution_lease_id IS NULL AND cell_erased_at IS NULL)
        OR
        (state='approved' AND approved_by IS NOT NULL AND approve_reason IS NOT NULL AND approved_at IS NOT NULL AND canceled_by IS NULL AND cancel_reason IS NULL AND canceled_at IS NULL AND account_fingerprint IS NULL AND execution_lease_id IS NULL AND cell_erased_at IS NULL)
        OR
        (state='canceled' AND canceled_by IS NOT NULL AND cancel_reason IS NOT NULL AND canceled_at IS NOT NULL AND account_fingerprint IS NULL AND execution_lease_id IS NULL AND cell_erased_at IS NULL)
        OR
        (state='cell_erasing' AND approved_at IS NOT NULL AND account_fingerprint IS NOT NULL AND operator_evidence_sha256 IS NOT NULL AND cell_request_version IS NOT NULL AND execution_lease_id IS NOT NULL AND lease_expires_at IS NOT NULL AND cell_erased_at IS NULL AND cell_row_counts IS NULL AND cell_tombstone_sha256 IS NULL)
        OR
        (state='cell_erased' AND approved_at IS NOT NULL AND account_fingerprint IS NOT NULL AND operator_evidence_sha256 IS NOT NULL AND cell_request_version IS NOT NULL AND execution_lease_id IS NULL AND lease_expires_at IS NULL AND cell_erased_at IS NOT NULL AND cell_row_counts IS NOT NULL AND cell_tombstone_sha256 IS NOT NULL)
        OR
        (state='global_erasing' AND approved_at IS NOT NULL AND account_fingerprint IS NOT NULL AND operator_evidence_sha256 IS NOT NULL AND cell_request_version IS NOT NULL AND execution_lease_id IS NOT NULL AND lease_expires_at IS NOT NULL AND cell_erased_at IS NOT NULL AND cell_row_counts IS NOT NULL AND cell_tombstone_sha256 IS NOT NULL)
    );
DROP INDEX public.account_erasure_one_current;
CREATE UNIQUE INDEX account_erasure_one_current
    ON public.account_erasure_requests (account_id) WHERE state IN ('prepared','approved','cell_erasing','cell_erased','global_erasing');
CREATE INDEX account_erasure_execution_claim
    ON public.account_erasure_requests (state,lease_expires_at,requested_at,id)
    WHERE state IN ('approved','cell_erasing','cell_erased','global_erasing');

ALTER TABLE public.account_erasure_operator_events
    DROP CONSTRAINT account_erasure_operator_events_action_check;
ALTER TABLE public.account_erasure_operator_events
    ADD CONSTRAINT account_erasure_operator_events_action_check
    CHECK (action IN ('prepared','inspected','approved','canceled','cell_claimed','cell_erased','global_claimed'));

CREATE TABLE public.account_erasure_tombstones (
    request_id uuid PRIMARY KEY,
    account_fingerprint bytea NOT NULL UNIQUE CHECK (octet_length(account_fingerprint)=32),
    policy_version bigint NOT NULL CHECK (policy_version>0),
    final_request_version bigint NOT NULL CHECK (final_request_version>0),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    prepared_at timestamptz NOT NULL,
    approved_at timestamptz NOT NULL,
    cell_erased_at timestamptz NOT NULL,
    completed_at timestamptz NOT NULL,
    cell_row_counts jsonb NOT NULL CHECK (jsonb_typeof(cell_row_counts)='object'),
    global_row_counts jsonb NOT NULL CHECK (jsonb_typeof(global_row_counts)='object'),
    export_sha256 bytea CHECK (export_sha256 IS NULL OR octet_length(export_sha256)=32),
    cell_tombstone_sha256 bytea NOT NULL CHECK (octet_length(cell_tombstone_sha256)=32),
    operator_evidence_sha256 bytea NOT NULL CHECK (octet_length(operator_evidence_sha256)=32),
    backup_expires_at timestamptz NOT NULL CHECK (backup_expires_at>completed_at)
);
CREATE INDEX account_erasure_tombstones_completed
    ON public.account_erasure_tombstones (completed_at,request_id);

CREATE OR REPLACE FUNCTION public.reject_account_membership_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text;
BEGIN
    SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner FROM pg_class c WHERE c.oid='public.account_erasure_tombstones'::regclass;
    IF TG_OP='DELETE' AND current_user=tombstone_owner AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'Account Membership events are immutable';
END;
$$;

CREATE OR REPLACE FUNCTION public.reject_account_lifecycle_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text;
BEGIN
    SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner FROM pg_class c WHERE c.oid='public.account_erasure_tombstones'::regclass;
    IF TG_OP='DELETE' AND current_user=tombstone_owner AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'Account lifecycle events are immutable';
END;
$$;

CREATE OR REPLACE FUNCTION public.reject_account_erasure_operator_event_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text;
BEGIN
    SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner FROM pg_class c WHERE c.oid='public.account_erasure_tombstones'::regclass;
    IF TG_OP='DELETE' AND current_user=tombstone_owner AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text AND current_setting('spyglass.erasure_request_id',true)=OLD.request_id::text THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'Account erasure operator events are immutable';
END;
$$;

CREATE FUNCTION public.spyglass_claim_account_erasure_execution(
    p_request_id uuid,
    p_event_id uuid,
    p_account_id uuid,
    p_expected_version bigint,
    p_lease_id uuid,
    p_lease_seconds bigint,
    p_account_fingerprint bytea,
    p_operator_evidence_sha256 bytea,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS SETOF public.account_erasure_requests
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    result public.account_erasure_requests%ROWTYPE;
    eligible record;
    action_name text;
BEGIN
    IF p_request_id IS NULL OR p_event_id IS NULL OR p_account_id IS NULL OR p_expected_version IS NULL OR p_expected_version<=0 OR
       p_lease_id IS NULL OR p_lease_seconds IS NULL OR p_lease_seconds NOT BETWEEN 30 AND 3600 OR
       p_account_fingerprint IS NULL OR octet_length(p_account_fingerprint)<>32 OR
       p_operator_evidence_sha256 IS NULL OR octet_length(p_operator_evidence_sha256)<>32 OR
       p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Account erasure execution claim';
    END IF;
    SELECT * INTO result FROM public.account_erasure_requests WHERE id=p_request_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Account erasure request not found';
    END IF;
    IF result.account_id<>p_account_id OR result.version<>p_expected_version OR result.environment<>p_environment OR
       result.backup_expires_at<=statement_timestamp() OR
       (result.export_disposition='artifact' AND result.export_expires_at<=statement_timestamp()) THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Account erasure execution claim conflicts with request';
    END IF;
    IF result.state IN ('cell_erasing','global_erasing') AND result.lease_expires_at>statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Account erasure execution lease is active';
    END IF;
    SELECT * INTO eligible FROM public.spyglass_assert_account_erasure_eligible(result.account_id);
    IF eligible.closure_request_id<>result.closure_request_id OR eligible.cell_id<>result.cell_id OR
       eligible.placement_generation<>result.placement_generation OR eligible.account_version<>result.account_version THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Account erasure eligibility changed before execution';
    END IF;

    IF result.state='approved' THEN
        action_name := 'cell_claimed';
        UPDATE public.account_erasure_requests SET
            state='cell_erasing',account_fingerprint=p_account_fingerprint,
            operator_evidence_sha256=p_operator_evidence_sha256,cell_request_version=version+1,
            execution_lease_id=p_lease_id,lease_expires_at=statement_timestamp()+make_interval(secs=>p_lease_seconds::integer),version=version+1
        WHERE id=p_request_id RETURNING * INTO result;
    ELSIF result.state='cell_erasing' THEN
        IF result.account_fingerprint<>p_account_fingerprint OR result.operator_evidence_sha256<>p_operator_evidence_sha256 THEN
            RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Account erasure cell retry evidence mismatch';
        END IF;
        action_name := 'cell_claimed';
        UPDATE public.account_erasure_requests SET
            execution_lease_id=p_lease_id,lease_expires_at=statement_timestamp()+make_interval(secs=>p_lease_seconds::integer),version=version+1
        WHERE id=p_request_id RETURNING * INTO result;
    ELSIF result.state='cell_erased' OR result.state='global_erasing' THEN
        IF result.account_fingerprint<>p_account_fingerprint OR result.operator_evidence_sha256<>p_operator_evidence_sha256 OR result.cell_tombstone_sha256 IS NULL THEN
            RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Account erasure global retry evidence mismatch';
        END IF;
        action_name := 'global_claimed';
        UPDATE public.account_erasure_requests SET
            state='global_erasing',execution_lease_id=p_lease_id,
            lease_expires_at=statement_timestamp()+make_interval(secs=>p_lease_seconds::integer),version=version+1
        WHERE id=p_request_id RETURNING * INTO result;
    ELSE
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Account erasure request is not executable';
    END IF;
    INSERT INTO public.account_erasure_operator_events
        (id,request_id,account_id,action,actor,reason,environment,request_version,created_at)
    VALUES (p_event_id,result.id,result.account_id,action_name,btrim(p_actor),btrim(p_reason),p_environment,result.version,statement_timestamp());
    RETURN NEXT result;
END;
$$;

CREATE FUNCTION public.spyglass_record_account_cell_erasure(
    p_request_id uuid,
    p_event_id uuid,
    p_expected_version bigint,
    p_lease_id uuid,
    p_account_fingerprint bytea,
    p_cell_erased_at timestamptz,
    p_cell_row_counts jsonb,
    p_cell_tombstone_sha256 bytea,
    p_actor text,
    p_reason text,
    p_environment text
) RETURNS SETOF public.account_erasure_requests
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE result public.account_erasure_requests%ROWTYPE;
BEGIN
    IF p_request_id IS NULL OR p_event_id IS NULL OR p_expected_version IS NULL OR p_expected_version<=0 OR p_lease_id IS NULL OR
       p_account_fingerprint IS NULL OR octet_length(p_account_fingerprint)<>32 OR p_cell_erased_at IS NULL OR
       p_cell_row_counts IS NULL OR jsonb_typeof(p_cell_row_counts)<>'object' OR
       p_cell_tombstone_sha256 IS NULL OR octet_length(p_cell_tombstone_sha256)<>32 OR
       p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid cell Account erasure acknowledgement';
    END IF;
    UPDATE public.account_erasure_requests SET
        state='cell_erased',execution_lease_id=NULL,lease_expires_at=NULL,cell_erased_at=p_cell_erased_at,
        cell_row_counts=p_cell_row_counts,cell_tombstone_sha256=p_cell_tombstone_sha256,version=version+1
    WHERE id=p_request_id AND state='cell_erasing' AND version=p_expected_version AND execution_lease_id=p_lease_id AND
          lease_expires_at>statement_timestamp() AND environment=p_environment AND account_fingerprint=p_account_fingerprint
    RETURNING * INTO result;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='cell Account erasure acknowledgement lost its lease';
    END IF;
    INSERT INTO public.account_erasure_operator_events
        (id,request_id,account_id,action,actor,reason,environment,request_version,created_at)
    VALUES (p_event_id,result.id,result.account_id,'cell_erased',btrim(p_actor),btrim(p_reason),p_environment,result.version,statement_timestamp());
    RETURN NEXT result;
END;
$$;

CREATE FUNCTION public.spyglass_finalize_account_erasure(
    p_request_id uuid,
    p_account_id uuid,
    p_expected_version bigint,
    p_lease_id uuid,
    p_account_fingerprint bytea,
    p_cell_tombstone_sha256 bytea,
    p_environment text
) RETURNS SETOF public.account_erasure_tombstones
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    existing public.account_erasure_tombstones%ROWTYPE;
    target public.account_erasure_requests%ROWTYPE;
    eligible record;
    cell_identifier text;
    notification_count bigint; inbox_count bigint; recompute_count bigint; reservation_count bigint; counter_count bigint;
    reconciliation_count bigint; checkout_count bigint; subscription_count bigint; profile_count bigint; snapshot_count bigint; grant_count bigint;
    invitation_count bigint; membership_event_count bigint; lifecycle_event_count bigint; operator_event_count bigint; closure_count bigint;
    membership_count bigint; cursor_count bigint; directory_count bigint; request_count bigint; account_count bigint; capacity_count bigint;
    result public.account_erasure_tombstones%ROWTYPE;
BEGIN
    IF p_request_id IS NULL OR p_account_id IS NULL OR p_expected_version IS NULL OR p_expected_version<=0 OR p_lease_id IS NULL OR
       p_account_fingerprint IS NULL OR octet_length(p_account_fingerprint)<>32 OR
       p_cell_tombstone_sha256 IS NULL OR octet_length(p_cell_tombstone_sha256)<>32 OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid global Account erasure finalization';
    END IF;
    SELECT * INTO existing FROM public.account_erasure_tombstones WHERE request_id=p_request_id;
    IF FOUND THEN
        IF existing.account_fingerprint<>p_account_fingerprint OR existing.cell_tombstone_sha256<>p_cell_tombstone_sha256 OR existing.environment<>p_environment THEN
            RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='global Account erasure tombstone conflicts with invocation';
        END IF;
        RETURN NEXT existing;
        RETURN;
    END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended('spyglass:account-erasure:' || p_account_id::text,0));
    -- A concurrent identical finalizer may have committed while this call
    -- waited on the same fence. Re-read in a new READ COMMITTED statement
    -- snapshot before treating the now-deleted active request as an error.
    SELECT * INTO existing FROM public.account_erasure_tombstones WHERE request_id=p_request_id;
    IF FOUND THEN
        IF existing.account_fingerprint<>p_account_fingerprint OR existing.cell_tombstone_sha256<>p_cell_tombstone_sha256 OR existing.environment<>p_environment THEN
            RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='global Account erasure tombstone conflicts with invocation';
        END IF;
        RETURN NEXT existing;
        RETURN;
    END IF;
    SELECT * INTO target FROM public.account_erasure_requests WHERE id=p_request_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='global Account erasure request not found without tombstone';
    END IF;
    IF target.account_id<>p_account_id OR target.state<>'global_erasing' OR target.version<>p_expected_version OR
       target.execution_lease_id<>p_lease_id OR target.lease_expires_at<=statement_timestamp() OR
       target.account_fingerprint<>p_account_fingerprint OR target.cell_tombstone_sha256<>p_cell_tombstone_sha256 OR
       target.environment<>p_environment OR target.backup_expires_at<=statement_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='global Account erasure finalization conflicts with request';
    END IF;
    SELECT * INTO eligible FROM public.spyglass_assert_account_erasure_eligible(target.account_id);
    IF eligible.closure_request_id<>target.closure_request_id OR eligible.cell_id<>target.cell_id OR
       eligible.placement_generation<>target.placement_generation OR eligible.account_version<>target.account_version THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Account erasure eligibility changed before global finalization';
    END IF;
    cell_identifier := target.cell_id;
    PERFORM set_config('spyglass.erasure_request_id',target.id::text,true);
    PERFORM set_config('spyglass.erasure_account_id',target.account_id::text,true);

    DELETE FROM public.identity_notification_outbox WHERE account_id=target.account_id; GET DIAGNOSTICS notification_count=ROW_COUNT;
    DELETE FROM public.billing_event_inbox WHERE account_id=target.account_id; GET DIAGNOSTICS inbox_count=ROW_COUNT;
    DELETE FROM public.entitlement_recompute_queue WHERE account_id=target.account_id; GET DIAGNOSTICS recompute_count=ROW_COUNT;
    DELETE FROM public.entitlement_usage_reservations WHERE account_id=target.account_id; GET DIAGNOSTICS reservation_count=ROW_COUNT;
    DELETE FROM public.entitlement_usage_counters WHERE account_id=target.account_id; GET DIAGNOSTICS counter_count=ROW_COUNT;
    DELETE FROM public.billing_reconciliation_queue WHERE provider_subscription_id IN (SELECT provider_subscription_id FROM public.subscriptions WHERE account_id=target.account_id); GET DIAGNOSTICS reconciliation_count=ROW_COUNT;
    DELETE FROM public.billing_checkout_attempts WHERE account_id=target.account_id; GET DIAGNOSTICS checkout_count=ROW_COUNT;
    DELETE FROM public.subscriptions WHERE account_id=target.account_id; GET DIAGNOSTICS subscription_count=ROW_COUNT;
    DELETE FROM public.billing_profiles WHERE account_id=target.account_id; GET DIAGNOSTICS profile_count=ROW_COUNT;
    DELETE FROM public.entitlement_snapshots WHERE account_id=target.account_id; GET DIAGNOSTICS snapshot_count=ROW_COUNT;
    DELETE FROM public.entitlement_grants WHERE account_id=target.account_id; GET DIAGNOSTICS grant_count=ROW_COUNT;
    DELETE FROM public.invitations WHERE account_id=target.account_id; GET DIAGNOSTICS invitation_count=ROW_COUNT;
    DELETE FROM public.account_membership_events WHERE account_id=target.account_id; GET DIAGNOSTICS membership_event_count=ROW_COUNT;
    DELETE FROM public.account_lifecycle_events WHERE account_id=target.account_id; GET DIAGNOSTICS lifecycle_event_count=ROW_COUNT;
    DELETE FROM public.account_erasure_operator_events WHERE account_id=target.account_id; GET DIAGNOSTICS operator_event_count=ROW_COUNT;
    DELETE FROM public.account_erasure_requests WHERE id=target.id; GET DIAGNOSTICS request_count=ROW_COUNT;
    DELETE FROM public.account_closure_requests WHERE account_id=target.account_id; GET DIAGNOSTICS closure_count=ROW_COUNT;
    DELETE FROM public.memberships WHERE account_id=target.account_id; GET DIAGNOSTICS membership_count=ROW_COUNT;
    UPDATE public.entitlement_catalog_rollouts SET cursor_created_at=NULL,cursor_account_id=NULL WHERE cursor_account_id=target.account_id; GET DIAGNOSTICS cursor_count=ROW_COUNT;
    DELETE FROM public.account_directory WHERE account_id=target.account_id; GET DIAGNOSTICS directory_count=ROW_COUNT;
    UPDATE public.cells SET assigned_accounts=assigned_accounts-1 WHERE id=cell_identifier AND assigned_accounts>0; GET DIAGNOSTICS capacity_count=ROW_COUNT;
    IF capacity_count<>1 OR request_count<>1 OR directory_count<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='global Account erasure invariant count is invalid';
    END IF;
    DELETE FROM public.accounts WHERE id=target.account_id; GET DIAGNOSTICS account_count=ROW_COUNT;
    IF account_count<>1 THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='global Account erasure Account count is invalid';
    END IF;

    INSERT INTO public.account_erasure_tombstones
        (request_id,account_fingerprint,policy_version,final_request_version,environment,prepared_at,approved_at,
         cell_erased_at,completed_at,cell_row_counts,global_row_counts,export_sha256,cell_tombstone_sha256,
         operator_evidence_sha256,backup_expires_at)
    VALUES
        (target.id,target.account_fingerprint,target.policy_version,target.version,target.environment,target.requested_at,target.approved_at,
         target.cell_erased_at,statement_timestamp(),target.cell_row_counts,jsonb_build_object(
            'identity_notification_outbox',notification_count,'billing_event_inbox',inbox_count,
            'entitlement_recompute_queue',recompute_count,'entitlement_usage_reservations',reservation_count,
            'entitlement_usage_counters',counter_count,'billing_reconciliation_queue',reconciliation_count,
            'billing_checkout_attempts',checkout_count,'subscriptions',subscription_count,'billing_profiles',profile_count,
            'entitlement_snapshots',snapshot_count,'entitlement_grants',grant_count,'invitations',invitation_count,
            'account_membership_events',membership_event_count,'account_lifecycle_events',lifecycle_event_count,
            'account_erasure_operator_events',operator_event_count,'account_erasure_requests',request_count,
            'account_closure_requests',closure_count,'memberships',membership_count,'entitlement_catalog_rollout_cursors',cursor_count,
            'account_directory',directory_count,'accounts',account_count,'cell_capacity',capacity_count),
         target.export_sha256,target.cell_tombstone_sha256,target.operator_evidence_sha256,target.backup_expires_at)
    RETURNING * INTO result;
    RETURN NEXT result;
END;
$$;

CREATE FUNCTION public.spyglass_attest_global_account_erasure(
    p_request_id uuid,
    p_account_fingerprint bytea
) RETURNS SETOF public.account_erasure_tombstones
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE result public.account_erasure_tombstones%ROWTYPE;
BEGIN
    IF p_request_id IS NULL OR p_account_fingerprint IS NULL OR octet_length(p_account_fingerprint)<>32 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid global Account erasure attestation';
    END IF;
    SELECT * INTO result FROM public.account_erasure_tombstones WHERE request_id=p_request_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='global Account erasure tombstone not found';
    END IF;
    IF result.account_fingerprint<>p_account_fingerprint THEN
        RAISE EXCEPTION USING ERRCODE='P0003', MESSAGE='global Account erasure tombstone fingerprint mismatch';
    END IF;
    RETURN NEXT result;
END;
$$;

REVOKE ALL ON TABLE public.account_erasure_tombstones FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_account_erasure_execution(uuid,uuid,uuid,bigint,uuid,bigint,bytea,bytea,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_record_account_cell_erasure(uuid,uuid,bigint,uuid,bytea,timestamptz,jsonb,bytea,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_finalize_account_erasure(uuid,uuid,bigint,uuid,bytea,bytea,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_attest_global_account_erasure(uuid,bytea) FROM PUBLIC;

COMMIT;
