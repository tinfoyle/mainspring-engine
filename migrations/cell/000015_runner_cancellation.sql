BEGIN;

-- Cancellation is an explicit, durable state transition. A queued invocation
-- can finish without ever creating a Kubernetes Job, while an invocation that
-- may have crossed the Kubernetes API boundary must retain Account capacity
-- until the exact Job has been observed as deleted. The same fail-closed rule
-- gives an ambiguous create response its own reconcilable state.
ALTER TABLE spyglass.runner_invocation_queue
    ADD COLUMN cancel_requested_at timestamptz;

UPDATE spyglass.runner_invocation_queue
SET cancel_requested_at=COALESCE(completed_at,statement_timestamp())
WHERE processing_state='canceled';

ALTER TABLE spyglass.runner_invocation_queue
    DROP CONSTRAINT runner_invocation_queue_processing_state_check,
    DROP CONSTRAINT runner_invocation_queue_check2,
    DROP CONSTRAINT runner_invocation_inspection_state;

DROP INDEX spyglass.runner_invocation_inspection_due;

ALTER TABLE spyglass.runner_invocation_queue
    ADD CONSTRAINT runner_invocation_queue_processing_state_check CHECK (processing_state IN (
        'queued', 'launching', 'launch_uncertain', 'launched', 'canceling', 'failed', 'dead_letter',
        'completed', 'execution_failed', 'canceled'
    )),
    ADD CONSTRAINT runner_invocation_job_state CHECK (
        (processing_state IN ('launch_uncertain','launched','canceling','completed','execution_failed') AND job_name IS NOT NULL) OR
        (processing_state='canceled') OR
        (processing_state NOT IN ('launch_uncertain','launched','canceling','completed','execution_failed','canceled') AND job_name IS NULL)
    ),
    ADD CONSTRAINT runner_invocation_cancellation_state CHECK (
        (processing_state IN ('canceling','canceled') AND cancel_requested_at IS NOT NULL) OR
        (processing_state='launching') OR
        (processing_state NOT IN ('launching','canceling','canceled') AND cancel_requested_at IS NULL)
    ),
    ADD CONSTRAINT runner_invocation_inspection_state CHECK (
        (processing_state IN ('launch_uncertain','launched','canceling') AND next_inspection_at IS NOT NULL) OR
        (processing_state NOT IN ('launch_uncertain','launched','canceling') AND next_inspection_at IS NULL)
    );

CREATE INDEX runner_invocation_inspection_due
    ON spyglass.runner_invocation_queue (next_inspection_at,launched_at,invocation_id)
    WHERE processing_state IN ('launch_uncertain','launched','canceling');

-- The serving-side producer may request cancellation but still cannot read or
-- mutate the identifier-only queue directly. The Account predicate deliberately
-- makes an unknown invocation and another Account's invocation indistinguishable.
CREATE FUNCTION public.spyglass_cancel_runner_invocation(
    p_invocation_id uuid,
    p_account_id uuid,
    p_now timestamptz
) RETURNS text
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    current_state text;
BEGIN
    IF p_invocation_id IS NULL OR p_account_id IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid runner cancellation request';
    END IF;
    PERFORM set_config('app.account_id',p_account_id::text,true);
    SELECT processing_state INTO current_state
    FROM spyglass.runner_invocation_queue
    WHERE invocation_id=p_invocation_id AND account_id=p_account_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='P0002', MESSAGE='runner invocation not found';
    END IF;

    CASE current_state
        WHEN 'queued', 'failed', 'dead_letter' THEN
            UPDATE spyglass.runner_invocation_queue SET
                processing_state='canceled',
                next_attempt_at=NULL,
                cancel_requested_at=COALESCE(cancel_requested_at,p_now),
                completed_at=p_now,
                last_error_code=NULL
            WHERE invocation_id=p_invocation_id;
            current_state := 'canceled';
        WHEN 'launching' THEN
            UPDATE spyglass.runner_invocation_queue SET
                cancel_requested_at=COALESCE(cancel_requested_at,p_now)
            WHERE invocation_id=p_invocation_id;
        WHEN 'launched' THEN
            UPDATE spyglass.runner_invocation_queue SET
                processing_state='canceling',
                cancel_requested_at=COALESCE(cancel_requested_at,p_now),
                next_inspection_at=p_now
            WHERE invocation_id=p_invocation_id;
            current_state := 'canceling';
        WHEN 'launch_uncertain' THEN
            UPDATE spyglass.runner_invocation_queue SET
                processing_state='canceling',
                cancel_requested_at=COALESCE(cancel_requested_at,p_now),
                next_inspection_at=p_now
            WHERE invocation_id=p_invocation_id;
            current_state := 'canceling';
        ELSE
            -- canceling/canceled are idempotent. A terminal completion wins if
            -- it committed before this request acquired the row lock.
            NULL;
    END CASE;
    RETURN current_state;
END;
$$;

REVOKE ALL ON FUNCTION public.spyglass_cancel_runner_invocation(uuid,uuid,timestamptz) FROM PUBLIC;

COMMIT;
