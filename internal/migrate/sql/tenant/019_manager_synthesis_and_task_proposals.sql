-- Manager-led runs intentionally snapshot Main Manager twice: once to plan and
-- once to synthesize specialist contributions. Turn number remains unique, so
-- the legacy per-run persona uniqueness constraint is both redundant and wrong.
ALTER TABLE boardroom_run_personas
    DROP CONSTRAINT IF EXISTS boardroom_run_personas_run_id_persona_id_key;

-- The operating manager has the tickets.create grant by default. Keep all task
-- creation approval-gated, but allow the manager to propose those actions.
UPDATE personas
SET action_policy = 'propose_only', updated_at = now()
WHERE name = 'Main Manager'
  AND role = 'Main Manager'
  AND action_policy = 'disabled';
