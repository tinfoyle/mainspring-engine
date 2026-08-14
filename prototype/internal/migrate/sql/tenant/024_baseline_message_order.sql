CREATE SEQUENCE baseline_interview_message_order_seq;

ALTER TABLE baseline_interview_messages
    ADD COLUMN message_order BIGINT;

-- now() is transaction-stable, so an owner's answer and the following agent
-- prompt can share the same timestamp. Recover the intended order for existing
-- interviews by placing the answer before the prompt when timestamps tie.
WITH ordered AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY assessment_id
               ORDER BY created_at,
                        CASE role WHEN 'user' THEN 0 ELSE 1 END,
                        id
           ) AS ordinal
    FROM baseline_interview_messages
)
UPDATE baseline_interview_messages message
SET message_order = ordered.ordinal
FROM ordered
WHERE message.id = ordered.id;

SELECT setval(
    'baseline_interview_message_order_seq',
    COALESCE((SELECT max(message_order) FROM baseline_interview_messages), 0) + 1,
    false
);

ALTER TABLE baseline_interview_messages
    ALTER COLUMN message_order SET DEFAULT nextval('baseline_interview_message_order_seq'),
    ALTER COLUMN message_order SET NOT NULL;

CREATE INDEX baseline_interview_messages_order_idx
    ON baseline_interview_messages(assessment_id, message_order);
