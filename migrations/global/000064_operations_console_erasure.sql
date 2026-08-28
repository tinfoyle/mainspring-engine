BEGIN;

-- Operations support evidence is immutable during ordinary operation, but it
-- contains customer identifiers and must leave with the Account during the
-- independently approved Account-erasure transaction.  The erasure function
-- sets a transaction-local exact Account identifier before deleting Accounts.
CREATE OR REPLACE FUNCTION spyglass_reject_operations_immutable_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' AND TG_TABLE_NAME IN ('operations_access_events','operations_support_grants') AND
       OLD.account_id IS NOT NULL AND
       current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'operations audit evidence is immutable' USING ERRCODE='42501';
END;
$$;

CREATE OR REPLACE FUNCTION spyglass_guard_operations_support_grant() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' AND
       current_setting('spyglass.erasure_account_id',true)=OLD.account_id::text THEN
        RETURN OLD;
    END IF;
    IF OLD.state <> 'active' OR NEW.state <> 'revoked' OR OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL OR
       NEW.version <> OLD.version+1 OR NEW.id IS DISTINCT FROM OLD.id OR
       NEW.staff_user_id IS DISTINCT FROM OLD.staff_user_id OR NEW.target_user_id IS DISTINCT FROM OLD.target_user_id OR
       NEW.account_id IS DISTINCT FROM OLD.account_id OR NEW.ticket IS DISTINCT FROM OLD.ticket OR
       NEW.reason IS DISTINCT FROM OLD.reason OR NEW.created_at IS DISTINCT FROM OLD.created_at OR
       NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
        RAISE EXCEPTION 'operations support grant transition is invalid' USING ERRCODE='42501';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE operations_access_events
    DROP CONSTRAINT operations_access_events_support_grant_id_fkey,
    ADD CONSTRAINT operations_access_events_support_grant_id_fkey
        FOREIGN KEY (support_grant_id) REFERENCES operations_support_grants (id) ON DELETE CASCADE,
    DROP CONSTRAINT operations_access_events_account_id_fkey,
    ADD CONSTRAINT operations_access_events_account_id_fkey
        FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE;

ALTER TABLE operations_support_grants
    DROP CONSTRAINT operations_support_grants_account_id_fkey,
    ADD CONSTRAINT operations_support_grants_account_id_fkey
        FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE;

REVOKE ALL ON FUNCTION spyglass_reject_operations_immutable_mutation() FROM PUBLIC;
REVOKE ALL ON FUNCTION spyglass_guard_operations_support_grant() FROM PUBLIC;

COMMIT;
