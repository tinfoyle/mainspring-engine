BEGIN;

ALTER TABLE accounts ADD COLUMN last_catalog_reconciled_version bigint;
UPDATE accounts a
SET last_catalog_reconciled_version=COALESCE(
    (SELECT s.catalog_version FROM entitlement_snapshots s WHERE s.account_id=a.id ORDER BY s.version DESC LIMIT 1),
    (SELECT version FROM catalog_publications WHERE state='published' AND published_at<=statement_timestamp() ORDER BY published_at DESC,version DESC LIMIT 1)
);
ALTER TABLE accounts ALTER COLUMN last_catalog_reconciled_version SET NOT NULL;
ALTER TABLE accounts ALTER COLUMN last_catalog_reconciled_version SET DEFAULT 1;
ALTER TABLE accounts ADD CONSTRAINT accounts_reconciled_catalog_fk
    FOREIGN KEY (last_catalog_reconciled_version) REFERENCES catalog_publications (version);

CREATE TABLE entitlement_catalog_rollouts (
    id uuid PRIMARY KEY,
    target_catalog_version bigint NOT NULL REFERENCES catalog_publications (version),
    source text NOT NULL CHECK (source IN ('catalog_publication','drift_repair','migration')),
    state text NOT NULL CHECK (state IN ('pending','seeding','processing','completed','failed')),
    effective_at timestamptz NOT NULL,
    cursor_created_at timestamptz,
    cursor_account_id uuid,
    seeded_count bigint NOT NULL DEFAULT 0 CHECK (seeded_count >= 0),
    completed_count bigint NOT NULL DEFAULT 0 CHECK (completed_count >= 0),
    failed_count bigint NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    created_at timestamptz NOT NULL,
    seeded_at timestamptz,
    completed_at timestamptz,
    last_error_code text,
    CONSTRAINT entitlement_rollout_cursor_pair CHECK ((cursor_created_at IS NULL) = (cursor_account_id IS NULL))
);
CREATE INDEX entitlement_rollouts_seed
    ON entitlement_catalog_rollouts (effective_at,created_at,id)
    WHERE state IN ('pending','seeding');
CREATE UNIQUE INDEX entitlement_rollouts_one_active_catalog
    ON entitlement_catalog_rollouts (target_catalog_version)
    WHERE state IN ('pending','seeding','processing');

CREATE TABLE entitlement_recompute_queue (
    rollout_id uuid NOT NULL REFERENCES entitlement_catalog_rollouts (id),
    account_id uuid NOT NULL REFERENCES accounts (id),
    processing_state text NOT NULL CHECK (processing_state IN ('pending','processing','failed','completed','dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz,
    lease_expires_at timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL,
    completed_at timestamptz,
    PRIMARY KEY (rollout_id,account_id)
);
CREATE INDEX entitlement_recompute_claim
    ON entitlement_recompute_queue (COALESCE(next_attempt_at,created_at),rollout_id,account_id)
    WHERE processing_state IN ('pending','processing','failed');

INSERT INTO entitlement_catalog_rollouts
    (id,target_catalog_version,source,state,effective_at,created_at)
SELECT gen_random_uuid(),version,'migration','pending',statement_timestamp(),statement_timestamp()
FROM catalog_publications
WHERE state='published' AND published_at<=statement_timestamp()
ORDER BY published_at DESC,version DESC LIMIT 1;

COMMIT;
