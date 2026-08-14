CREATE TABLE baseline_assessments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenant_settings(tenant_id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'in_progress'
        CHECK (status IN ('in_progress', 'active', 'ready', 'archived')),
    phase TEXT NOT NULL DEFAULT 'interview'
        CHECK (phase IN ('interview', 'source_access', 'inventory', 'gap_review', 'plan_approval', 'active', 'baseline_ready')),
    current_question INTEGER NOT NULL DEFAULT 0 CHECK (current_question >= 0),
    baseline_version INTEGER NOT NULL DEFAULT 1 CHECK (baseline_version > 0),
    last_assessed_at TIMESTAMPTZ,
    next_reassessment_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX baseline_assessments_current_idx
    ON baseline_assessments(tenant_id)
    WHERE status <> 'archived';

CREATE TABLE baseline_interview_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    assessment_id UUID NOT NULL REFERENCES baseline_assessments(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('agent', 'user', 'system')),
    question_key TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX baseline_interview_messages_assessment_idx
    ON baseline_interview_messages(assessment_id, created_at, id);

CREATE TABLE business_facts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    assessment_id UUID NOT NULL REFERENCES baseline_assessments(id) ON DELETE CASCADE,
    fact_key TEXT NOT NULL,
    value TEXT NOT NULL,
    source_type TEXT NOT NULL DEFAULT 'owner'
        CHECK (source_type IN ('owner', 'document', 'email', 'google_drive', 'public_web', 'agent')),
    source_ref TEXT NOT NULL DEFAULT '',
    confidence NUMERIC(4,3) NOT NULL DEFAULT 1 CHECK (confidence >= 0 AND confidence <= 1),
    confirmed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (assessment_id, fact_key)
);

CREATE TABLE baseline_data_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenant_settings(tenant_id) ON DELETE CASCADE,
    source_type TEXT NOT NULL
        CHECK (source_type IN ('uploads', 'email', 'google_drive', 'public_web')),
    display_name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'available'
        CHECK (status IN ('available', 'connected', 'syncing', 'error', 'disconnected')),
    scope JSONB NOT NULL DEFAULT '{}'::jsonb,
    credential_ciphertext BYTEA,
    cursor TEXT NOT NULL DEFAULT '',
    last_synced_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, source_type)
);

CREATE TABLE baseline_source_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    data_source_id UUID NOT NULL REFERENCES baseline_data_sources(id) ON DELETE CASCADE,
    external_id TEXT NOT NULL,
    name TEXT NOT NULL,
    media_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    source_uri TEXT NOT NULL DEFAULT '',
    modified_at TIMESTAMPTZ,
    content_sha256 TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    document_id UUID REFERENCES documents(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (data_source_id, external_id)
);

CREATE TABLE evidence_requirements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    assessment_id UUID NOT NULL REFERENCES baseline_assessments(id) ON DELETE CASCADE,
    requirement_key TEXT NOT NULL,
    domain TEXT NOT NULL,
    label TEXT NOT NULL,
    rationale TEXT NOT NULL DEFAULT '',
    expected_artifact_types TEXT[] NOT NULL DEFAULT '{}',
    required BOOLEAN NOT NULL DEFAULT true,
    status TEXT NOT NULL DEFAULT 'missing'
        CHECK (status IN ('confirmed', 'partial', 'missing', 'stale', 'not_applicable', 'blocked')),
    disposition TEXT NOT NULL DEFAULT ''
        CHECK (disposition IN ('', 'have_it', 'search_sources', 'create_it', 'obtain_it', 'not_applicable')),
    responsibility TEXT NOT NULL DEFAULT 'shared'
        CHECK (responsibility IN ('agent', 'owner', 'shared', 'external')),
    renewal_due_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (assessment_id, requirement_key)
);

CREATE INDEX evidence_requirements_review_idx
    ON evidence_requirements(assessment_id, status, domain);

CREATE TABLE evidence_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    requirement_id UUID NOT NULL REFERENCES evidence_requirements(id) ON DELETE CASCADE,
    document_id UUID REFERENCES documents(id) ON DELETE CASCADE,
    source_item_id UUID REFERENCES baseline_source_items(id) ON DELETE CASCADE,
    public_url TEXT NOT NULL DEFAULT '',
    citation TEXT NOT NULL DEFAULT '',
    confidence NUMERIC(4,3) NOT NULL DEFAULT 1 CHECK (confidence >= 0 AND confidence <= 1),
    verified_by UUID REFERENCES users(id) ON DELETE SET NULL,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(document_id, source_item_id) + CASE WHEN public_url <> '' THEN 1 ELSE 0 END = 1)
);

CREATE UNIQUE INDEX evidence_links_document_idx
    ON evidence_links(requirement_id, document_id)
    WHERE document_id IS NOT NULL;

CREATE TABLE baseline_research_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    assessment_id UUID NOT NULL REFERENCES baseline_assessments(id) ON DELETE CASCADE,
    requirement_id UUID REFERENCES evidence_requirements(id) ON DELETE CASCADE,
    query TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','completed','failed')),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE baseline_research_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    research_run_id UUID NOT NULL REFERENCES baseline_research_runs(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    citation_id TEXT NOT NULL DEFAULT '',
    retrieved_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX baseline_research_runs_requirement_idx
    ON baseline_research_runs(requirement_id, created_at DESC);

ALTER TABLE work_items
    ADD COLUMN responsibility TEXT NOT NULL DEFAULT 'owner'
        CHECK (responsibility IN ('agent', 'owner', 'shared', 'external')),
    ADD COLUMN baseline_requirement_id UUID REFERENCES evidence_requirements(id) ON DELETE SET NULL;

CREATE INDEX work_items_baseline_requirement_idx
    ON work_items(baseline_requirement_id)
    WHERE baseline_requirement_id IS NOT NULL;

CREATE TABLE baseline_work_items (
    assessment_id UUID NOT NULL REFERENCES baseline_assessments(id) ON DELETE CASCADE,
    requirement_id UUID REFERENCES evidence_requirements(id) ON DELETE SET NULL,
    work_item_id UUID NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (assessment_id, work_item_id)
);

INSERT INTO baseline_data_sources (tenant_id, source_type, display_name, status, scope)
SELECT tenant_settings.tenant_id, defaults.source_type, defaults.display_name, defaults.status, defaults.scope
FROM tenant_settings
CROSS JOIN (VALUES
    ('uploads', 'File uploads', 'connected', '{"read_only":true}'::jsonb),
    ('email', 'Email inbox', 'available', '{"read_only":true,"folders":["INBOX"],"max_items":100}'::jsonb),
    ('google_drive', 'Google Drive', 'available', '{"read_only":true,"folders":[]}'::jsonb),
    ('public_web', 'Public web', 'connected', '{"read_only":true,"authoritative_sources_only":true}'::jsonb)
) AS defaults(source_type, display_name, status, scope)
ON CONFLICT (tenant_id, source_type) DO NOTHING;
