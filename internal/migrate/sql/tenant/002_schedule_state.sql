ALTER TABLE schedules
    ADD COLUMN prompt TEXT NOT NULL DEFAULT '',
    ADD COLUMN state TEXT NOT NULL DEFAULT 'active'
        CHECK (state IN ('creating', 'active', 'failed')),
    ADD COLUMN last_error TEXT;
