BEGIN;

CREATE TABLE public.account_moves (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES public.accounts(id),
    state text NOT NULL CHECK (state IN ('prepared','drained','copied','ready','switched','paused','retiring','rolled_back','completed')),
    resume_state text CHECK (resume_state IS NULL OR resume_state IN ('prepared','drained','copied','ready')),
    source_cell_id text NOT NULL REFERENCES public.cells(id),
    destination_cell_id text NOT NULL REFERENCES public.cells(id),
    source_generation bigint NOT NULL CHECK (source_generation>0),
    destination_generation bigint NOT NULL CHECK (destination_generation>0),
    rollback_generation bigint CHECK (rollback_generation IS NULL OR rollback_generation>0),
    account_version bigint NOT NULL CHECK (account_version>0),
    version bigint NOT NULL DEFAULT 1 CHECK (version>0),
    lease_id uuid,
    lease_expires_at timestamptz,
    source_high_watermark text CHECK (source_high_watermark IS NULL OR char_length(source_high_watermark) BETWEEN 1 AND 500),
    source_manifest jsonb CHECK (source_manifest IS NULL OR jsonb_typeof(source_manifest)='object'),
    destination_manifest jsonb CHECK (destination_manifest IS NULL OR jsonb_typeof(destination_manifest)='object'),
    source_digest bytea CHECK (source_digest IS NULL OR octet_length(source_digest)=32),
    destination_digest bytea CHECK (destination_digest IS NULL OR octet_length(destination_digest)=32),
    rollback_window_seconds integer NOT NULL CHECK (rollback_window_seconds BETWEEN 300 AND 604800),
    rollback_expires_at timestamptz,
    prepared_at timestamptz NOT NULL,
    drained_at timestamptz,
    copied_at timestamptz,
    reconciled_at timestamptz,
    switched_at timestamptz,
    paused_at timestamptz,
    retired_at timestamptz,
    completed_at timestamptz,
    rolled_back_at timestamptz,
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    requested_by text NOT NULL CHECK (char_length(requested_by) BETWEEN 3 AND 200 AND requested_by !~ E'[\r\n]'),
    request_reason text NOT NULL CHECK (char_length(request_reason) BETWEEN 8 AND 500 AND request_reason !~ E'[\r\n]'),
    CHECK (source_cell_id<>destination_cell_id),
    CHECK (destination_generation=source_generation+1),
    CHECK ((lease_id IS NULL)=(lease_expires_at IS NULL)),
    CHECK ((state='paused')=(resume_state IS NOT NULL)),
    CHECK ((source_digest IS NULL)=(source_manifest IS NULL)),
    CHECK ((destination_digest IS NULL)=(destination_manifest IS NULL))
);
CREATE UNIQUE INDEX account_moves_one_open_per_account ON public.account_moves(account_id)
    WHERE state NOT IN ('rolled_back','completed');
CREATE INDEX account_moves_state ON public.account_moves(state,lease_expires_at,prepared_at);

CREATE TABLE public.account_move_events (
    move_id uuid NOT NULL REFERENCES public.account_moves(id),
    event_id uuid NOT NULL,
    action text NOT NULL CHECK (action IN ('prepared','inspected','claimed','drained','copied','reconciled','switched','paused','resumed','retirement_started','completed','rolled_back')),
    from_state text,
    to_state text NOT NULL,
    move_version bigint NOT NULL CHECK (move_version>0),
    actor text NOT NULL CHECK (char_length(actor) BETWEEN 3 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (move_id,event_id)
);
CREATE INDEX account_move_events_time ON public.account_move_events(move_id,occurred_at);

ALTER TABLE public.account_moves DROP CONSTRAINT account_moves_account_id_fkey;
ALTER TABLE public.account_moves ADD CONSTRAINT account_moves_account_id_fkey
    FOREIGN KEY(account_id) REFERENCES public.accounts(id) ON DELETE CASCADE;
ALTER TABLE public.account_move_events DROP CONSTRAINT account_move_events_move_id_fkey;
ALTER TABLE public.account_move_events ADD CONSTRAINT account_move_events_move_id_fkey
    FOREIGN KEY(move_id) REFERENCES public.account_moves(id) ON DELETE CASCADE;

CREATE FUNCTION public.spyglass_reject_account_move_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text; erased_account text;
BEGIN
    SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner FROM pg_class c WHERE c.oid='public.account_erasure_tombstones'::regclass;
    IF TG_OP='DELETE' THEN
      SELECT account_id::text INTO erased_account FROM public.account_moves WHERE id=OLD.move_id;
      IF current_user=tombstone_owner AND current_setting('spyglass.erasure_account_id',true)=erased_account AND
         (current_setting('spyglass.erasure_request_id',true)<>'' OR current_setting('spyglass.erasure_restore_replay',true)='on') THEN
        RETURN OLD;
      END IF;
    END IF;
    RAISE EXCEPTION 'Account move events are immutable';
END;
$$;
CREATE TRIGGER account_move_events_immutable BEFORE UPDATE OR DELETE ON public.account_move_events
FOR EACH ROW EXECUTE FUNCTION public.spyglass_reject_account_move_event_mutation();

CREATE FUNCTION public.spyglass_capture_account_move_erasure() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text; event_count bigint; prior jsonb;
BEGIN
    SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner FROM pg_class c WHERE c.oid='public.account_erasure_tombstones'::regclass;
    IF current_user<>tombstone_owner OR current_setting('spyglass.erasure_account_id',true)<>OLD.account_id::text OR
       (current_setting('spyglass.erasure_request_id',true)='' AND current_setting('spyglass.erasure_restore_replay',true)<>'on') THEN
      RAISE EXCEPTION 'Account move records may be deleted only by Account erasure';
    END IF;
    SELECT count(*) INTO event_count FROM public.account_move_events WHERE move_id=OLD.id;
    prior:=COALESCE(NULLIF(current_setting('spyglass.account_move_erasure_counts',true),''),'{}')::jsonb;
    PERFORM set_config('spyglass.account_move_erasure_counts',jsonb_build_object(
      'account_moves',COALESCE((prior->>'account_moves')::bigint,0)+1,
      'account_move_events',COALESCE((prior->>'account_move_events')::bigint,0)+event_count)::text,true);
    RETURN OLD;
END;
$$;
CREATE TRIGGER account_moves_erasure_only BEFORE DELETE ON public.account_moves
FOR EACH ROW EXECUTE FUNCTION public.spyglass_capture_account_move_erasure();

CREATE FUNCTION public.spyglass_add_account_move_erasure_counts() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.account_move_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.global_row_counts:=NEW.global_row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_erasure_account_move_counts BEFORE INSERT ON public.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION public.spyglass_add_account_move_erasure_counts();

CREATE FUNCTION public.spyglass_prepare_account_move(
    p_move_id uuid,p_event_id uuid,p_account_id uuid,p_destination_cell_id text,p_rollback_window_seconds integer,
    p_actor text,p_reason text,p_environment text
) RETURNS SETOF public.account_moves
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE target record; destination record; created public.account_moves;
BEGIN
    IF p_move_id IS NULL OR p_event_id IS NULL OR p_account_id IS NULL OR
       p_destination_cell_id !~ '^[a-z][a-z0-9-]{0,62}$' OR p_rollback_window_seconds NOT BETWEEN 300 AND 604800 OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR p_actor ~ E'[\r\n]' OR
       char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move preparation';
    END IF;
    SELECT a.id,a.cell_id,a.placement_generation,a.version,a.state,d.state AS directory_state,d.cell_id AS directory_cell,
           d.placement_generation AS directory_generation
      INTO target FROM public.accounts a JOIN public.account_directory d ON d.account_id=a.id
      WHERE a.id=p_account_id FOR UPDATE OF a,d;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002',MESSAGE='Account move target not found'; END IF;
    IF target.state<>'active' OR target.directory_state<>'active' OR target.cell_id<>target.directory_cell OR
       target.placement_generation<>target.directory_generation OR target.cell_id=p_destination_cell_id THEN
        RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='Account move target is not eligible';
    END IF;
    SELECT id,state,assigned_accounts,soft_account_limit INTO destination FROM public.cells
      WHERE id=p_destination_cell_id FOR UPDATE;
    IF NOT FOUND OR destination.state<>'active' OR destination.assigned_accounts>=destination.soft_account_limit THEN
        RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='Account move destination has no capacity';
    END IF;
    INSERT INTO public.account_moves(id,account_id,state,source_cell_id,destination_cell_id,source_generation,
        destination_generation,account_version,rollback_window_seconds,prepared_at,environment,requested_by,request_reason)
    VALUES(p_move_id,p_account_id,'prepared',target.cell_id,p_destination_cell_id,target.placement_generation,
        target.placement_generation+1,target.version,p_rollback_window_seconds,statement_timestamp(),p_environment,btrim(p_actor),btrim(p_reason))
    RETURNING * INTO created;
    INSERT INTO public.account_move_events(move_id,event_id,action,from_state,to_state,move_version,actor,reason,environment,occurred_at)
    VALUES(created.id,p_event_id,'prepared',NULL,created.state,created.version,btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());
    RETURN NEXT created;
END;
$$;

CREATE FUNCTION public.spyglass_inspect_account_move(
    p_move_id uuid,p_event_id uuid,p_actor text,p_reason text,p_environment text
) RETURNS SETOF public.account_moves
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE target public.account_moves;
BEGIN
    IF p_move_id IS NULL OR p_event_id IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR
       char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR
       p_actor ~ E'[\r\n]' OR p_reason ~ E'[\r\n]' THEN
        RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move inspection';
    END IF;
    SELECT * INTO target FROM public.account_moves WHERE id=p_move_id;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002',MESSAGE='Account move not found'; END IF;
    IF target.environment<>p_environment THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='Account move environment mismatch'; END IF;
    INSERT INTO public.account_move_events(move_id,event_id,action,from_state,to_state,move_version,actor,reason,environment,occurred_at)
    VALUES(target.id,p_event_id,'inspected',target.state,target.state,target.version,btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());
    RETURN NEXT target;
END;
$$;

CREATE FUNCTION public.spyglass_claim_account_move(
    p_move_id uuid,p_event_id uuid,p_expected_version bigint,p_lease_id uuid,p_lease_seconds integer,
    p_actor text,p_reason text,p_environment text
) RETURNS SETOF public.account_moves
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE target public.account_moves; now_at timestamptz:=statement_timestamp();
BEGIN
    IF p_move_id IS NULL OR p_event_id IS NULL OR p_expected_version<=0 OR p_lease_id IS NULL OR p_lease_seconds NOT BETWEEN 30 AND 3600 OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR
       p_actor ~ E'[\r\n]' OR p_reason ~ E'[\r\n]' OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move claim';
    END IF;
    SELECT * INTO target FROM public.account_moves WHERE id=p_move_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002',MESSAGE='Account move not found'; END IF;
    IF target.version<>p_expected_version OR target.environment<>p_environment OR target.state IN ('paused','rolled_back','completed') OR
       (target.lease_id IS NOT NULL AND target.lease_id<>p_lease_id AND target.lease_expires_at>now_at) THEN
        RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account move claim conflict';
    END IF;
    UPDATE public.account_moves SET lease_id=p_lease_id,lease_expires_at=now_at+make_interval(secs=>p_lease_seconds),version=version+1
      WHERE id=p_move_id RETURNING * INTO target;
    INSERT INTO public.account_move_events(move_id,event_id,action,from_state,to_state,move_version,actor,reason,environment,occurred_at)
    VALUES(target.id,p_event_id,'claimed',target.state,target.state,target.version,btrim(p_actor),btrim(p_reason),p_environment,now_at);
    RETURN NEXT target;
END;
$$;

CREATE FUNCTION public.spyglass_advance_account_move(
    p_move_id uuid,p_event_id uuid,p_expected_version bigint,p_lease_id uuid,p_action text,
    p_source_high_watermark text,p_source_manifest jsonb,p_source_digest bytea,
    p_destination_manifest jsonb,p_destination_digest bytea,p_actor text,p_reason text,p_environment text
) RETURNS SETOF public.account_moves
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE target public.account_moves; prior text; next_state text; now_at timestamptz:=statement_timestamp(); action_name text;
BEGIN
    IF p_move_id IS NULL OR p_event_id IS NULL OR p_expected_version<=0 OR p_lease_id IS NULL OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR
       p_actor ~ E'[\r\n]' OR p_reason ~ E'[\r\n]' OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move advance';
    END IF;
    SELECT * INTO target FROM public.account_moves WHERE id=p_move_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002',MESSAGE='Account move not found'; END IF;
    IF target.version<>p_expected_version OR target.environment<>p_environment OR target.lease_id<>p_lease_id OR target.lease_expires_at<=now_at THEN
        RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account move lease conflict';
    END IF;
    prior:=target.state;
    CASE p_action
      WHEN 'drain' THEN
        IF target.state<>'prepared' THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='Account move state conflict'; END IF;
        UPDATE public.account_directory SET state='moving',updated_at=now_at
          WHERE account_id=target.account_id AND cell_id=target.source_cell_id AND placement_generation=target.source_generation AND state='active';
        IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account placement changed before drain'; END IF;
        next_state:='drained'; action_name:='drained';
        UPDATE public.account_moves SET state=next_state,drained_at=now_at,lease_id=NULL,lease_expires_at=NULL,version=version+1 WHERE id=target.id;
      WHEN 'copy' THEN
        IF target.state<>'drained' OR p_source_high_watermark IS NULL OR char_length(p_source_high_watermark) NOT BETWEEN 1 AND 500 OR
           p_source_manifest IS NULL OR jsonb_typeof(p_source_manifest)<>'object' OR p_source_digest IS NULL OR octet_length(p_source_digest)<>32 THEN
          RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='Account move copy evidence invalid'; END IF;
        next_state:='copied'; action_name:='copied';
        UPDATE public.account_moves SET state=next_state,source_high_watermark=p_source_high_watermark,source_manifest=p_source_manifest,
          source_digest=p_source_digest,copied_at=now_at,lease_id=NULL,lease_expires_at=NULL,version=version+1 WHERE id=target.id;
      WHEN 'reconcile' THEN
        IF target.state<>'copied' OR p_destination_manifest IS NULL OR jsonb_typeof(p_destination_manifest)<>'object' OR
           p_destination_digest IS NULL OR octet_length(p_destination_digest)<>32 OR
           target.source_digest<>p_destination_digest OR target.source_manifest<>p_destination_manifest THEN
          RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='Account move reconciliation mismatch'; END IF;
        next_state:='ready'; action_name:='reconciled';
        UPDATE public.account_moves SET state=next_state,destination_manifest=p_destination_manifest,destination_digest=p_destination_digest,
          reconciled_at=now_at,lease_id=NULL,lease_expires_at=NULL,version=version+1 WHERE id=target.id;
      ELSE RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='unknown Account move advance action';
    END CASE;
    SELECT * INTO target FROM public.account_moves WHERE id=p_move_id;
    INSERT INTO public.account_move_events(move_id,event_id,action,from_state,to_state,move_version,actor,reason,environment,occurred_at)
    VALUES(target.id,p_event_id,action_name,prior,target.state,target.version,btrim(p_actor),btrim(p_reason),p_environment,now_at);
    RETURN NEXT target;
END;
$$;

CREATE FUNCTION public.spyglass_switch_account_move(
    p_move_id uuid,p_event_id uuid,p_expected_version bigint,p_lease_id uuid,p_actor text,p_reason text,p_environment text
) RETURNS SETOF public.account_moves
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE target public.account_moves; account_row record; now_at timestamptz:=statement_timestamp();
BEGIN
    IF p_move_id IS NULL OR p_event_id IS NULL OR p_expected_version<=0 OR p_lease_id IS NULL OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR
       p_actor ~ E'[\r\n]' OR p_reason ~ E'[\r\n]' OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move switch'; END IF;
    SELECT * INTO target FROM public.account_moves WHERE id=p_move_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002',MESSAGE='Account move not found'; END IF;
    IF target.version<>p_expected_version OR target.state<>'ready' OR target.environment<>p_environment OR
       target.lease_id<>p_lease_id OR target.lease_expires_at<=now_at THEN
      RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account move switch conflict'; END IF;
    SELECT a.cell_id,a.placement_generation,a.version,d.state AS directory_state,d.cell_id AS directory_cell,d.placement_generation AS directory_generation
      INTO account_row FROM public.accounts a JOIN public.account_directory d ON d.account_id=a.id
      WHERE a.id=target.account_id FOR UPDATE OF a,d;
    IF account_row.cell_id<>target.source_cell_id OR account_row.placement_generation<>target.source_generation OR
       account_row.version<>target.account_version OR account_row.directory_cell<>target.source_cell_id OR
       account_row.directory_generation<>target.source_generation OR account_row.directory_state<>'moving' THEN
      RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account placement changed before switch'; END IF;
    UPDATE public.cells SET assigned_accounts=assigned_accounts-1 WHERE id=target.source_cell_id AND assigned_accounts>0;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='source cell capacity underflow'; END IF;
    UPDATE public.cells SET assigned_accounts=assigned_accounts+1 WHERE id=target.destination_cell_id AND state='active' AND assigned_accounts<soft_account_limit;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='destination cell capacity unavailable'; END IF;
    UPDATE public.accounts SET cell_id=target.destination_cell_id,placement_generation=target.destination_generation,version=version+1 WHERE id=target.account_id;
    UPDATE public.account_directory SET cell_id=target.destination_cell_id,placement_generation=target.destination_generation,state='active',
      data_region=(SELECT region FROM public.cells WHERE id=target.destination_cell_id),updated_at=now_at WHERE account_id=target.account_id;
    UPDATE public.account_moves SET state='switched',switched_at=now_at,rollback_expires_at=now_at+make_interval(secs=>rollback_window_seconds),
      lease_id=NULL,lease_expires_at=NULL,version=version+1 WHERE id=target.id RETURNING * INTO target;
    INSERT INTO public.account_move_events(move_id,event_id,action,from_state,to_state,move_version,actor,reason,environment,occurred_at)
    VALUES(target.id,p_event_id,'switched','ready','switched',target.version,btrim(p_actor),btrim(p_reason),p_environment,now_at);
    RETURN NEXT target;
END;
$$;

CREATE FUNCTION public.spyglass_pause_account_move(
    p_move_id uuid,p_event_id uuid,p_expected_version bigint,p_pause boolean,p_actor text,p_reason text,p_environment text
) RETURNS SETOF public.account_moves
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE target public.account_moves; prior text; now_at timestamptz:=statement_timestamp();
BEGIN
    IF p_move_id IS NULL OR p_event_id IS NULL OR p_expected_version<=0 OR p_pause IS NULL OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR
       p_actor ~ E'[\r\n]' OR p_reason ~ E'[\r\n]' OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move pause'; END IF;
    SELECT * INTO target FROM public.account_moves WHERE id=p_move_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002',MESSAGE='Account move not found'; END IF;
    IF target.version<>p_expected_version OR target.environment<>p_environment OR target.lease_id IS NOT NULL THEN
      RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account move pause conflict'; END IF;
    prior:=target.state;
    IF p_pause THEN
      IF target.state NOT IN ('prepared','drained','copied','ready') THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='Account move cannot be paused'; END IF;
      UPDATE public.account_moves SET state='paused',resume_state=target.state,paused_at=now_at,version=version+1 WHERE id=target.id;
    ELSE
      IF target.state<>'paused' OR target.resume_state IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='Account move is not paused'; END IF;
      UPDATE public.account_moves SET state=target.resume_state,resume_state=NULL,paused_at=NULL,version=version+1 WHERE id=target.id;
    END IF;
    SELECT * INTO target FROM public.account_moves WHERE id=p_move_id;
    INSERT INTO public.account_move_events(move_id,event_id,action,from_state,to_state,move_version,actor,reason,environment,occurred_at)
    VALUES(target.id,p_event_id,CASE WHEN p_pause THEN 'paused' ELSE 'resumed' END,prior,target.state,target.version,btrim(p_actor),btrim(p_reason),p_environment,now_at);
    RETURN NEXT target;
END;
$$;

CREATE FUNCTION public.spyglass_rollback_account_move(
    p_move_id uuid,p_event_id uuid,p_expected_version bigint,p_lease_id uuid,p_actor text,p_reason text,p_environment text
) RETURNS SETOF public.account_moves
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE target public.account_moves; now_at timestamptz:=statement_timestamp(); rollback_gen bigint;
BEGIN
    IF p_move_id IS NULL OR p_event_id IS NULL OR p_expected_version<=0 OR p_lease_id IS NULL OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR
       p_actor ~ E'[\r\n]' OR p_reason ~ E'[\r\n]' OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move rollback'; END IF;
    SELECT * INTO target FROM public.account_moves WHERE id=p_move_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002',MESSAGE='Account move not found'; END IF;
    IF target.version<>p_expected_version OR target.state<>'switched' OR target.environment<>p_environment OR
       target.lease_id<>p_lease_id OR target.lease_expires_at<=now_at OR target.rollback_expires_at<=now_at THEN
      RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account move rollback conflict'; END IF;
    rollback_gen:=target.destination_generation+1;
    UPDATE public.cells SET assigned_accounts=assigned_accounts-1 WHERE id=target.destination_cell_id AND assigned_accounts>0;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='destination cell capacity underflow'; END IF;
    UPDATE public.cells SET assigned_accounts=assigned_accounts+1 WHERE id=target.source_cell_id AND state IN ('active','draining') AND assigned_accounts<soft_account_limit;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='source cell capacity unavailable'; END IF;
    UPDATE public.accounts SET cell_id=target.source_cell_id,placement_generation=rollback_gen,version=version+1
      WHERE id=target.account_id AND cell_id=target.destination_cell_id AND placement_generation=target.destination_generation;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account placement changed before rollback'; END IF;
    UPDATE public.account_directory SET cell_id=target.source_cell_id,placement_generation=rollback_gen,state='active',
      data_region=(SELECT region FROM public.cells WHERE id=target.source_cell_id),updated_at=now_at WHERE account_id=target.account_id;
    UPDATE public.account_moves SET state='rolled_back',rollback_generation=rollback_gen,rolled_back_at=now_at,
      lease_id=NULL,lease_expires_at=NULL,version=version+1 WHERE id=target.id RETURNING * INTO target;
    INSERT INTO public.account_move_events(move_id,event_id,action,from_state,to_state,move_version,actor,reason,environment,occurred_at)
    VALUES(target.id,p_event_id,'rolled_back','switched','rolled_back',target.version,btrim(p_actor),btrim(p_reason),p_environment,now_at);
    RETURN NEXT target;
END;
$$;

CREATE FUNCTION public.spyglass_finish_account_move(
    p_move_id uuid,p_event_id uuid,p_expected_version bigint,p_lease_id uuid,p_complete boolean,p_actor text,p_reason text,p_environment text
) RETURNS SETOF public.account_moves
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE target public.account_moves; prior text; now_at timestamptz:=statement_timestamp();
BEGIN
    IF p_move_id IS NULL OR p_event_id IS NULL OR p_expected_version<=0 OR p_lease_id IS NULL OR p_complete IS NULL OR
       char_length(btrim(p_actor)) NOT BETWEEN 3 AND 200 OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR
       p_actor ~ E'[\r\n]' OR p_reason ~ E'[\r\n]' OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Account move retirement'; END IF;
    SELECT * INTO target FROM public.account_moves WHERE id=p_move_id FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002',MESSAGE='Account move not found'; END IF;
    IF target.version<>p_expected_version OR target.environment<>p_environment OR target.lease_id<>p_lease_id OR target.lease_expires_at<=now_at THEN
      RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='Account move retirement conflict'; END IF;
    prior:=target.state;
    IF p_complete THEN
      IF target.state<>'retiring' THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='Account move is not retiring'; END IF;
      UPDATE public.account_moves SET state='completed',completed_at=now_at,lease_id=NULL,lease_expires_at=NULL,version=version+1 WHERE id=target.id;
    ELSE
      IF target.state<>'switched' OR target.rollback_expires_at>now_at THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='Account move rollback window remains open'; END IF;
      UPDATE public.account_moves SET state='retiring',retired_at=now_at,version=version+1 WHERE id=target.id;
    END IF;
    SELECT * INTO target FROM public.account_moves WHERE id=p_move_id;
    INSERT INTO public.account_move_events(move_id,event_id,action,from_state,to_state,move_version,actor,reason,environment,occurred_at)
    VALUES(target.id,p_event_id,CASE WHEN p_complete THEN 'completed' ELSE 'retirement_started' END,prior,target.state,target.version,btrim(p_actor),btrim(p_reason),p_environment,now_at);
    RETURN NEXT target;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_reject_account_move_event_mutation() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_capture_account_move_erasure() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_add_account_move_erasure_counts() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_prepare_account_move(uuid,uuid,uuid,text,integer,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_inspect_account_move(uuid,uuid,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_claim_account_move(uuid,uuid,bigint,uuid,integer,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_advance_account_move(uuid,uuid,bigint,uuid,text,text,jsonb,bytea,jsonb,bytea,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_switch_account_move(uuid,uuid,bigint,uuid,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_pause_account_move(uuid,uuid,bigint,boolean,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_rollback_account_move(uuid,uuid,bigint,uuid,text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_finish_account_move(uuid,uuid,bigint,uuid,boolean,text,text,text) FROM PUBLIC;

COMMIT;
