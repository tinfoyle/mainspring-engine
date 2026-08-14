-- Web research is a baseline read-only capability for every agent. Apply the
-- grant to inactive personas too so turning an existing agent on later does
-- not produce a partially configured agent.
INSERT INTO persona_tool_grants (persona_id, capability, conditions)
SELECT p.id, capability, '{}'::jsonb
FROM personas p
CROSS JOIN (VALUES ('web.search'), ('web.read')) AS baseline(capability)
ON CONFLICT (persona_id, capability) DO NOTHING;
