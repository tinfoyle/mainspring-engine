DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_app_api') THEN
    CREATE ROLE spyglass_app_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

ALTER ROLE spyglass_app_api PASSWORD 'spyglass-app-api-local-only';
GRANT CONNECT ON DATABASE spyglass TO spyglass_app_api;
GRANT USAGE ON SCHEMA public, spyglass TO spyglass_app_api;
GRANT SELECT ON spyglass.account_erasure_restore_ledger TO spyglass_app_api;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA spyglass TO spyglass_app_api;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA spyglass TO spyglass_app_api;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public, spyglass TO spyglass_app_api;
