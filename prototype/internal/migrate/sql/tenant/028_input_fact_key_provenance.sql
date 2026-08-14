ALTER TABLE human_input_question_facts
    ADD COLUMN key_source TEXT NOT NULL DEFAULT 'heuristic'
        CHECK (key_source IN ('heuristic', 'agent'));
