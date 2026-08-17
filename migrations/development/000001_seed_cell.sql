BEGIN;

INSERT INTO cells (id, region, state, assigned_accounts, soft_account_limit, created_at)
VALUES ('cell-us-east-01', 'us-east', 'active', 0, 1000, statement_timestamp())
ON CONFLICT (id) DO NOTHING;

COMMIT;
