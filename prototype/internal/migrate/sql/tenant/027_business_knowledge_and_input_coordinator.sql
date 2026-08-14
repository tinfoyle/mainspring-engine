CREATE TABLE business_knowledge_facts (
    fact_key TEXT PRIMARY KEY,
    label TEXT NOT NULL,
    value TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'company',
    source_type TEXT NOT NULL DEFAULT 'owner',
    source_ref TEXT NOT NULL DEFAULT '',
    confidence NUMERIC(4,3) NOT NULL DEFAULT 1 CHECK (confidence >= 0 AND confidence <= 1),
    sensitivity TEXT NOT NULL DEFAULT 'internal'
        CHECK (sensitivity IN ('public', 'internal', 'confidential', 'restricted')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'superseded', 'stale')),
    confirmed_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX business_knowledge_facts_active_idx
    ON business_knowledge_facts(status, updated_at DESC);

CREATE TABLE human_input_question_facts (
    request_id UUID NOT NULL REFERENCES human_input_requests(id) ON DELETE CASCADE,
    question_index INTEGER NOT NULL CHECK (question_index >= 0),
    fact_key TEXT NOT NULL,
    question TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'answered', 'declined')),
    answer TEXT NOT NULL DEFAULT '',
    answered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (request_id, question_index)
);

CREATE INDEX human_input_question_facts_queue_idx
    ON human_input_question_facts(status, fact_key, created_at);

CREATE TABLE input_coordinator_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    role TEXT NOT NULL CHECK (role IN ('agent', 'user', 'system')),
    message_kind TEXT NOT NULL DEFAULT 'message'
        CHECK (message_kind IN ('intro', 'question', 'answer', 'progress', 'complete', 'message')),
    fact_key TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX input_coordinator_question_once_idx
    ON input_coordinator_messages(fact_key)
    WHERE message_kind='question' AND fact_key <> '';

-- Onboarding is the first source of shared business knowledge. Carry the
-- current assessment's confirmed facts forward without changing its audit log.
INSERT INTO business_knowledge_facts (
    fact_key, label, value, source_type, source_ref, confidence, confirmed_at, updated_at
)
SELECT DISTINCT ON (CASE bf.fact_key
           WHEN 'business_name' THEN 'organization.legal_name'
           WHEN 'website_url' THEN 'organization.website'
           WHEN 'industry' THEN 'organization.industry'
           WHEN 'primary_location' THEN 'organization.primary_jurisdiction'
           WHEN 'services' THEN 'organization.services'
           WHEN 'team_size' THEN 'workforce.structure'
           WHEN 'immediate_concern' THEN 'organization.immediate_concern'
           ELSE bf.fact_key END)
       CASE bf.fact_key
           WHEN 'business_name' THEN 'organization.legal_name'
           WHEN 'website_url' THEN 'organization.website'
           WHEN 'industry' THEN 'organization.industry'
           WHEN 'primary_location' THEN 'organization.primary_jurisdiction'
           WHEN 'services' THEN 'organization.services'
           WHEN 'team_size' THEN 'workforce.structure'
           WHEN 'immediate_concern' THEN 'organization.immediate_concern'
           ELSE bf.fact_key END,
       initcap(replace(bf.fact_key, '_', ' ')), bf.value,
       bf.source_type, bf.source_ref, bf.confidence, bf.confirmed_at, bf.updated_at
FROM business_facts bf
JOIN baseline_assessments ba ON ba.id=bf.assessment_id
WHERE ba.status <> 'archived' AND btrim(bf.value) <> ''
ORDER BY CASE bf.fact_key
           WHEN 'business_name' THEN 'organization.legal_name'
           WHEN 'website_url' THEN 'organization.website'
           WHEN 'industry' THEN 'organization.industry'
           WHEN 'primary_location' THEN 'organization.primary_jurisdiction'
           WHEN 'services' THEN 'organization.services'
           WHEN 'team_size' THEN 'workforce.structure'
           WHEN 'immediate_concern' THEN 'organization.immediate_concern'
           ELSE bf.fact_key END,
         ba.baseline_version DESC, bf.updated_at DESC
ON CONFLICT (fact_key) DO NOTHING;
