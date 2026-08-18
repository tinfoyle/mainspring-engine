BEGIN;

ALTER TABLE spyglass.runner_invocation_queue
    ADD COLUMN next_inspection_at timestamptz;

UPDATE spyglass.runner_invocation_queue
SET next_inspection_at=COALESCE(launched_at,statement_timestamp())
WHERE processing_state='launched';

ALTER TABLE spyglass.runner_invocation_queue
    ADD CONSTRAINT runner_invocation_inspection_state CHECK (
        (processing_state='launched' AND next_inspection_at IS NOT NULL) OR
        (processing_state<>'launched' AND next_inspection_at IS NULL)
    );

CREATE INDEX runner_invocation_inspection_due
    ON spyglass.runner_invocation_queue (next_inspection_at,launched_at,invocation_id)
    WHERE processing_state='launched';

COMMIT;
