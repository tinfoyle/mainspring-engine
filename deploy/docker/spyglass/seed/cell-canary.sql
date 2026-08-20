INSERT INTO spyglass.account_namespaces (account_id, placement_generation, state, created_at)
VALUES (:'account_id', 1, 'active', statement_timestamp())
ON CONFLICT (account_id) DO UPDATE
SET placement_generation = EXCLUDED.placement_generation,
    state = EXCLUDED.state;
