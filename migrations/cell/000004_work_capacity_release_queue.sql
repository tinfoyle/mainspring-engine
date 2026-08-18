BEGIN;

-- This technical outbox contains only routing and reservation identifiers. It
-- is intentionally not Account-RLS scoped so a narrowly permissioned shared
-- reconciler can lease jobs across the cell without BYPASSRLS access to any
-- customer business table.
CREATE TABLE spyglass.work_capacity_release_queue (
    account_id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    reservation_id uuid NOT NULL,
    processing_state text NOT NULL DEFAULT 'pending'
        CHECK (processing_state IN ('pending', 'processing', 'failed', 'completed', 'dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz,
    lease_id uuid,
    lease_expires_at timestamptz,
    last_attempt_at timestamptz,
    last_error_code text CHECK (last_error_code IS NULL OR char_length(last_error_code) BETWEEN 1 AND 100),
    queued_at timestamptz NOT NULL,
    completed_at timestamptz,
    PRIMARY KEY (account_id, work_item_id, reservation_id),
    FOREIGN KEY (account_id, work_item_id) REFERENCES spyglass.work_items (account_id, id),
    CHECK (
        (processing_state = 'processing' AND lease_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR
        (processing_state <> 'processing' AND lease_id IS NULL AND lease_expires_at IS NULL)
    ),
    CHECK ((processing_state = 'completed' AND completed_at IS NOT NULL) OR (processing_state <> 'completed' AND completed_at IS NULL)),
    CHECK (processing_state NOT IN ('pending', 'failed') OR next_attempt_at IS NOT NULL)
);

CREATE INDEX work_capacity_release_claim
    ON spyglass.work_capacity_release_queue (next_attempt_at, queued_at, account_id, work_item_id)
    WHERE processing_state IN ('pending', 'failed');
CREATE INDEX work_capacity_release_expired_lease
    ON spyglass.work_capacity_release_queue (lease_expires_at)
    WHERE processing_state = 'processing';
CREATE INDEX work_capacity_release_dead_letter
    ON spyglass.work_capacity_release_queue (queued_at)
    WHERE processing_state = 'dead_letter';

CREATE FUNCTION spyglass.enqueue_work_capacity_release() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.state IN ('done', 'canceled') AND NEW.capacity_released_at IS NULL THEN
        INSERT INTO spyglass.work_capacity_release_queue
            (account_id, work_item_id, reservation_id, processing_state, next_attempt_at, queued_at)
        VALUES
            (NEW.account_id, NEW.id, NEW.capacity_reservation_id, 'pending', NEW.updated_at, NEW.updated_at)
        ON CONFLICT (account_id, work_item_id, reservation_id) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER work_items_enqueue_capacity_release
AFTER INSERT OR UPDATE OF state, capacity_reservation_id ON spyglass.work_items
FOR EACH ROW EXECUTE FUNCTION spyglass.enqueue_work_capacity_release();

INSERT INTO spyglass.work_capacity_release_queue
    (account_id, work_item_id, reservation_id, processing_state, next_attempt_at, queued_at)
SELECT account_id, id, capacity_reservation_id, 'pending', statement_timestamp(), updated_at
FROM spyglass.work_items
WHERE state IN ('done', 'canceled') AND capacity_released_at IS NULL
ON CONFLICT (account_id, work_item_id, reservation_id) DO NOTHING;

COMMIT;
