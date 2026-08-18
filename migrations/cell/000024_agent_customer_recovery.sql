BEGIN;

-- Customer recovery never rewrites a terminal Run. An acceptance records the
-- deliberate human outcome; a retry points to one newly planned Run that
-- reuses only the failed immutable PersonaVersions and original context
-- watermark. The unique resolution fence prevents duplicate recovery when callers
-- lose an HTTP response or mistakenly replace their operation UUID.
CREATE TABLE spyglass.agent_run_resolutions (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    run_id uuid NOT NULL,
    action text NOT NULL CHECK (action IN ('retry_failed','accept_failure')),
    note text NOT NULL CHECK (char_length(note) BETWEEN 3 AND 1000),
    actor_user_id uuid NOT NULL,
    retry_run_id uuid,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,run_id),
    FOREIGN KEY (account_id,run_id) REFERENCES spyglass.agent_runs(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,retry_run_id) REFERENCES spyglass.agent_runs(account_id,id),
    CHECK ((action='retry_failed' AND retry_run_id IS NOT NULL AND retry_run_id<>run_id) OR
           (action='accept_failure' AND retry_run_id IS NULL))
);

CREATE INDEX agent_run_resolutions_run
    ON spyglass.agent_run_resolutions(account_id,run_id,created_at,id);

ALTER TABLE spyglass.agent_run_resolutions ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_run_resolutions FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_run_resolutions_isolation ON spyglass.agent_run_resolutions
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

-- Preserve the stable erasure ABI while extending exact Account row counts.
ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz)
    RENAME TO spyglass_erase_account_cell_without_agent_run_resolutions;
ALTER FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea)
    RENAME TO spyglass_replay_account_cell_erasure_without_agent_run_resolutions;

CREATE FUNCTION spyglass.add_agent_run_resolution_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.agent_run_resolution_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_erasure_agent_run_resolution_counts
BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_agent_run_resolution_erasure_counts();

CREATE FUNCTION public.spyglass_erase_account_cell(
    p_request_id uuid,p_account_id uuid,p_placement_generation bigint,p_account_fingerprint bytea,
    p_policy_version bigint,p_request_version bigint,p_environment text,p_export_sha256 bytea,
    p_operator_evidence_sha256 bytea,p_backup_expires_at timestamptz
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE resolution_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT count(*) INTO resolution_count FROM spyglass.agent_run_resolutions WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.agent_run_resolution_erasure_counts',
        jsonb_build_object('agent_run_resolutions',resolution_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_erase_account_cell_without_agent_run_resolutions(
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
DECLARE resolution_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT count(*) INTO resolution_count FROM spyglass.agent_run_resolutions WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.agent_run_resolution_erasure_counts',
        jsonb_build_object('agent_run_resolutions',resolution_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_replay_account_cell_erasure_without_agent_run_resolutions(
        p_request_id,p_account_id,p_restored_placement_generation,p_tombstone_placement_generation,p_account_fingerprint,
        p_policy_version,p_request_version,p_environment,p_erased_at,p_export_sha256,p_operator_evidence_sha256,
        p_backup_expires_at,p_previous_ledger_sequence,p_previous_ledger_root,p_expected_ledger_sequence,p_expected_ledger_root);
END;
$$;

REVOKE ALL ON FUNCTION spyglass.add_agent_run_resolution_erasure_counts() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell_without_agent_run_resolutions(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure_without_agent_run_resolutions(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;

COMMIT;
