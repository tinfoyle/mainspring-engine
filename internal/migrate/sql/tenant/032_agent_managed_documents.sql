ALTER TABLE documents
    ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    ADD COLUMN created_by_persona UUID REFERENCES personas(id) ON DELETE SET NULL,
    ADD COLUMN last_updated_by_persona UUID REFERENCES personas(id) ON DELETE SET NULL,
    ADD COLUMN source_run_id UUID REFERENCES boardroom_runs(id) ON DELETE SET NULL,
    ADD COLUMN source_invocation_id UUID REFERENCES agent_invocations(id) ON DELETE SET NULL;

CREATE TABLE document_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK (revision > 0),
    name TEXT NOT NULL,
    media_type TEXT NOT NULL,
    content TEXT NOT NULL,
    sha256 BYTEA NOT NULL,
    change_summary TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_by_persona UUID REFERENCES personas(id) ON DELETE SET NULL,
    source_run_id UUID REFERENCES boardroom_runs(id) ON DELETE SET NULL,
    source_invocation_id UUID REFERENCES agent_invocations(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (document_id, revision)
);

INSERT INTO document_revisions (
    document_id, revision, name, media_type, content, sha256,
    change_summary, created_by, created_at
)
SELECT id, 1, name, media_type, COALESCE(content, ''), sha256,
       'Initial document revision', created_by, created_at
FROM documents
WHERE deleted_at IS NULL;

INSERT INTO persona_tool_grants (persona_id, capability, conditions)
SELECT id, 'documents.read', '{}'::jsonb
FROM personas
ON CONFLICT (persona_id, capability) DO NOTHING;

INSERT INTO persona_tool_grants (persona_id, capability, conditions)
SELECT id, 'documents.write', '{}'::jsonb
FROM personas
ON CONFLICT (persona_id, capability) DO NOTHING;
