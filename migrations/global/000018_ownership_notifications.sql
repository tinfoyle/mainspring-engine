BEGIN;

ALTER TABLE identity_notification_outbox
    DROP CONSTRAINT identity_notification_outbox_kind_check;

ALTER TABLE identity_notification_outbox
    ADD CONSTRAINT identity_notification_outbox_kind_check CHECK (kind IN (
        'verification','invitation','recovery','discard','ownership_transfer'
    ));

COMMIT;
