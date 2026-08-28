BEGIN;

-- Global Account creation and cell namespace creation cannot share a database
-- transaction.  This durable queue closes that boundary: registration commits
-- the authoritative directory row, and one cell-bounded worker idempotently
-- creates the matching RLS namespace before routed traffic is accepted.
CREATE TABLE account_cell_provision_queue (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    cell_id text NOT NULL REFERENCES cells(id),
    placement_generation bigint NOT NULL CHECK (placement_generation > 0),
    processing_state text NOT NULL DEFAULT 'pending'
        CHECK (processing_state IN ('pending','processing','completed','failed')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    available_at timestamptz NOT NULL,
    lease_expires_at timestamptz,
    completed_at timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK ((processing_state='processing')=(lease_expires_at IS NOT NULL)),
    CHECK ((processing_state='completed')=(completed_at IS NOT NULL)),
    CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,63}$')
);

CREATE INDEX account_cell_provision_queue_claim
    ON account_cell_provision_queue(cell_id,available_at,account_id)
    WHERE processing_state IN ('pending','failed');

CREATE OR REPLACE FUNCTION spyglass_queue_account_cell_provision() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
    IF NEW.state <> 'active' THEN
        RETURN NEW;
    END IF;
    INSERT INTO account_cell_provision_queue
        (account_id,cell_id,placement_generation,processing_state,available_at,created_at,updated_at)
    VALUES (NEW.account_id,NEW.cell_id,NEW.placement_generation,'pending',NEW.updated_at,NEW.updated_at,NEW.updated_at)
    ON CONFLICT (account_id) DO UPDATE SET
        cell_id=EXCLUDED.cell_id,
        placement_generation=EXCLUDED.placement_generation,
        processing_state=CASE
            WHEN account_cell_provision_queue.cell_id=EXCLUDED.cell_id AND
                 account_cell_provision_queue.placement_generation=EXCLUDED.placement_generation AND
                 account_cell_provision_queue.processing_state='completed'
            THEN 'completed' ELSE 'pending' END,
        available_at=LEAST(account_cell_provision_queue.available_at,EXCLUDED.available_at),
        lease_expires_at=NULL,
        completed_at=CASE
            WHEN account_cell_provision_queue.cell_id=EXCLUDED.cell_id AND
                 account_cell_provision_queue.placement_generation=EXCLUDED.placement_generation AND
                 account_cell_provision_queue.processing_state='completed'
            THEN account_cell_provision_queue.completed_at ELSE NULL END,
        last_error_code=NULL,
        updated_at=EXCLUDED.updated_at;
    RETURN NEW;
END;
$$;

CREATE TRIGGER account_directory_cell_provision
AFTER INSERT ON account_directory
FOR EACH ROW EXECUTE FUNCTION spyglass_queue_account_cell_provision();

INSERT INTO account_cell_provision_queue
    (account_id,cell_id,placement_generation,processing_state,available_at,created_at,updated_at)
SELECT account_id,cell_id,placement_generation,'pending',updated_at,updated_at,updated_at
FROM account_directory WHERE state='active'
ON CONFLICT (account_id) DO NOTHING;

REVOKE ALL ON FUNCTION spyglass_queue_account_cell_provision() FROM PUBLIC;

COMMIT;
