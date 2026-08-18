BEGIN;

CREATE TABLE spyglass.agent_queue_operator_events (
    batch_id uuid NOT NULL,
    event_sequence integer NOT NULL CHECK (event_sequence>=0),
    action text NOT NULL CHECK (action IN ('inspected','requeued')),
    queue_name text NOT NULL CHECK (queue_name IN ('dispatch','projection')),
    account_id uuid,
    invocation_id uuid,
    actor text NOT NULL CHECK (char_length(actor) BETWEEN 1 AND 200 AND actor !~ E'[\r\n]'),
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 8 AND 500 AND reason !~ E'[\r\n]'),
    environment text NOT NULL CHECK (environment ~ '^[a-z][a-z0-9-]{0,99}$'),
    previous_attempt_count integer CHECK (previous_attempt_count IS NULL OR previous_attempt_count>=0),
    previous_error_code text CHECK (previous_error_code IS NULL OR previous_error_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (batch_id,event_sequence),
    FOREIGN KEY (account_id,invocation_id) REFERENCES spyglass.agent_invocations(account_id,id) ON DELETE CASCADE,
    CHECK ((account_id IS NULL AND invocation_id IS NULL) OR (account_id IS NOT NULL AND invocation_id IS NOT NULL)),
    CHECK (action<>'requeued' OR account_id IS NOT NULL)
);
CREATE INDEX agent_queue_operator_events_target
    ON spyglass.agent_queue_operator_events(account_id,invocation_id,created_at DESC);
CREATE INDEX agent_queue_operator_events_time
    ON spyglass.agent_queue_operator_events(created_at DESC,batch_id,event_sequence);

CREATE FUNCTION spyglass.reject_agent_queue_operator_event_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tombstone_owner text;
BEGIN
    SELECT pg_get_userbyid(c.relowner) INTO tombstone_owner
    FROM pg_class c WHERE c.oid='spyglass.account_erasure_tombstones'::regclass;
    IF TG_OP='DELETE' AND OLD.account_id IS NOT NULL AND current_user=tombstone_owner AND
       current_setting('spyglass.erasure_request_id',true) IS NOT NULL AND
       current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'Agent queue operator events are immutable';
END;
$$;
CREATE TRIGGER agent_queue_operator_events_immutable
BEFORE UPDATE OR DELETE ON spyglass.agent_queue_operator_events
FOR EACH ROW EXECUTE FUNCTION spyglass.reject_agent_queue_operator_event_change();

CREATE FUNCTION public.spyglass_inspect_agent_queue_dead_letters(
    p_batch_id uuid,p_queue_name text,p_actor text,p_reason text,p_environment text,p_limit integer
) RETURNS TABLE (
    queue_name text,account_id uuid,invocation_id uuid,attempt_count integer,last_error_code text,
    created_at timestamptz,updated_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE target record; target_sequence integer:=0;
BEGIN
    IF p_batch_id IS NULL OR p_queue_name NOT IN ('dispatch','projection') OR
       p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' OR p_limit NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Agent queue inspection';
    END IF;
    IF p_queue_name='dispatch' THEN
        FOR target IN SELECT q.account_id,q.invocation_id,q.attempt_count,q.last_error_code,q.created_at,q.updated_at
            FROM spyglass.agent_dispatch_queue q WHERE q.state='dead_letter'
            ORDER BY q.created_at,q.account_id,q.invocation_id LIMIT p_limit FOR SHARE
        LOOP
            target_sequence:=target_sequence+1;
            INSERT INTO spyglass.agent_queue_operator_events
                (batch_id,event_sequence,action,queue_name,account_id,invocation_id,actor,reason,environment,previous_attempt_count,previous_error_code,created_at)
            VALUES (p_batch_id,target_sequence,'inspected',p_queue_name,target.account_id,target.invocation_id,btrim(p_actor),btrim(p_reason),p_environment,target.attempt_count,target.last_error_code,statement_timestamp());
            queue_name:=p_queue_name; account_id:=target.account_id; invocation_id:=target.invocation_id;
            attempt_count:=target.attempt_count; last_error_code:=target.last_error_code;
            created_at:=target.created_at; updated_at:=target.updated_at; RETURN NEXT;
        END LOOP;
    ELSE
        FOR target IN SELECT q.account_id,q.invocation_id,q.attempt_count,q.last_error_code,q.created_at,q.updated_at
            FROM spyglass.agent_result_projection_queue q WHERE q.state='dead_letter'
            ORDER BY q.created_at,q.account_id,q.invocation_id LIMIT p_limit FOR SHARE
        LOOP
            target_sequence:=target_sequence+1;
            INSERT INTO spyglass.agent_queue_operator_events
                (batch_id,event_sequence,action,queue_name,account_id,invocation_id,actor,reason,environment,previous_attempt_count,previous_error_code,created_at)
            VALUES (p_batch_id,target_sequence,'inspected',p_queue_name,target.account_id,target.invocation_id,btrim(p_actor),btrim(p_reason),p_environment,target.attempt_count,target.last_error_code,statement_timestamp());
            queue_name:=p_queue_name; account_id:=target.account_id; invocation_id:=target.invocation_id;
            attempt_count:=target.attempt_count; last_error_code:=target.last_error_code;
            created_at:=target.created_at; updated_at:=target.updated_at; RETURN NEXT;
        END LOOP;
    END IF;
    IF target_sequence=0 THEN
        INSERT INTO spyglass.agent_queue_operator_events
            (batch_id,event_sequence,action,queue_name,actor,reason,environment,created_at)
        VALUES (p_batch_id,0,'inspected',p_queue_name,btrim(p_actor),btrim(p_reason),p_environment,statement_timestamp());
    END IF;
END;
$$;

CREATE FUNCTION public.spyglass_requeue_agent_queue_dead_letter(
    p_batch_id uuid,p_queue_name text,p_account_id uuid,p_invocation_id uuid,
    p_actor text,p_reason text,p_environment text
) RETURNS TABLE (
    queue_name text,account_id uuid,invocation_id uuid,previous_attempt_count integer,previous_error_code text,
    created_at timestamptz,updated_at timestamptz,next_attempt_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE target record; requeued_at timestamptz:=statement_timestamp();
BEGIN
    IF p_batch_id IS NULL OR p_queue_name NOT IN ('dispatch','projection') OR p_account_id IS NULL OR p_invocation_id IS NULL OR
       p_actor IS NULL OR char_length(btrim(p_actor)) NOT BETWEEN 1 AND 200 OR p_actor ~ E'[\r\n]' OR
       p_reason IS NULL OR char_length(btrim(p_reason)) NOT BETWEEN 8 AND 500 OR p_reason ~ E'[\r\n]' OR
       p_environment IS NULL OR p_environment !~ '^[a-z][a-z0-9-]{0,99}$' THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid Agent queue requeue';
    END IF;
    IF p_queue_name='dispatch' THEN
        SELECT q.state,q.attempt_count,q.last_error_code,q.created_at,q.updated_at INTO target
        FROM spyglass.agent_dispatch_queue q WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id FOR UPDATE;
    ELSE
        SELECT q.state,q.attempt_count,q.last_error_code,q.created_at,q.updated_at INTO target
        FROM spyglass.agent_result_projection_queue q WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id FOR UPDATE;
    END IF;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='Agent queue job not found'; END IF;
    IF target.state<>'dead_letter' THEN RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='Agent queue job is not dead letter'; END IF;

    INSERT INTO spyglass.agent_queue_operator_events
        (batch_id,event_sequence,action,queue_name,account_id,invocation_id,actor,reason,environment,previous_attempt_count,previous_error_code,created_at)
    VALUES (p_batch_id,0,'requeued',p_queue_name,p_account_id,p_invocation_id,btrim(p_actor),btrim(p_reason),p_environment,target.attempt_count,target.last_error_code,requeued_at);
    IF p_queue_name='dispatch' THEN
        UPDATE spyglass.agent_dispatch_queue AS q SET state='pending',attempt_count=0,next_attempt_at=requeued_at,
            lease_id=NULL,lease_expires_at=NULL,request_digest=NULL,last_error_code=NULL,provisioned_at=NULL,updated_at=requeued_at
        WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id;
    ELSE
        UPDATE spyglass.agent_result_projection_queue AS q SET state='pending',attempt_count=0,next_attempt_at=requeued_at,
            lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL,projected_at=NULL,updated_at=requeued_at
        WHERE q.account_id=p_account_id AND q.invocation_id=p_invocation_id;
    END IF;
    RETURN QUERY SELECT p_queue_name,p_account_id,p_invocation_id,target.attempt_count,target.last_error_code,
        target.created_at,target.updated_at,requeued_at;
END;
$$;

REVOKE ALL ON FUNCTION spyglass.reject_agent_queue_operator_event_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_inspect_agent_queue_dead_letters(uuid,text,text,text,text,integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_requeue_agent_queue_dead_letter(uuid,text,uuid,uuid,text,text,text) FROM PUBLIC;

ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz)
    RENAME TO spyglass_erase_account_cell_without_agent_queue_admin;
ALTER FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea)
    RENAME TO spyglass_replay_account_cell_erasure_without_agent_queue_admin;

CREATE FUNCTION spyglass.add_agent_queue_admin_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.agent_queue_admin_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_erasure_agent_queue_admin_counts BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_agent_queue_admin_erasure_counts();

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
    PERFORM set_config('spyglass.erasure_request_id',p_request_id::text,true);
    PERFORM set_config('spyglass.erasure_account_id',p_account_id::text,true);
    DELETE FROM spyglass.agent_queue_operator_events WHERE account_id=p_account_id;
    GET DIAGNOSTICS event_count=ROW_COUNT;
    PERFORM set_config('spyglass.agent_queue_admin_erasure_counts',jsonb_build_object('agent_queue_operator_events',event_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_erase_account_cell_without_agent_queue_admin(
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
    PERFORM set_config('spyglass.erasure_request_id',p_request_id::text,true);
    PERFORM set_config('spyglass.erasure_account_id',p_account_id::text,true);
    DELETE FROM spyglass.agent_queue_operator_events WHERE account_id=p_account_id;
    GET DIAGNOSTICS event_count=ROW_COUNT;
    PERFORM set_config('spyglass.agent_queue_admin_erasure_counts',jsonb_build_object('agent_queue_operator_events',event_count)::text,true);
    RETURN QUERY SELECT * FROM public.spyglass_replay_account_cell_erasure_without_agent_queue_admin(
        p_request_id,p_account_id,p_restored_placement_generation,p_tombstone_placement_generation,p_account_fingerprint,
        p_policy_version,p_request_version,p_environment,p_erased_at,p_export_sha256,p_operator_evidence_sha256,
        p_backup_expires_at,p_previous_ledger_sequence,p_previous_ledger_root,p_expected_ledger_sequence,p_expected_ledger_root);
END;
$$;

REVOKE ALL ON FUNCTION spyglass.add_agent_queue_admin_erasure_counts() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell_without_agent_queue_admin(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure_without_agent_queue_admin(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;

COMMIT;
