BEGIN;

-- Capacity admission must fence the Account entitlement version while it
-- locks and changes the usage counter. A narrowly permissioned admission API
-- may execute this function without receiving UPDATE on the Accounts table.
CREATE FUNCTION spyglass_lock_account_entitlement_version(p_account_id uuid) RETURNS bigint
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT entitlement_version
    FROM public.accounts
    WHERE id = p_account_id
    FOR UPDATE
$$;

REVOKE ALL ON FUNCTION spyglass_lock_account_entitlement_version(uuid) FROM PUBLIC;

COMMIT;
