BEGIN;

CREATE OR REPLACE FUNCTION spyglass_reject_privacy_evidence_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND
       current_setting('spyglass.privacy_erasure_subject_id',true) = OLD.subject_id::text THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'privacy consent decisions are immutable';
END;
$$;

ALTER TABLE affiliate_attributions
    DROP CONSTRAINT affiliate_attributions_referred_account_id_fkey,
    DROP CONSTRAINT affiliate_attributions_checkout_request_id_fkey,
    ALTER COLUMN referred_account_id DROP NOT NULL,
    ALTER COLUMN checkout_request_id DROP NOT NULL,
    ADD CONSTRAINT affiliate_attributions_referred_account_id_fkey
        FOREIGN KEY (referred_account_id) REFERENCES accounts (id) ON DELETE SET NULL,
    ADD CONSTRAINT affiliate_attributions_checkout_request_id_fkey
        FOREIGN KEY (checkout_request_id) REFERENCES billing_checkout_attempts (request_id) ON DELETE SET NULL;

COMMIT;
