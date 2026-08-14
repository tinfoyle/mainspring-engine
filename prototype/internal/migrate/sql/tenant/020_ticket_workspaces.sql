ALTER TABLE work_items
    ADD COLUMN parent_id UUID REFERENCES work_items(id) ON DELETE CASCADE;

CREATE INDEX work_items_parent_idx
    ON work_items(parent_id, created_at)
    WHERE parent_id IS NOT NULL;

-- Ticket workspaces let any enabled specialist propose approval-gated subtasks.
-- The application injects and validates the current parent ticket ID, so this
-- grant cannot create unrelated root work from a ticket conversation.
INSERT INTO persona_tool_grants (persona_id, capability, conditions)
SELECT id, 'tickets.create', '{"scope":"subtask_only"}'::jsonb
FROM personas
WHERE enabled
ON CONFLICT (persona_id, capability) DO NOTHING;
