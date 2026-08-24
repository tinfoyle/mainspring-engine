BEGIN;

ALTER TABLE affiliate_attributions
    DROP CONSTRAINT affiliate_attributions_lock_shape,
    ADD CONSTRAINT affiliate_attributions_lock_shape CHECK (
        (state = 'reserved' AND provider_subscription_id IS NULL AND locked_at IS NULL) OR
        (state = 'locked' AND left(provider_subscription_id,4) = 'sub_' AND length(provider_subscription_id) <= 200 AND provider_subscription_id !~ '[[:space:]]' AND locked_at IS NOT NULL AND locked_at >= created_at) OR
        (state = 'canceled' AND provider_subscription_id IS NULL)
    );

ALTER TABLE affiliate_commission_entries
    DROP CONSTRAINT affiliate_commission_entries_provider_shape,
    ADD CONSTRAINT affiliate_commission_entries_provider_shape CHECK (
        left(provider_subscription_id,4) = 'sub_' AND length(provider_subscription_id) <= 200 AND provider_subscription_id !~ '[[:space:]]' AND
        left(provider_invoice_id,3) = 'in_' AND length(provider_invoice_id) <= 200 AND provider_invoice_id !~ '[[:space:]]'
    );

COMMIT;
