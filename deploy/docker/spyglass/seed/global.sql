INSERT INTO cells (id, region, state, assigned_accounts, soft_account_limit, created_at, route_origin)
VALUES
  ('cell-us-east-01', 'us-east', 'active', 0, 1000, statement_timestamp(), 'http://app-api-a:8080'),
  ('cell-us-west-01', 'us-west', 'active', 0, 1000, statement_timestamp(), 'http://app-api-b:8080')
ON CONFLICT (id) DO UPDATE
SET region = EXCLUDED.region,
    state = EXCLUDED.state,
    soft_account_limit = EXCLUDED.soft_account_limit,
    route_origin = EXCLUDED.route_origin;
