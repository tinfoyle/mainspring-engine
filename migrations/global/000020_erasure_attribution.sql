BEGIN;

ALTER TABLE identity_notification_outbox
    ADD COLUMN account_id uuid REFERENCES accounts (id);
CREATE INDEX identity_notification_outbox_account
    ON identity_notification_outbox (account_id,created_at,id)
    WHERE account_id IS NOT NULL;

-- Billing events can arrive again after an Account has been erased, so this
-- attribution is deliberately not a foreign key. Completed erasure removes
-- Account-attributed payloads; a later provider retry may be durably accepted
-- and classified against the content-free erasure tombstone.
ALTER TABLE billing_event_inbox ADD COLUMN account_id uuid;
CREATE INDEX billing_event_inbox_account
    ON billing_event_inbox (account_id,created_at,provider_event_id)
    WHERE account_id IS NOT NULL;

COMMIT;
