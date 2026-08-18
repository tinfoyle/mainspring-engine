BEGIN;

-- This queue carries only Account identifiers and cleanup timing. A narrow
-- cell worker may coordinate across Accounts here, but receipt deletion still
-- occurs inside transaction-local Account RLS.
CREATE TABLE spyglass.route_context_receipt_cleanup_queue (
    account_id uuid PRIMARY KEY REFERENCES spyglass.account_namespaces (account_id),
    next_cleanup_at timestamptz NOT NULL,
    schedule_version bigint NOT NULL DEFAULT 1 CHECK (schedule_version > 0),
    lease_id uuid,
    lease_expires_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_attempt_at timestamptz,
    last_error_code text CHECK (last_error_code IS NULL OR char_length(last_error_code) BETWEEN 1 AND 100),
    updated_at timestamptz NOT NULL,
    CHECK ((lease_id IS NULL) = (lease_expires_at IS NULL))
);
CREATE INDEX route_context_receipt_cleanup_due
    ON spyglass.route_context_receipt_cleanup_queue
    (next_cleanup_at,account_id);
CREATE INDEX route_context_receipt_cleanup_expired_lease
    ON spyglass.route_context_receipt_cleanup_queue
    (lease_expires_at,account_id)
    WHERE lease_id IS NOT NULL;

CREATE FUNCTION spyglass.schedule_route_context_receipt_cleanup() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, spyglass
AS $$
BEGIN
    INSERT INTO spyglass.route_context_receipt_cleanup_queue
        (account_id,next_cleanup_at,schedule_version,updated_at)
    VALUES
        (NEW.account_id,NEW.expires_at + interval '1 minute',1,NEW.consumed_at)
    ON CONFLICT (account_id) DO UPDATE SET
        next_cleanup_at=LEAST(spyglass.route_context_receipt_cleanup_queue.next_cleanup_at,EXCLUDED.next_cleanup_at),
        schedule_version=spyglass.route_context_receipt_cleanup_queue.schedule_version+1,
        updated_at=EXCLUDED.updated_at;
    RETURN NEW;
END;
$$;

CREATE TRIGGER route_context_receipts_schedule_cleanup
AFTER INSERT ON spyglass.route_context_receipts
FOR EACH ROW EXECUTE FUNCTION spyglass.schedule_route_context_receipt_cleanup();

INSERT INTO spyglass.route_context_receipt_cleanup_queue
    (account_id,next_cleanup_at,schedule_version,updated_at)
SELECT account_id,min(expires_at) + interval '1 minute',1,statement_timestamp()
FROM spyglass.route_context_receipts
GROUP BY account_id
ON CONFLICT (account_id) DO NOTHING;

REVOKE ALL ON FUNCTION spyglass.schedule_route_context_receipt_cleanup() FROM PUBLIC;

COMMIT;
