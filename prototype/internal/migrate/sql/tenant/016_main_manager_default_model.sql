-- A previous runner smoke test may have left an API-only model override on the
-- first default persona. ChatGPT-backed Codex selects its permitted default
-- model when no explicit model is supplied.
UPDATE personas
SET model = '', updated_at = now()
WHERE name = 'Main Manager'
  AND role = 'Main Manager'
  AND provider = 'codex';

-- The circuit recorded the configuration failure, not a service outage. Clear
-- it after correcting the configuration so a newly created run can proceed.
DELETE FROM provider_circuits WHERE provider = 'runner:mock:codex';
