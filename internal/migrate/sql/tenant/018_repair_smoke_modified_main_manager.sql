-- An early agent customization smoke test selected the first agent link after
-- creating its fixture. On populated tenants that changed Main Manager rather
-- than the fixture. Repair only the exact smoke-test settings fingerprint.
UPDATE personas
SET context_token_limit = 12000,
    max_output_tokens = 2000,
    timeout_seconds = 300,
    max_cost_micros = 1,
    temperature = NULL,
    top_p = NULL,
    updated_at = now()
WHERE name = 'Main Manager'
  AND role = 'Main Manager'
  AND context_token_limit = 3456
  AND max_output_tokens = 789
  AND timeout_seconds = 45
  AND max_cost_micros = 7
  AND temperature = 0.3
  AND top_p = 0.8;
