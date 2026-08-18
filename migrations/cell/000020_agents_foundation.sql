BEGIN;

-- Agents is customer-owned cell data. Mutable identities are separated from
-- immutable persona versions and run plans so retries never silently acquire
-- edited prompts, tools, model policy, or commercial authority.
CREATE TABLE spyglass.agent_boardrooms (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 160),
    purpose text NOT NULL CHECK (char_length(purpose)<=2000),
    state text NOT NULL CHECK (state IN ('active','archived')),
    version bigint NOT NULL CHECK (version>0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id) REFERENCES spyglass.account_namespaces(account_id) ON DELETE CASCADE
);

CREATE TABLE spyglass.agent_personas (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    boardroom_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('active','inactive','archived')),
    latest_version bigint NOT NULL DEFAULT 0 CHECK (latest_version>=0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,boardroom_id) REFERENCES spyglass.agent_boardrooms(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.agent_persona_versions (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    persona_id uuid NOT NULL,
    version bigint NOT NULL CHECK (version>0),
    name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 120),
    role text NOT NULL CHECK (char_length(role) BETWEEN 2 AND 160),
    description text NOT NULL CHECK (char_length(description)<=4000),
    system_instructions text NOT NULL CHECK (char_length(system_instructions) BETWEEN 20 AND 32768),
    policy jsonb NOT NULL CHECK (jsonb_typeof(policy)='object' AND octet_length(policy::text)<=131072),
    content_digest bytea NOT NULL CHECK (octet_length(content_digest)=32),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,persona_id,version),
    UNIQUE (account_id,persona_id,id,content_digest),
    FOREIGN KEY (account_id,persona_id) REFERENCES spyglass.agent_personas(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.agent_conversations (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    boardroom_id uuid NOT NULL,
    subject text NOT NULL CHECK (char_length(subject) BETWEEN 2 AND 240),
    state text NOT NULL CHECK (state IN ('open','closed')),
    next_message_sequence bigint NOT NULL DEFAULT 1 CHECK (next_message_sequence>0),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at>=created_at),
    PRIMARY KEY (account_id,id),
    FOREIGN KEY (account_id,boardroom_id) REFERENCES spyglass.agent_boardrooms(account_id,id) ON DELETE CASCADE
);

CREATE TABLE spyglass.agent_runs (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    boardroom_id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('planned','running','succeeded','partially_failed','failed','canceled')),
    entitlement_version bigint NOT NULL CHECK (entitlement_version>0),
    policy_version bigint NOT NULL CHECK (policy_version>0),
    plan_digest bytea NOT NULL CHECK (octet_length(plan_digest)=32),
    turn_count integer NOT NULL CHECK (turn_count BETWEEN 1 AND 32),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,id,conversation_id),
    FOREIGN KEY (account_id,boardroom_id) REFERENCES spyglass.agent_boardrooms(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,conversation_id) REFERENCES spyglass.agent_conversations(account_id,id) ON DELETE CASCADE,
    CHECK ((state='planned' AND started_at IS NULL AND completed_at IS NULL) OR
           (state='running' AND started_at IS NOT NULL AND completed_at IS NULL) OR
           (state IN ('succeeded','partially_failed','failed','canceled') AND completed_at IS NOT NULL))
);

CREATE TABLE spyglass.agent_run_plan_turns (
    account_id uuid NOT NULL,
    run_id uuid NOT NULL,
    turn integer NOT NULL CHECK (turn BETWEEN 1 AND 32),
    persona_id uuid NOT NULL,
    persona_version_id uuid NOT NULL,
    persona_digest bytea NOT NULL CHECK (octet_length(persona_digest)=32),
    PRIMARY KEY (account_id,run_id,turn),
    UNIQUE (account_id,run_id,persona_id),
    UNIQUE (account_id,run_id,persona_version_id),
    UNIQUE (account_id,run_id,turn,persona_version_id),
    FOREIGN KEY (account_id,run_id) REFERENCES spyglass.agent_runs(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,persona_id,persona_version_id,persona_digest)
        REFERENCES spyglass.agent_persona_versions(account_id,persona_id,id,content_digest)
);

CREATE TABLE spyglass.agent_invocations (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    run_id uuid NOT NULL,
    turn integer NOT NULL CHECK (turn BETWEEN 1 AND 32),
    persona_version_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('queued','running','succeeded','failed','canceled')),
    expected_provider text NOT NULL CHECK (expected_provider ~ '^[a-z][a-z0-9-]{0,31}$'),
    requested_model text NOT NULL CHECK (requested_model ~ '^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$'),
    response_model text CHECK (response_model IS NULL OR response_model ~ '^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$'),
    provider_response_id text CHECK (provider_response_id IS NULL OR (char_length(provider_response_id) BETWEEN 1 AND 200 AND provider_response_id !~ '[[:space:][:cntrl:]]')),
    runner_result_digest bytea CHECK (runner_result_digest IS NULL OR octet_length(runner_result_digest)=32),
    result_digest bytea CHECK (result_digest IS NULL OR octet_length(result_digest)=32),
    result_payload jsonb CHECK (result_payload IS NULL OR (jsonb_typeof(result_payload)='object' AND octet_length(result_payload::text)<=262144)),
    input_tokens bigint CHECK (input_tokens IS NULL OR input_tokens>=0),
    output_tokens bigint CHECK (output_tokens IS NULL OR output_tokens>=0),
    total_tokens bigint CHECK (total_tokens IS NULL OR total_tokens>=0),
    failure_code text CHECK (failure_code IS NULL OR failure_code ~ '^[a-z][a-z0-9_]{0,99}$'),
    queued_at timestamptz NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,run_id,turn),
    UNIQUE (account_id,id,run_id),
    FOREIGN KEY (account_id,run_id,turn,persona_version_id)
        REFERENCES spyglass.agent_run_plan_turns(account_id,run_id,turn,persona_version_id),
    CHECK ((status='queued' AND started_at IS NULL AND completed_at IS NULL) OR
           (status='running' AND started_at IS NOT NULL AND completed_at IS NULL) OR
           (status IN ('succeeded','failed','canceled') AND completed_at IS NOT NULL)),
    CHECK ((status='succeeded' AND response_model IS NOT NULL AND provider_response_id IS NOT NULL AND runner_result_digest IS NOT NULL AND
            result_digest IS NOT NULL AND result_payload IS NOT NULL AND input_tokens IS NOT NULL AND output_tokens IS NOT NULL AND
            total_tokens=input_tokens+output_tokens AND failure_code IS NULL) OR
           (status IN ('queued','running') AND response_model IS NULL AND provider_response_id IS NULL AND runner_result_digest IS NULL AND
            result_digest IS NULL AND result_payload IS NULL AND input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL AND failure_code IS NULL) OR
	       (status='failed' AND response_model IS NULL AND provider_response_id IS NULL AND runner_result_digest IS NOT NULL AND
	        result_digest IS NULL AND result_payload IS NULL AND input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL AND failure_code IS NOT NULL) OR
	       (status='canceled' AND response_model IS NULL AND provider_response_id IS NULL AND
            result_digest IS NULL AND result_payload IS NULL AND input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL AND failure_code IS NOT NULL))
);

CREATE TABLE spyglass.agent_messages (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    run_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    sequence bigint NOT NULL CHECK (sequence>0),
    role text NOT NULL CHECK (role='persona'),
    persona_version_id uuid NOT NULL,
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 65536),
    structured_result jsonb NOT NULL CHECK (jsonb_typeof(structured_result)='object' AND octet_length(structured_result::text)<=262144),
    result_digest bytea NOT NULL CHECK (octet_length(result_digest)=32),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (account_id,conversation_id,sequence),
    UNIQUE (account_id,invocation_id),
    FOREIGN KEY (account_id,conversation_id) REFERENCES spyglass.agent_conversations(account_id,id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,run_id,conversation_id) REFERENCES spyglass.agent_runs(account_id,id,conversation_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,invocation_id,run_id) REFERENCES spyglass.agent_invocations(account_id,id,run_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id,persona_version_id) REFERENCES spyglass.agent_persona_versions(account_id,id)
);

CREATE INDEX agent_boardrooms_state ON spyglass.agent_boardrooms(account_id,state,updated_at,id);
CREATE INDEX agent_conversations_boardroom ON spyglass.agent_conversations(account_id,boardroom_id,updated_at DESC,id);
CREATE INDEX agent_runs_conversation ON spyglass.agent_runs(account_id,conversation_id,created_at DESC,id);
CREATE INDEX agent_invocations_status ON spyglass.agent_invocations(account_id,status,queued_at,id);
CREATE INDEX agent_messages_conversation ON spyglass.agent_messages(account_id,conversation_id,sequence);

ALTER TABLE spyglass.agent_boardrooms ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_boardrooms FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_personas ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_personas FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_persona_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_persona_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_conversations FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_runs FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_run_plan_turns ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_run_plan_turns FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_invocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_invocations FORCE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE spyglass.agent_messages FORCE ROW LEVEL SECURITY;

CREATE POLICY agent_boardrooms_isolation ON spyglass.agent_boardrooms USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY agent_personas_isolation ON spyglass.agent_personas USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY agent_persona_versions_isolation ON spyglass.agent_persona_versions USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY agent_conversations_isolation ON spyglass.agent_conversations USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY agent_runs_isolation ON spyglass.agent_runs USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY agent_run_plan_turns_isolation ON spyglass.agent_run_plan_turns USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY agent_invocations_isolation ON spyglass.agent_invocations USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);
CREATE POLICY agent_messages_isolation ON spyglass.agent_messages USING (account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK (account_id=nullif(current_setting('app.account_id',true),'')::uuid);

-- Projection is execute-only. It binds the plaintext projection to the exact
-- encrypted runner result digest already accepted from the bound Pod and
-- commits invocation success plus exactly one message in one transaction.
CREATE FUNCTION public.spyglass_project_agent_invocation_success(
    p_account_id uuid,p_invocation_id uuid,p_message_id uuid,p_provider text,p_response_model text,p_provider_response_id text,
    p_runner_result_digest bytea,p_result_digest bytea,p_result_payload jsonb,p_body text,
    p_input_tokens bigint,p_output_tokens bigint,p_total_tokens bigint,p_completed_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE invocation_row record; exchange_row record; run_row record; assigned_sequence bigint; existing_message record;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_message_id IS NULL OR p_provider IS NULL OR
       p_provider !~ '^[a-z][a-z0-9-]{0,31}$' OR p_response_model IS NULL OR p_response_model !~ '^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$' OR
       p_provider_response_id IS NULL OR char_length(p_provider_response_id) NOT BETWEEN 1 AND 200 OR p_provider_response_id ~ '[[:space:][:cntrl:]]' OR
       p_runner_result_digest IS NULL OR octet_length(p_runner_result_digest)<>32 OR p_result_digest IS NULL OR octet_length(p_result_digest)<>32 OR
       p_result_payload IS NULL OR jsonb_typeof(p_result_payload)<>'object' OR octet_length(p_result_payload::text)>262144 OR
       p_body IS NULL OR char_length(btrim(p_body)) NOT BETWEEN 1 AND 65536 OR p_result_payload->>'contribution' IS DISTINCT FROM btrim(p_body) OR
       p_input_tokens IS NULL OR p_input_tokens<0 OR p_output_tokens IS NULL OR p_output_tokens<0 OR
       p_total_tokens IS NULL OR p_total_tokens<>p_input_tokens+p_output_tokens OR p_completed_at IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent invocation success projection';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT i.* INTO invocation_row FROM spyglass.agent_invocations i
    WHERE i.account_id=p_account_id AND i.id=p_invocation_id FOR UPDATE;
    IF NOT FOUND OR invocation_row.expected_provider<>p_provider THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent invocation projection identity denied';
    END IF;
    SELECT x.result_outcome,x.result_digest INTO exchange_row FROM spyglass.runner_invocation_exchanges x
    WHERE x.account_id=p_account_id AND x.invocation_id=p_invocation_id FOR SHARE;
    IF NOT FOUND OR exchange_row.result_outcome<>'completed' OR exchange_row.result_digest<>p_runner_result_digest THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent invocation runner result binding denied';
    END IF;
    IF invocation_row.status='succeeded' THEN
        SELECT m.id,m.result_digest INTO existing_message FROM spyglass.agent_messages m
        WHERE m.account_id=p_account_id AND m.invocation_id=p_invocation_id;
        IF FOUND AND existing_message.id=p_message_id AND existing_message.result_digest=p_result_digest AND
           invocation_row.runner_result_digest=p_runner_result_digest AND invocation_row.result_digest=p_result_digest AND
           invocation_row.provider_response_id=p_provider_response_id THEN RETURN false; END IF;
        RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent invocation success projection conflicts with existing result';
    END IF;
    IF invocation_row.status NOT IN ('queued','running') OR p_completed_at<invocation_row.queued_at THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent invocation cannot be projected from its current state';
    END IF;
    SELECT r.conversation_id INTO run_row FROM spyglass.agent_runs r
    WHERE r.account_id=p_account_id AND r.id=invocation_row.run_id FOR SHARE;
    UPDATE spyglass.agent_conversations c SET next_message_sequence=c.next_message_sequence+1,updated_at=GREATEST(c.updated_at,p_completed_at)
    WHERE c.account_id=p_account_id AND c.id=run_row.conversation_id
    RETURNING c.next_message_sequence-1 INTO assigned_sequence;
    IF assigned_sequence IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent conversation projection target missing'; END IF;
    UPDATE spyglass.agent_invocations SET status='succeeded',response_model=p_response_model,provider_response_id=p_provider_response_id,
        runner_result_digest=p_runner_result_digest,result_digest=p_result_digest,result_payload=p_result_payload,
        input_tokens=p_input_tokens,output_tokens=p_output_tokens,total_tokens=p_total_tokens,completed_at=p_completed_at
    WHERE account_id=p_account_id AND id=p_invocation_id;
    INSERT INTO spyglass.agent_messages
        (account_id,id,conversation_id,run_id,invocation_id,sequence,role,persona_version_id,body,structured_result,result_digest,created_at)
    VALUES
        (p_account_id,p_message_id,run_row.conversation_id,invocation_row.run_id,p_invocation_id,assigned_sequence,'persona',
         invocation_row.persona_version_id,btrim(p_body),p_result_payload,p_result_digest,p_completed_at);
    UPDATE spyglass.agent_runs r SET state=CASE
            WHEN (SELECT count(*) FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.run_id=r.id)=r.turn_count
             AND NOT EXISTS (SELECT 1 FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.run_id=r.id AND i.status<>'succeeded')
            THEN 'succeeded' ELSE 'running' END,
        started_at=COALESCE(r.started_at,p_completed_at),
        completed_at=CASE WHEN (SELECT count(*) FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.run_id=r.id)=r.turn_count
             AND NOT EXISTS (SELECT 1 FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.run_id=r.id AND i.status<>'succeeded')
            THEN p_completed_at ELSE NULL END
    WHERE r.account_id=p_account_id AND r.id=invocation_row.run_id;
    RETURN true;
END;
$$;

CREATE FUNCTION public.spyglass_project_agent_invocation_failure(
    p_account_id uuid,p_invocation_id uuid,p_runner_result_digest bytea,p_failure_code text,p_completed_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE invocation_row record; exchange_row record; succeeded_count bigint; invocation_count bigint; unfinished_count bigint; terminal_state text;
BEGIN
    IF p_account_id IS NULL OR p_invocation_id IS NULL OR p_runner_result_digest IS NULL OR octet_length(p_runner_result_digest)<>32 OR
       p_failure_code IS NULL OR p_failure_code !~ '^[a-z][a-z0-9_]{0,99}$' OR p_completed_at IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid agent invocation failure projection';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT i.* INTO invocation_row FROM spyglass.agent_invocations i WHERE i.account_id=p_account_id AND i.id=p_invocation_id FOR UPDATE;
    SELECT x.result_outcome,x.result_digest INTO exchange_row FROM spyglass.runner_invocation_exchanges x
    WHERE x.account_id=p_account_id AND x.invocation_id=p_invocation_id FOR SHARE;
    IF invocation_row.id IS NULL OR exchange_row.result_outcome<>'execution_failed' OR exchange_row.result_digest<>p_runner_result_digest THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='agent invocation failure binding denied';
    END IF;
    IF invocation_row.status='failed' THEN
        IF invocation_row.runner_result_digest=p_runner_result_digest AND invocation_row.failure_code=p_failure_code THEN RETURN false; END IF;
        RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='agent invocation failure projection conflicts with existing result';
    END IF;
    IF invocation_row.status NOT IN ('queued','running') OR p_completed_at<invocation_row.queued_at THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='agent invocation cannot be failed from its current state';
    END IF;
	UPDATE spyglass.agent_invocations SET status='failed',runner_result_digest=p_runner_result_digest,failure_code=p_failure_code,completed_at=p_completed_at
	WHERE account_id=p_account_id AND id=p_invocation_id;
	SELECT count(*) INTO succeeded_count FROM spyglass.agent_invocations WHERE account_id=p_account_id AND run_id=invocation_row.run_id AND status='succeeded';
	SELECT count(*),count(*) FILTER (WHERE status IN ('queued','running')) INTO invocation_count,unfinished_count
	FROM spyglass.agent_invocations WHERE account_id=p_account_id AND run_id=invocation_row.run_id;
	SELECT CASE WHEN succeeded_count>0 THEN 'partially_failed' ELSE 'failed' END INTO terminal_state;
	UPDATE spyglass.agent_runs SET state=CASE WHEN invocation_count=turn_count AND unfinished_count=0 THEN terminal_state ELSE 'running' END,
	    started_at=COALESCE(started_at,p_completed_at),completed_at=CASE WHEN invocation_count=turn_count AND unfinished_count=0 THEN p_completed_at ELSE NULL END
	WHERE account_id=p_account_id AND id=invocation_row.run_id;
    RETURN true;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_project_agent_invocation_success(uuid,uuid,uuid,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_project_agent_invocation_failure(uuid,uuid,bytea,text,timestamptz) FROM PUBLIC;

-- Extend Account erasure/replay without changing their stable public ABI.
ALTER FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz)
    RENAME TO spyglass_erase_account_cell_without_agents;
ALTER FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea)
    RENAME TO spyglass_replay_account_cell_erasure_without_agents;

CREATE FUNCTION spyglass.add_agent_erasure_counts() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,spyglass
AS $$
DECLARE counts text;
BEGIN
    counts:=current_setting('spyglass.agent_erasure_counts',true);
    IF counts IS NOT NULL AND counts<>'' THEN NEW.row_counts:=NEW.row_counts||counts::jsonb; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_erasure_agent_counts BEFORE INSERT ON spyglass.account_erasure_tombstones
FOR EACH ROW EXECUTE FUNCTION spyglass.add_agent_erasure_counts();

CREATE FUNCTION public.spyglass_erase_account_cell(
    p_request_id uuid,p_account_id uuid,p_placement_generation bigint,p_account_fingerprint bytea,
    p_policy_version bigint,p_request_version bigint,p_environment text,p_export_sha256 bytea,
    p_operator_evidence_sha256 bytea,p_backup_expires_at timestamptz
) RETURNS SETOF spyglass.account_erasure_tombstones
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public
AS $$
DECLARE boardroom_count bigint; persona_count bigint; version_count bigint; conversation_count bigint;
        run_count bigint; turn_count bigint; invocation_count bigint; message_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT count(*) INTO boardroom_count FROM spyglass.agent_boardrooms WHERE account_id=p_account_id;
    SELECT count(*) INTO persona_count FROM spyglass.agent_personas WHERE account_id=p_account_id;
    SELECT count(*) INTO version_count FROM spyglass.agent_persona_versions WHERE account_id=p_account_id;
    SELECT count(*) INTO conversation_count FROM spyglass.agent_conversations WHERE account_id=p_account_id;
    SELECT count(*) INTO run_count FROM spyglass.agent_runs WHERE account_id=p_account_id;
    SELECT count(*) INTO turn_count FROM spyglass.agent_run_plan_turns WHERE account_id=p_account_id;
    SELECT count(*) INTO invocation_count FROM spyglass.agent_invocations WHERE account_id=p_account_id;
    SELECT count(*) INTO message_count FROM spyglass.agent_messages WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.agent_erasure_counts',jsonb_build_object(
        'agent_boardrooms',boardroom_count,'agent_personas',persona_count,'agent_persona_versions',version_count,
        'agent_conversations',conversation_count,'agent_runs',run_count,'agent_run_plan_turns',turn_count,
        'agent_invocations',invocation_count,'agent_messages',message_count)::text,true);
    DELETE FROM spyglass.agent_messages WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_invocations WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_run_plan_turns WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_runs WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_conversations WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_persona_versions WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_personas WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_boardrooms WHERE account_id=p_account_id;
    RETURN QUERY SELECT * FROM public.spyglass_erase_account_cell_without_agents(
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
DECLARE boardroom_count bigint; persona_count bigint; version_count bigint; conversation_count bigint;
        run_count bigint; turn_count bigint; invocation_count bigint; message_count bigint;
BEGIN
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT count(*) INTO boardroom_count FROM spyglass.agent_boardrooms WHERE account_id=p_account_id;
    SELECT count(*) INTO persona_count FROM spyglass.agent_personas WHERE account_id=p_account_id;
    SELECT count(*) INTO version_count FROM spyglass.agent_persona_versions WHERE account_id=p_account_id;
    SELECT count(*) INTO conversation_count FROM spyglass.agent_conversations WHERE account_id=p_account_id;
    SELECT count(*) INTO run_count FROM spyglass.agent_runs WHERE account_id=p_account_id;
    SELECT count(*) INTO turn_count FROM spyglass.agent_run_plan_turns WHERE account_id=p_account_id;
    SELECT count(*) INTO invocation_count FROM spyglass.agent_invocations WHERE account_id=p_account_id;
    SELECT count(*) INTO message_count FROM spyglass.agent_messages WHERE account_id=p_account_id;
    PERFORM set_config('spyglass.agent_erasure_counts',jsonb_build_object(
        'agent_boardrooms',boardroom_count,'agent_personas',persona_count,'agent_persona_versions',version_count,
        'agent_conversations',conversation_count,'agent_runs',run_count,'agent_run_plan_turns',turn_count,
        'agent_invocations',invocation_count,'agent_messages',message_count)::text,true);
    DELETE FROM spyglass.agent_messages WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_invocations WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_run_plan_turns WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_runs WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_conversations WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_persona_versions WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_personas WHERE account_id=p_account_id;
    DELETE FROM spyglass.agent_boardrooms WHERE account_id=p_account_id;
    RETURN QUERY SELECT * FROM public.spyglass_replay_account_cell_erasure_without_agents(
        p_request_id,p_account_id,p_restored_placement_generation,p_tombstone_placement_generation,p_account_fingerprint,
        p_policy_version,p_request_version,p_environment,p_erased_at,p_export_sha256,p_operator_evidence_sha256,
        p_backup_expires_at,p_previous_ledger_sequence,p_previous_ledger_root,p_expected_ledger_sequence,p_expected_ledger_root);
END;
$$;

REVOKE ALL ON FUNCTION spyglass.add_agent_erasure_counts() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell_without_agents(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure_without_agents(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_erase_account_cell(uuid,uuid,bigint,bytea,bigint,bigint,text,bytea,bytea,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_replay_account_cell_erasure(uuid,uuid,bigint,bigint,bytea,bigint,bigint,text,timestamptz,bytea,bytea,timestamptz,bigint,bytea,bigint,bytea) FROM PUBLIC;

COMMIT;
