BEGIN;

-- A Boardroom manager is a mutable configuration reference. The command path
-- validates that it is an active, published Persona in this Boardroom without
-- adding a reverse Persona -> Boardroom foreign-key cycle; account movement
-- must retain an acyclic table dependency graph. Every run freezes the exact
-- immutable manager version in its turn plan.
ALTER TABLE spyglass.agent_boardrooms
    ADD COLUMN manager_persona_id uuid;

ALTER TABLE spyglass.agent_runs
    ADD COLUMN mode text NOT NULL DEFAULT 'selected'
        CHECK (mode IN ('selected','manager_led'));

COMMIT;
