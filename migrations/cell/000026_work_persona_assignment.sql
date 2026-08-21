BEGIN;

-- Work may point at a cell-owned Persona only within the same Account. The
-- adapter independently requires the Persona, its Boardroom, and its latest
-- published version to be active when a new assignment is written.
ALTER TABLE spyglass.work_items
    ADD CONSTRAINT work_items_persona_assignment_fk
    FOREIGN KEY (account_id, assignee_persona_id)
    REFERENCES spyglass.agent_personas (account_id, id)
    DEFERRABLE INITIALLY DEFERRED
    NOT VALID;

ALTER TABLE spyglass.work_items
    VALIDATE CONSTRAINT work_items_persona_assignment_fk;

COMMIT;
