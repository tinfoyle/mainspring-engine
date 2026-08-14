-- A fact may be requested again by a later ticket after the owner answered an
-- earlier request. Preserve each conversational turn instead of forcing every
-- occurrence of a fact key to reuse the first question message forever.
DROP INDEX IF EXISTS input_coordinator_question_once_idx;

ALTER TABLE input_coordinator_messages
    ADD COLUMN question_cycle INTEGER NOT NULL DEFAULT 0 CHECK (question_cycle >= 0);

CREATE UNIQUE INDEX input_coordinator_question_cycle_idx
    ON input_coordinator_messages(fact_key, question_cycle)
    WHERE message_kind='question' AND fact_key <> '';
