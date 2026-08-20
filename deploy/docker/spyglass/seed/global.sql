INSERT INTO cells (id, region, state, assigned_accounts, soft_account_limit, created_at, route_origin)
VALUES
  ('cell-us-east-01', 'us-east', 'active', 0, 1000, statement_timestamp(), :'cell_a_route_origin'),
  ('cell-us-west-01', 'us-west', 'active', 0, 1000, statement_timestamp(), :'cell_b_route_origin')
ON CONFLICT (id) DO UPDATE
SET region = EXCLUDED.region,
    state = EXCLUDED.state,
    soft_account_limit = EXCLUDED.soft_account_limit,
    route_origin = EXCLUDED.route_origin;
