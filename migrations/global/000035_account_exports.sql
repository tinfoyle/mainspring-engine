BEGIN;

CREATE TABLE account_export_requests (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    requested_by_user_id uuid NOT NULL REFERENCES users(id),
    state text NOT NULL CHECK (state IN ('queued','building','available','failed','deleting','deleted','canceled')),
    cell_id text NOT NULL CHECK (cell_id ~ '^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$'),
    placement_generation bigint NOT NULL CHECK (placement_generation > 0),
    account_version bigint NOT NULL CHECK (account_version > 0),
    attempt_count bigint NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz,
    lease_id uuid,
    lease_expires_at timestamptz,
    snapshot_global_at timestamptz,
    snapshot_cell_at timestamptz,
    artifact_reference text,
    artifact_sha256 bytea,
    artifact_bytes bigint,
    error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    requested_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    available_at timestamptz,
    deleted_at timestamptz,
    CHECK (expires_at > requested_at AND expires_at <= requested_at + interval '30 days'),
    CHECK (
        (state IN ('building','deleting') AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (state NOT IN ('building','deleting') AND lease_id IS NULL AND lease_expires_at IS NULL)
    ),
    CHECK ((state='queued' AND next_attempt_at IS NOT NULL) OR (state<>'queued' AND next_attempt_at IS NULL)),
    CHECK (
        (state IN ('available','deleting') AND artifact_reference IS NOT NULL AND artifact_sha256 IS NOT NULL AND artifact_bytes IS NOT NULL AND available_at IS NOT NULL)
        OR (state='deleted' AND artifact_reference IS NULL AND artifact_sha256 IS NOT NULL AND artifact_bytes IS NOT NULL AND available_at IS NOT NULL AND deleted_at IS NOT NULL)
        OR (state IN ('queued','building','failed','canceled') AND artifact_reference IS NULL AND artifact_sha256 IS NULL AND artifact_bytes IS NULL AND available_at IS NULL AND deleted_at IS NULL)
    ),
    CHECK (artifact_reference IS NULL OR (length(artifact_reference) BETWEEN 1 AND 1000 AND artifact_reference=btrim(artifact_reference) AND artifact_reference !~ E'[\\x00\\r\\n]')),
    CHECK (artifact_sha256 IS NULL OR octet_length(artifact_sha256)=32),
    CHECK (artifact_bytes IS NULL OR artifact_bytes BETWEEN 1 AND 34359738368),
    CHECK (
        (state IN ('available','deleting','deleted') AND snapshot_global_at IS NOT NULL AND snapshot_cell_at IS NOT NULL)
        OR (state IN ('queued','building','failed','canceled') AND snapshot_global_at IS NULL AND snapshot_cell_at IS NULL)
    ),
    CHECK (snapshot_global_at IS NULL OR (snapshot_global_at >= requested_at - interval '1 minute' AND snapshot_global_at <= requested_at + interval '15 minutes' AND snapshot_global_at <= expires_at)),
    CHECK (snapshot_cell_at IS NULL OR (snapshot_cell_at >= requested_at - interval '1 minute' AND snapshot_cell_at <= requested_at + interval '15 minutes' AND snapshot_cell_at <= expires_at))
);

CREATE UNIQUE INDEX account_export_requests_one_active_per_account_idx
    ON account_export_requests(account_id)
    WHERE state IN ('queued','building','available','deleting');
CREATE INDEX account_export_requests_build_queue_idx
    ON account_export_requests(next_attempt_at,id)
    WHERE state='queued';
CREATE INDEX account_export_requests_build_recovery_idx
    ON account_export_requests(lease_expires_at,id)
    WHERE state='building';
CREATE INDEX account_export_requests_expiry_idx
    ON account_export_requests(expires_at,id)
    WHERE state='available';
CREATE INDEX account_export_requests_deletion_recovery_idx
    ON account_export_requests(lease_expires_at,id)
    WHERE state='deleting';
CREATE INDEX account_export_requests_account_history_idx
    ON account_export_requests(account_id,requested_at DESC,id DESC);

CREATE TABLE account_export_events (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    request_id uuid NOT NULL REFERENCES account_export_requests(id) ON DELETE CASCADE,
    event_type text NOT NULL CHECK (event_type IN ('requested','canceled','build_claimed','build_requeued','build_failed','available','deletion_claimed','deleted')),
    state text NOT NULL CHECK (state IN ('queued','building','available','failed','deleting','deleted','canceled')),
    request_version bigint NOT NULL CHECK (request_version > 0),
    actor_kind text NOT NULL CHECK (actor_kind IN ('user','workload')),
    actor_id text NOT NULL CHECK (length(actor_id) BETWEEN 1 AND 200 AND actor_id=btrim(actor_id)),
    error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    occurred_at timestamptz NOT NULL
);
CREATE INDEX account_export_events_request_idx ON account_export_events(request_id,occurred_at,id);
CREATE INDEX account_export_events_account_idx ON account_export_events(account_id,occurred_at,id);

CREATE FUNCTION spyglass_reject_account_export_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text; export_owner text;
BEGIN
    SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner FROM pg_class c WHERE c.oid='public.account_erasure_tombstones'::regclass;
    SELECT pg_get_userbyid(c.relowner) INTO export_owner FROM pg_class c WHERE c.oid='public.account_export_requests'::regclass;
    IF TG_OP='DELETE' AND current_user IN (tombstone_owner,export_owner) AND current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text
       AND (current_setting('spyglass.erasure_request_id',true)<>'' OR current_setting('spyglass.erasure_restore_replay',true)='on') THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION USING ERRCODE='42501', MESSAGE='Account export events are immutable';
END $$;
CREATE TRIGGER account_export_events_immutable
    BEFORE UPDATE OR DELETE ON account_export_events
    FOR EACH ROW EXECUTE FUNCTION spyglass_reject_account_export_event_mutation();
REVOKE ALL ON FUNCTION public.spyglass_reject_account_export_event_mutation() FROM PUBLIC;

CREATE FUNCTION spyglass_capture_account_export_erasure() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text; export_owner text; event_count bigint; prior jsonb;
BEGIN
    SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner FROM pg_class c WHERE c.oid='public.account_erasure_tombstones'::regclass;
    SELECT pg_get_userbyid(c.relowner) INTO export_owner FROM pg_class c WHERE c.oid='public.account_export_requests'::regclass;
    IF current_user NOT IN (tombstone_owner,export_owner) OR current_setting('spyglass.erasure_account_id',true)<>OLD.account_id::text OR
       (current_setting('spyglass.erasure_request_id',true)='' AND current_setting('spyglass.erasure_restore_replay',true)<>'on') THEN
        RAISE EXCEPTION 'Account export requests may be deleted only by Account erasure';
    END IF;
    SELECT count(*) INTO event_count FROM public.account_export_events WHERE request_id=OLD.id;
    prior:=COALESCE(NULLIF(current_setting('spyglass.account_export_erasure_counts',true),''),'{}')::jsonb;
    PERFORM set_config('spyglass.account_export_erasure_counts',jsonb_build_object(
        'account_export_requests',COALESCE((prior->>'account_export_requests')::bigint,0)+1,
        'account_export_events',COALESCE((prior->>'account_export_events')::bigint,0)+event_count)::text,true);
    RETURN OLD;
END $$;
CREATE TRIGGER account_export_requests_erasure_only BEFORE DELETE ON account_export_requests
    FOR EACH ROW EXECUTE FUNCTION spyglass_capture_account_export_erasure();

CREATE FUNCTION spyglass_add_account_export_erasure_counts() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.account_export_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN
        NEW.global_row_counts:=NEW.global_row_counts||counts::jsonb;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER account_erasure_account_export_counts BEFORE INSERT ON account_erasure_tombstones
    FOR EACH ROW EXECUTE FUNCTION spyglass_add_account_export_erasure_counts();

REVOKE ALL ON FUNCTION public.spyglass_capture_account_export_erasure() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_add_account_export_erasure_counts() FROM PUBLIC;

COMMIT;
