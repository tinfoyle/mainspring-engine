BEGIN;

CREATE TABLE spyglass.account_move_checkpoints (
    account_id uuid NOT NULL,
    move_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('source','destination')),
    state text NOT NULL CHECK (state IN ('frozen','staged','copied','active','rollback_active','retired')),
    placement_generation bigint NOT NULL CHECK (placement_generation>0),
    high_watermark text,
    manifest jsonb CHECK (manifest IS NULL OR jsonb_typeof(manifest)='object'),
    content_digest bytea CHECK (content_digest IS NULL OR octet_length(content_digest)=32),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY(account_id,move_id,role),
    CHECK ((manifest IS NULL)=(content_digest IS NULL))
);
ALTER TABLE spyglass.account_move_checkpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.account_move_checkpoints FORCE ROW LEVEL SECURITY;
CREATE POLICY account_move_checkpoints_isolation ON spyglass.account_move_checkpoints
    USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid)
    WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

-- Movement credentials have direct, release-generated grants over Account
-- tables, so a custom GUC alone is not sufficient authority. Every bypass is
-- bound to an existing checkpoint, its exact move ID/role, and namespace state.
CREATE FUNCTION spyglass.account_movement_write_allowed(p_account_id uuid,p_operation text) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,spyglass STABLE AS $$
  SELECT current_setting('spyglass.account_movement',true)='on'
     AND current_setting('app.account_id',true)=p_account_id::text
     AND current_setting('spyglass.account_movement_id',true)<>''
     AND current_setting('spyglass.account_movement_role',true) IN ('source','destination')
     AND EXISTS (
       SELECT 1 FROM spyglass.account_move_checkpoints checkpoint
       JOIN spyglass.account_namespaces namespace ON namespace.account_id=checkpoint.account_id
       WHERE checkpoint.account_id=p_account_id
         AND checkpoint.move_id=nullif(current_setting('spyglass.account_movement_id',true),'')::uuid
         AND checkpoint.role=current_setting('spyglass.account_movement_role',true)
         AND namespace.state='moving'
         AND ((checkpoint.role='destination' AND checkpoint.state IN ('staged','copied') AND p_operation IN ('INSERT','UPDATE','DELETE')) OR
              (checkpoint.role='source' AND checkpoint.state='copied' AND p_operation='DELETE'))
     )
$$;

CREATE FUNCTION spyglass.enforce_account_namespace_write_fence() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass AS $$
DECLARE target_account uuid; namespace_state text;
BEGIN
  target_account:=CASE WHEN TG_OP='DELETE' THEN OLD.account_id ELSE NEW.account_id END;
  IF target_account IS NULL THEN
    IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
  END IF;
  IF current_setting('spyglass.erasure_request_id',true)<>'' OR
     current_setting('spyglass.erasure_restore_replay',true)='on' OR
     spyglass.account_movement_write_allowed(target_account,TG_OP) THEN
    IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
  END IF;
  -- The share lock is held until the writer commits. The movement freeze takes
  -- an update lock, so it waits for already-running writes before fencing new
  -- ones. A route receipt consumed before the freeze cannot authorize a later
  -- business write because that write reaches this trigger independently.
  SELECT state INTO namespace_state FROM spyglass.account_namespaces
    WHERE account_id=target_account FOR SHARE;
  IF NOT FOUND OR (namespace_state NOT IN ('active','draining') AND
                   NOT (TG_TABLE_NAME='route_context_receipts' AND namespace_state='frozen')) THEN
    RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='Account namespace does not accept durable writes';
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;

CREATE FUNCTION spyglass.erase_account_move_checkpoints_with_namespace() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE checkpoint_count bigint;
BEGIN
    IF current_setting('spyglass.erasure_request_id',true)<>'' OR current_setting('spyglass.erasure_restore_replay',true)='on' THEN
      DELETE FROM spyglass.account_move_checkpoints WHERE account_id=OLD.account_id;
      GET DIAGNOSTICS checkpoint_count=ROW_COUNT;
      PERFORM set_config('spyglass.account_move_checkpoint_erasure_counts',jsonb_build_object('account_move_checkpoints',checkpoint_count)::text,true);
    END IF;
    RETURN OLD;
END;
$$;
CREATE TRIGGER account_namespace_move_checkpoint_erasure BEFORE DELETE ON spyglass.account_namespaces
FOR EACH ROW EXECUTE FUNCTION spyglass.erase_account_move_checkpoints_with_namespace();

CREATE FUNCTION spyglass.add_account_move_checkpoint_erasure_counts() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.account_move_checkpoint_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_erasure_move_checkpoint_counts BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_account_move_checkpoint_erasure_counts();

CREATE FUNCTION public.spyglass_stage_account_move(
    p_move_id uuid,p_account_id uuid,p_role text,p_expected_generation bigint,p_new_generation bigint,p_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,spyglass AS $$
DECLARE namespace record;
BEGIN
    IF p_move_id IS NULL OR p_account_id IS NULL OR p_role NOT IN ('source','destination') OR
       p_expected_generation<=0 OR p_new_generation<=0 OR p_at IS NULL THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move cell stage'; END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT placement_generation,state INTO namespace FROM spyglass.account_namespaces WHERE account_id=p_account_id FOR UPDATE;
    IF p_role='source' THEN
      IF NOT FOUND OR namespace.placement_generation<>p_expected_generation OR namespace.state NOT IN ('active','draining','moving') THEN
        RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='source Account namespace changed'; END IF;
      -- Freezing the namespace prevents new customer work. Refuse the freeze
      -- until every technical queue is terminal as well, otherwise a worker
      -- could mutate Account-owned rows after the source digest is captured.
      IF EXISTS (SELECT 1 FROM spyglass.work_capacity_release_queue
                 WHERE account_id=p_account_id AND processing_state<>'completed') OR
         EXISTS (SELECT 1 FROM spyglass.runner_invocation_queue
                 WHERE account_id=p_account_id AND processing_state NOT IN ('completed','execution_failed','canceled')) OR
         EXISTS (SELECT 1 FROM spyglass.agent_dispatch_queue
                 WHERE account_id=p_account_id AND state NOT IN ('provisioned','dead_letter')) OR
         EXISTS (SELECT 1 FROM spyglass.agent_result_projection_queue
                 WHERE account_id=p_account_id AND state NOT IN ('projected','dead_letter')) THEN
        RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='source Account has unfinished technical work';
      END IF;
      UPDATE spyglass.account_namespaces SET state='moving' WHERE account_id=p_account_id;
      INSERT INTO spyglass.account_move_checkpoints(account_id,move_id,role,state,placement_generation,updated_at)
      VALUES(p_account_id,p_move_id,'source','frozen',p_expected_generation,p_at)
      ON CONFLICT(account_id,move_id,role) DO NOTHING;
      PERFORM 1 FROM spyglass.account_move_checkpoints WHERE account_id=p_account_id AND move_id=p_move_id AND role='source'
        AND state='frozen' AND placement_generation=p_expected_generation FOR UPDATE;
      IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='source Account move checkpoint conflicts'; END IF;
    ELSE
      IF FOUND AND (namespace.placement_generation<>p_new_generation OR namespace.state<>'moving') THEN
        RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='destination Account namespace conflicts'; END IF;
      IF NOT FOUND THEN
        INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES(p_account_id,p_new_generation,'moving',p_at);
      END IF;
      INSERT INTO spyglass.account_move_checkpoints(account_id,move_id,role,state,placement_generation,updated_at)
      VALUES(p_account_id,p_move_id,'destination','staged',p_new_generation,p_at)
      ON CONFLICT(account_id,move_id,role) DO NOTHING;
      PERFORM 1 FROM spyglass.account_move_checkpoints WHERE account_id=p_account_id AND move_id=p_move_id AND role='destination'
        AND state='staged' AND placement_generation=p_new_generation FOR UPDATE;
      IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='destination Account move checkpoint conflicts'; END IF;
    END IF;
END;
$$;

CREATE FUNCTION public.spyglass_record_account_move_copy(
    p_move_id uuid,p_account_id uuid,p_role text,p_generation bigint,p_high_watermark text,p_manifest jsonb,p_digest bytea,p_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,spyglass AS $$
BEGIN
    IF p_move_id IS NULL OR p_account_id IS NULL OR p_role NOT IN ('source','destination') OR p_generation<=0 OR
       p_high_watermark IS NULL OR char_length(p_high_watermark) NOT BETWEEN 1 AND 500 OR p_manifest IS NULL OR jsonb_typeof(p_manifest)<>'object' OR
       p_digest IS NULL OR octet_length(p_digest)<>32 OR p_at IS NULL THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move copy checkpoint'; END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    UPDATE spyglass.account_move_checkpoints SET state='copied',high_watermark=p_high_watermark,manifest=p_manifest,
      content_digest=p_digest,updated_at=p_at WHERE account_id=p_account_id AND move_id=p_move_id AND role=p_role AND placement_generation=p_generation;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account move checkpoint changed'; END IF;
END;
$$;

CREATE FUNCTION public.spyglass_activate_account_move_namespace(
    p_move_id uuid,p_account_id uuid,p_role text,p_expected_generation bigint,p_active_generation bigint,p_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,spyglass AS $$
DECLARE checkpoint_state text;
BEGIN
    IF p_move_id IS NULL OR p_account_id IS NULL OR p_role NOT IN ('source','destination') OR
       p_expected_generation<=0 OR p_active_generation<=0 OR p_at IS NULL THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move activation'; END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT state INTO checkpoint_state FROM spyglass.account_move_checkpoints
      WHERE account_id=p_account_id AND move_id=p_move_id AND role=p_role FOR UPDATE;
    IF NOT FOUND OR checkpoint_state NOT IN ('frozen','staged','copied','active','rollback_active') THEN
      RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account move checkpoint cannot activate'; END IF;
    UPDATE spyglass.account_namespaces SET placement_generation=p_active_generation,state='active'
      WHERE account_id=p_account_id AND placement_generation=p_expected_generation AND state='moving';
    IF NOT FOUND THEN
      PERFORM 1 FROM spyglass.account_namespaces WHERE account_id=p_account_id AND placement_generation=p_active_generation AND state='active';
      IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account move namespace changed before activation'; END IF;
    END IF;
    UPDATE spyglass.account_move_checkpoints SET state=CASE WHEN p_role='source' THEN 'rollback_active' ELSE 'active' END,
      placement_generation=p_active_generation,updated_at=p_at WHERE account_id=p_account_id AND move_id=p_move_id AND role=p_role;
END;
$$;

CREATE FUNCTION public.spyglass_freeze_account_move_destination(
    p_move_id uuid,p_account_id uuid,p_generation bigint,p_at timestamptz
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,spyglass AS $$
BEGIN
    IF p_move_id IS NULL OR p_account_id IS NULL OR p_generation<=0 OR p_at IS NULL THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move destination freeze'; END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    UPDATE spyglass.account_namespaces SET state='moving' WHERE account_id=p_account_id AND placement_generation=p_generation AND state IN ('active','moving');
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='destination Account namespace changed'; END IF;
    UPDATE spyglass.account_move_checkpoints SET state='frozen',updated_at=p_at
      WHERE account_id=p_account_id AND move_id=p_move_id AND role='destination';
END;
$$;

-- Preserve immutable audit semantics while allowing only a checkpoint-bound
-- source-retirement delete. Ordinary movement writes and all updates remain
-- rejected by the original audit triggers.
CREATE OR REPLACE FUNCTION spyglass.reject_work_capacity_release_operator_event_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text;
BEGIN
  SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner
    FROM pg_class c WHERE c.oid='spyglass.account_erasure_tombstones'::regclass;
  IF TG_OP='DELETE' AND OLD.account_id IS NOT NULL THEN
    IF current_user=tombstone_owner AND current_setting('spyglass.erasure_request_id',true) IS NOT NULL AND
       current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN RETURN OLD; END IF;
    IF current_setting('spyglass.account_movement',true)='on' AND
       spyglass.account_movement_write_allowed(OLD.account_id,'DELETE') THEN RETURN OLD; END IF;
  END IF;
  RAISE EXCEPTION 'Work capacity release operator events are immutable';
END;
$$;

CREATE OR REPLACE FUNCTION spyglass.reject_agent_queue_operator_event_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text;
BEGIN
  SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner
    FROM pg_class c WHERE c.oid='spyglass.account_erasure_tombstones'::regclass;
  IF TG_OP='DELETE' AND OLD.account_id IS NOT NULL THEN
    IF current_user=tombstone_owner AND current_setting('spyglass.erasure_request_id',true) IS NOT NULL AND
       current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN RETURN OLD; END IF;
    IF current_setting('spyglass.account_movement',true)='on' AND
       spyglass.account_movement_write_allowed(OLD.account_id,'DELETE') THEN RETURN OLD; END IF;
  END IF;
  RAISE EXCEPTION 'Agent queue operator events are immutable';
END;
$$;

-- The historical erasure ABI is implemented as a wrapper chain. Establish the
-- erasure context at the outermost entry point so the movement write fence also
-- permits runner/Agent wrapper deletions that occur before the original core.
ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz)
  RENAME TO spyglass_erase_account_cell_without_movement_fence;
ALTER FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea)
  RENAME TO spyglass_replay_account_cell_erasure_without_movement_fence;

CREATE FUNCTION public.spyglass_erase_account_cell(
  p_request_id uuid,p_account_id uuid,p_placement_generation bigint,p_account_fingerprint bytea,
  p_policy_version bigint,p_request_version bigint,p_environment text,p_export_sha256 bytea,
  p_operator_evidence_sha256 bytea,p_backup_expires_at timestamptz
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
  PERFORM set_config('app.account_id',p_account_id::text,true);
  PERFORM set_config('spyglass.erasure_request_id',p_request_id::text,true);
  PERFORM set_config('spyglass.erasure_account_id',p_account_id::text,true);
  RETURN QUERY SELECT * FROM public.spyglass_erase_account_cell_without_movement_fence(
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
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
  PERFORM set_config('app.account_id',p_account_id::text,true);
  PERFORM set_config('spyglass.erasure_request_id',p_request_id::text,true);
  PERFORM set_config('spyglass.erasure_account_id',p_account_id::text,true);
  PERFORM set_config('spyglass.erasure_restore_replay','on',true);
  RETURN QUERY SELECT * FROM public.spyglass_replay_account_cell_erasure_without_movement_fence(
    p_request_id,p_account_id,p_restored_placement_generation,p_tombstone_placement_generation,p_account_fingerprint,
    p_policy_version,p_request_version,p_environment,p_erased_at,p_export_sha256,p_operator_evidence_sha256,
    p_backup_expires_at,p_previous_ledger_sequence,p_previous_ledger_root,p_expected_ledger_sequence,p_expected_ledger_root);
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_stage_account_move(uuid,uuid,text,bigint,bigint,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_record_account_move_copy(uuid,uuid,text,bigint,text,jsonb,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_activate_account_move_namespace(uuid,uuid,text,bigint,bigint,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_freeze_account_move_destination(uuid,uuid,bigint,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.enforce_account_namespace_write_fence() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell_without_movement_fence(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure_without_movement_fence(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.erase_account_move_checkpoints_with_namespace() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass.add_account_move_checkpoint_erasure_counts() FROM PUBLIC;

-- Apply the fence to every existing durable Account table. Future migrations
-- that add an account_id table must install the same trigger in that migration;
-- the movement copier separately refuses tables without deterministic identity.
DO $$
DECLARE target record;
BEGIN
  FOR target IN
    SELECT c.relname FROM pg_catalog.pg_class c
    JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
    WHERE n.nspname='spyglass' AND c.relkind='r'
      AND c.relname NOT IN ('account_namespaces','account_move_checkpoints','account_erasure_tombstones','account_erasure_restore_ledger')
      AND EXISTS (SELECT 1 FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attname='account_id' AND a.attnum>0 AND NOT a.attisdropped)
    ORDER BY c.relname
  LOOP
    EXECUTE format('CREATE TRIGGER account_namespace_write_fence BEFORE INSERT OR UPDATE OR DELETE ON spyglass.%I FOR EACH ROW EXECUTE FUNCTION spyglass.enforce_account_namespace_write_fence()',target.relname);
  END LOOP;
END;
$$;

COMMIT;
