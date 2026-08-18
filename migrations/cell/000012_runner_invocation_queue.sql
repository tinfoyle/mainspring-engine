BEGIN;

-- Runner control is an identifier-only cell control plane. Customer prompts,
-- outputs, credentials, and package data never enter these tables. They are
-- intentionally outside Account RLS so one narrowly permissioned controller
-- can schedule fairly across the shared cell without BYPASSRLS.
CREATE TABLE spyglass.runner_account_scheduling (
    account_id uuid PRIMARY KEY REFERENCES spyglass.account_namespaces (account_id),
    weight integer NOT NULL DEFAULT 1 CHECK (weight BETWEEN 1 AND 100),
    concurrency_limit integer NOT NULL DEFAULT 1 CHECK (concurrency_limit BETWEEN 1 AND 1000),
    active_count integer NOT NULL DEFAULT 0 CHECK (active_count >= 0 AND active_count <= concurrency_limit),
    virtual_finish numeric(30, 12) NOT NULL DEFAULT 0 CHECK (virtual_finish >= 0),
    last_dispatched_at timestamptz,
    updated_at timestamptz NOT NULL
);

CREATE TABLE spyglass.runner_invocation_queue (
    invocation_id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES spyglass.account_namespaces (account_id),
    profile text NOT NULL CHECK (profile ~ '^[a-z][a-z0-9-]{0,49}$'),
    processing_state text NOT NULL DEFAULT 'queued' CHECK (processing_state IN (
        'queued', 'launching', 'launched', 'failed', 'dead_letter',
        'completed', 'execution_failed', 'canceled'
    )),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz,
    lease_id uuid,
    lease_expires_at timestamptz,
    job_name text CHECK (job_name IS NULL OR char_length(job_name) BETWEEN 1 AND 253),
    last_error_code text CHECK (last_error_code IS NULL OR char_length(last_error_code) BETWEEN 1 AND 100),
    queued_at timestamptz NOT NULL,
    last_attempt_at timestamptz,
    launched_at timestamptz,
    completed_at timestamptz,
    CHECK (
        (processing_state = 'launching' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR
        (processing_state <> 'launching' AND lease_id IS NULL AND lease_expires_at IS NULL)
    ),
    CHECK (
        (processing_state IN ('queued', 'failed') AND next_attempt_at IS NOT NULL) OR
        (processing_state NOT IN ('queued', 'failed') AND next_attempt_at IS NULL)
    ),
    CHECK (
        (processing_state IN ('launched', 'completed', 'execution_failed', 'canceled') AND job_name IS NOT NULL) OR
        (processing_state NOT IN ('launched', 'completed', 'execution_failed', 'canceled') AND job_name IS NULL)
    ),
    CHECK (
        (processing_state IN ('completed', 'execution_failed', 'canceled') AND completed_at IS NOT NULL) OR
        (processing_state NOT IN ('completed', 'execution_failed', 'canceled') AND completed_at IS NULL)
    )
);

CREATE INDEX runner_invocation_ready
    ON spyglass.runner_invocation_queue (account_id, next_attempt_at, queued_at, invocation_id)
    WHERE processing_state IN ('queued', 'failed');
CREATE INDEX runner_invocation_expired_launch
    ON spyglass.runner_invocation_queue (lease_expires_at, invocation_id)
    WHERE processing_state = 'launching';
CREATE UNIQUE INDEX runner_invocation_job_name
    ON spyglass.runner_invocation_queue (job_name)
    WHERE job_name IS NOT NULL;
CREATE INDEX runner_invocation_account_state
    ON spyglass.runner_invocation_queue (account_id, processing_state, queued_at);

CREATE FUNCTION public.spyglass_configure_runner_account(
    p_account_id uuid,
    p_weight integer,
    p_concurrency_limit integer,
    p_now timestamptz
) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    namespace_state text;
BEGIN
    IF p_account_id IS NULL OR p_weight IS NULL OR p_weight NOT BETWEEN 1 AND 100 OR
       p_concurrency_limit IS NULL OR p_concurrency_limit NOT BETWEEN 1 AND 1000 OR p_now IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner Account scheduling policy';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT state INTO namespace_state FROM spyglass.account_namespaces WHERE account_id=p_account_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='runner Account namespace not found';
    END IF;
    INSERT INTO spyglass.runner_account_scheduling (account_id,weight,concurrency_limit,updated_at)
    VALUES (p_account_id,p_weight,p_concurrency_limit,p_now)
    ON CONFLICT (account_id) DO UPDATE SET
        weight=EXCLUDED.weight,
        concurrency_limit=EXCLUDED.concurrency_limit,
        updated_at=EXCLUDED.updated_at
    WHERE spyglass.runner_account_scheduling.weight<>EXCLUDED.weight
       OR spyglass.runner_account_scheduling.concurrency_limit<>EXCLUDED.concurrency_limit;
END;
$$;

CREATE FUNCTION public.spyglass_enqueue_runner_invocation(
    p_invocation_id uuid,
    p_account_id uuid,
    p_profile text,
    p_queued_at timestamptz
) RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    namespace_state text;
    inserted_count bigint;
    existing record;
BEGIN
    IF p_invocation_id IS NULL OR p_account_id IS NULL OR p_profile IS NULL OR
       p_profile !~ '^[a-z][a-z0-9-]{0,49}$' OR p_queued_at IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner invocation';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT state INTO namespace_state FROM spyglass.account_namespaces WHERE account_id=p_account_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='runner Account namespace not found';
    END IF;
    IF namespace_state<>'active' THEN
        RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='runner Account namespace does not accept new invocations';
    END IF;
    INSERT INTO spyglass.runner_account_scheduling (account_id,updated_at)
    VALUES (p_account_id,p_queued_at)
    ON CONFLICT (account_id) DO NOTHING;
    INSERT INTO spyglass.runner_invocation_queue
        (invocation_id,account_id,profile,processing_state,next_attempt_at,queued_at)
    VALUES (p_invocation_id,p_account_id,p_profile,'queued',p_queued_at,p_queued_at)
    ON CONFLICT (invocation_id) DO NOTHING;
    GET DIAGNOSTICS inserted_count=ROW_COUNT;
    IF inserted_count=1 THEN
        RETURN true;
    END IF;
    SELECT account_id,profile INTO existing
    FROM spyglass.runner_invocation_queue WHERE invocation_id=p_invocation_id;
    IF NOT FOUND OR existing.account_id<>p_account_id OR existing.profile<>p_profile THEN
        RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='runner invocation identity conflicts with an existing record';
    END IF;
    RETURN false;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_configure_runner_account(uuid,integer,integer,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.spyglass_enqueue_runner_invocation(uuid,uuid,text,timestamptz) FROM PUBLIC;

COMMIT;
