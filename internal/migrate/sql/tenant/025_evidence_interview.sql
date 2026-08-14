ALTER TABLE evidence_requirements
    ADD COLUMN owner_answer TEXT NOT NULL DEFAULT '',
    ADD COLUMN interviewed_at TIMESTAMPTZ;

CREATE INDEX evidence_requirements_interview_idx
    ON evidence_requirements(assessment_id, interviewed_at, created_at);
