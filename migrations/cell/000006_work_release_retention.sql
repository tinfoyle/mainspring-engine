BEGIN;

-- Completed technical jobs are pruned in small SKIP LOCKED batches. Operator
-- events have no foreign key to this queue and retain the durable audit trail.
CREATE INDEX work_capacity_release_completed_retention
    ON spyglass.work_capacity_release_queue (completed_at,account_id,work_item_id,reservation_id)
    WHERE processing_state = 'completed';

COMMIT;
