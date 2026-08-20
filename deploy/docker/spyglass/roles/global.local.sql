DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_account_api') THEN
    CREATE ROLE spyglass_account_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_app_router') THEN
    CREATE ROLE spyglass_app_router LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_admission_api') THEN
    CREATE ROLE spyglass_admission_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

ALTER ROLE spyglass_account_api PASSWORD 'spyglass-account-api-local-only';
ALTER ROLE spyglass_app_router PASSWORD 'spyglass-app-router-local-only';
ALTER ROLE spyglass_admission_api PASSWORD 'spyglass-admission-local-only';

GRANT CONNECT ON DATABASE spyglass TO spyglass_account_api, spyglass_app_router, spyglass_admission_api;
GRANT USAGE ON SCHEMA public TO spyglass_account_api, spyglass_app_router, spyglass_admission_api;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO spyglass_account_api;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO spyglass_account_api;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO spyglass_account_api;

GRANT SELECT ON users, sessions, accounts, memberships, entitlement_snapshots,
  account_directory, cells, account_erasure_restore_ledger TO spyglass_app_router;
GRANT UPDATE ON sessions TO spyglass_app_router;

GRANT SELECT ON accounts, memberships, entitlement_snapshots,
  entitlement_usage_counters, entitlement_usage_reservations,
  account_erasure_restore_ledger TO spyglass_admission_api;
GRANT INSERT, UPDATE ON entitlement_usage_counters, entitlement_usage_reservations TO spyglass_admission_api;
GRANT EXECUTE ON FUNCTION spyglass_lock_account_entitlement_version(uuid) TO spyglass_admission_api;
