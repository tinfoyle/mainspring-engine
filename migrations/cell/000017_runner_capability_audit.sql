BEGIN;

-- Capability audit contains identity and lifecycle facts only. Tool inputs,
-- outputs, credentials, provider errors, and customer content are forbidden.
CREATE TABLE spyglass.runner_capability_events (
    account_id uuid NOT NULL REFERENCES spyglass.account_namespaces(account_id),
    event_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    pod_uid uuid NOT NULL,
    operation_id uuid NOT NULL,
    capability text NOT NULL CHECK (capability ~ '^[a-z][a-z0-9.:/-]{0,127}$'),
    effect text NOT NULL CHECK (effect IN ('read_only','consequential','unregistered')),
    decision text NOT NULL CHECK (decision IN ('authorized','denied','succeeded','failed')),
    error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,event_id),
    CHECK ((decision IN ('authorized','succeeded') AND error_code IS NULL) OR
           (decision IN ('denied','failed') AND error_code IS NOT NULL))
);
CREATE INDEX runner_capability_events_invocation
    ON spyglass.runner_capability_events(account_id,invocation_id,occurred_at,event_id);

ALTER TABLE spyglass.runner_capability_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.runner_capability_events FORCE ROW LEVEL SECURITY;
CREATE POLICY runner_capability_events_isolation ON spyglass.runner_capability_events
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

CREATE FUNCTION public.spyglass_record_runner_capability_event(
    p_account_id uuid,p_event_id uuid,p_invocation_id uuid,p_pod_uid uuid,p_operation_id uuid,
    p_capability text,p_effect text,p_decision text,p_error_code text,p_occurred_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
BEGIN
    IF p_account_id IS NULL OR p_event_id IS NULL OR p_invocation_id IS NULL OR p_pod_uid IS NULL OR
       p_operation_id IS NULL OR p_capability IS NULL OR p_capability !~ '^[a-z][a-z0-9.:/-]{0,127}$' OR
       p_effect NOT IN ('read_only','consequential','unregistered') OR
       p_decision NOT IN ('authorized','denied','succeeded','failed') OR p_occurred_at IS NULL OR
       (p_decision IN ('authorized','succeeded') AND p_error_code IS NOT NULL) OR
       (p_decision IN ('denied','failed') AND (p_error_code IS NULL OR p_error_code !~ '^[a-z][a-z0-9_]{0,99}$')) THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner capability audit event';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    IF p_decision='authorized' THEN
        -- This is the final execution gate. The share lock serializes it with
        -- cancellation's queue-row update: whichever commits first defines
        -- whether the call was admitted. Post-execution facts remain writable
        -- after cancellation so an already-admitted outcome is never lost.
        PERFORM 1 FROM spyglass.runner_invocation_queue q
        JOIN spyglass.runner_invocation_exchanges x
          ON x.account_id=q.account_id AND x.invocation_id=q.invocation_id
        WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id
          AND q.processing_state IN ('launch_uncertain','launched') AND x.bound_pod_uid=p_pod_uid
        FOR SHARE OF q;
        IF NOT FOUND THEN
            RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='runner capability audit identity denied';
        END IF;
    ELSIF NOT EXISTS (
        SELECT 1 FROM spyglass.runner_invocation_exchanges x
        WHERE x.account_id=p_account_id AND x.invocation_id=p_invocation_id AND x.bound_pod_uid=p_pod_uid
    ) THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='runner capability audit identity denied';
    END IF;
    INSERT INTO spyglass.runner_capability_events
        (account_id,event_id,invocation_id,pod_uid,operation_id,capability,effect,decision,error_code,occurred_at)
    VALUES
        (p_account_id,p_event_id,p_invocation_id,p_pod_uid,p_operation_id,p_capability,p_effect,p_decision,p_error_code,p_occurred_at);
END;
$$;
REVOKE ALL ON FUNCTION public.spyglass_record_runner_capability_event(uuid,uuid,uuid,uuid,uuid,text,text,text,text,timestamptz) FROM PUBLIC;

ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz)
    RENAME TO spyglass_erase_account_cell_without_runner_capability_audit;
ALTER FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea)
    RENAME TO spyglass_replay_account_cell_erasure_without_runner_capability_audit;

CREATE FUNCTION spyglass.add_runner_capability_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.runner_capability_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_erasure_runner_capability_counts
BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_runner_capability_erasure_counts();

CREATE FUNCTION public.spyglass_erase_account_cell(
    p_request_id uuid,p_account_id uuid,p_placement_generation bigint,p_account_fingerprint bytea,
    p_policy_version bigint,p_request_version bigint,p_environment text,p_export_sha256 bytea,
    p_operator_evidence_sha256 bytea,p_backup_expires_at timestamptz
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE event_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    DELETE FROM spyglass.runner_capability_events WHERE account_id=p_account_id;
    GET DIAGNOSTICS event_count=ROW_COUNT;
    PERFORM set_config('spyglass.runner_capability_erasure_counts',jsonb_build_object('runner_capability_events',event_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_erase_account_cell_without_runner_capability_audit(
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
DECLARE event_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    DELETE FROM spyglass.runner_capability_events WHERE account_id=p_account_id;
    GET DIAGNOSTICS event_count=ROW_COUNT;
    PERFORM set_config('spyglass.runner_capability_erasure_counts',jsonb_build_object('runner_capability_events',event_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_replay_account_cell_erasure_without_runner_capability_audit(
        p_request_id,p_account_id,p_restored_placement_generation,p_tombstone_placement_generation,p_account_fingerprint,
        p_policy_version,p_request_version,p_environment,p_erased_at,p_export_sha256,p_operator_evidence_sha256,
        p_backup_expires_at,p_previous_ledger_sequence,p_previous_ledger_root,p_expected_ledger_sequence,p_expected_ledger_root);
END;
$$;

REVOKE ALL ON FUNCTION spyglass.add_runner_capability_erasure_counts() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell_without_runner_capability_audit(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure_without_runner_capability_audit(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;

COMMIT;
