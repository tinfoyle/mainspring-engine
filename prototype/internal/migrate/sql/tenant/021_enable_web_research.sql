-- Existing managers need the new fallback research path. Agents that already
-- had search permission also receive bounded page reads so they can verify a
-- result instead of treating search snippets as complete evidence.
INSERT INTO persona_tool_grants (persona_id, capability, conditions)
SELECT id, 'web.search', '{}'::jsonb
FROM personas
WHERE enabled = true AND role = 'Main Manager'
ON CONFLICT (persona_id, capability) DO NOTHING;

INSERT INTO persona_tool_grants (persona_id, capability, conditions)
SELECT id, 'web.read', '{}'::jsonb
FROM personas
WHERE enabled = true AND (
    role = 'Main Manager'
    OR id IN (SELECT persona_id FROM persona_tool_grants WHERE capability = 'web.search')
)
ON CONFLICT (persona_id, capability) DO NOTHING;
