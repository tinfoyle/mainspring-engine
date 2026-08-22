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
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_billing_worker') THEN
    CREATE ROLE spyglass_billing_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_notification_worker') THEN
    CREATE ROLE spyglass_notification_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_entitlement_worker') THEN
    CREATE ROLE spyglass_entitlement_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_account_lifecycle_worker') THEN
    CREATE ROLE spyglass_account_lifecycle_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_identity_maintenance_worker') THEN
    CREATE ROLE spyglass_identity_maintenance_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_work_reconciler') THEN
    CREATE ROLE spyglass_work_reconciler LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_baseline_maintenance_worker') THEN
    CREATE ROLE spyglass_baseline_maintenance_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

\getenv account_api_password SPYGLASS_ACCOUNT_API_DATABASE_PASSWORD
\getenv app_router_password SPYGLASS_APP_ROUTER_DATABASE_PASSWORD
\getenv admission_password SPYGLASS_ADMISSION_DATABASE_PASSWORD
\getenv billing_password SPYGLASS_BILLING_WORKER_DATABASE_PASSWORD
\getenv notification_password SPYGLASS_NOTIFICATION_WORKER_DATABASE_PASSWORD
\getenv entitlement_password SPYGLASS_ENTITLEMENT_WORKER_DATABASE_PASSWORD
\getenv lifecycle_password SPYGLASS_ACCOUNT_LIFECYCLE_WORKER_DATABASE_PASSWORD
\getenv identity_maintenance_password SPYGLASS_IDENTITY_MAINTENANCE_WORKER_DATABASE_PASSWORD
\getenv work_reconciler_password SPYGLASS_WORK_RECONCILER_DATABASE_PASSWORD
\getenv baseline_maintenance_password SPYGLASS_BASELINE_MAINTENANCE_WORKER_DATABASE_PASSWORD
SELECT format('ALTER ROLE spyglass_account_api PASSWORD %L', :'account_api_password') \gexec
SELECT format('ALTER ROLE spyglass_app_router PASSWORD %L', :'app_router_password') \gexec
SELECT format('ALTER ROLE spyglass_admission_api PASSWORD %L', :'admission_password') \gexec
SELECT format('ALTER ROLE spyglass_billing_worker PASSWORD %L', :'billing_password') \gexec
SELECT format('ALTER ROLE spyglass_notification_worker PASSWORD %L', :'notification_password') \gexec
SELECT format('ALTER ROLE spyglass_entitlement_worker PASSWORD %L', :'entitlement_password') \gexec
SELECT format('ALTER ROLE spyglass_account_lifecycle_worker PASSWORD %L', :'lifecycle_password') \gexec
SELECT format('ALTER ROLE spyglass_identity_maintenance_worker PASSWORD %L', :'identity_maintenance_password') \gexec
SELECT format('ALTER ROLE spyglass_work_reconciler PASSWORD %L', :'work_reconciler_password') \gexec
SELECT format('ALTER ROLE spyglass_baseline_maintenance_worker PASSWORD %L', :'baseline_maintenance_password') \gexec

GRANT CONNECT ON DATABASE spyglass TO spyglass_account_api, spyglass_app_router, spyglass_admission_api,
  spyglass_billing_worker, spyglass_notification_worker, spyglass_entitlement_worker,
  spyglass_account_lifecycle_worker, spyglass_identity_maintenance_worker, spyglass_work_reconciler,
  spyglass_baseline_maintenance_worker;
GRANT USAGE ON SCHEMA public TO spyglass_account_api, spyglass_app_router, spyglass_admission_api,
  spyglass_billing_worker, spyglass_notification_worker, spyglass_entitlement_worker,
  spyglass_account_lifecycle_worker, spyglass_identity_maintenance_worker, spyglass_work_reconciler,
  spyglass_baseline_maintenance_worker;

REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM spyglass_billing_worker,
  spyglass_notification_worker, spyglass_entitlement_worker, spyglass_account_lifecycle_worker,
  spyglass_identity_maintenance_worker,
  spyglass_work_reconciler, spyglass_baseline_maintenance_worker;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM spyglass_billing_worker,
  spyglass_notification_worker, spyglass_entitlement_worker, spyglass_account_lifecycle_worker,
  spyglass_identity_maintenance_worker,
  spyglass_work_reconciler, spyglass_baseline_maintenance_worker;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM spyglass_billing_worker,
  spyglass_notification_worker, spyglass_entitlement_worker, spyglass_account_lifecycle_worker,
  spyglass_identity_maintenance_worker,
  spyglass_work_reconciler, spyglass_baseline_maintenance_worker;

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

GRANT SELECT ON account_erasure_restore_ledger TO spyglass_billing_worker,
  spyglass_notification_worker, spyglass_entitlement_worker, spyglass_account_lifecycle_worker,
  spyglass_identity_maintenance_worker, spyglass_work_reconciler, spyglass_baseline_maintenance_worker;

GRANT SELECT, UPDATE ON billing_event_inbox TO spyglass_billing_worker;
GRANT SELECT ON billing_profiles, offer_provider_prices, catalog_publications TO spyglass_billing_worker;
GRANT SELECT, INSERT, UPDATE ON subscriptions, entitlement_snapshots,
  billing_reconciliation_queue TO spyglass_billing_worker;
GRANT SELECT, UPDATE ON billing_checkout_attempts, accounts TO spyglass_billing_worker;
GRANT SELECT, INSERT, DELETE ON entitlement_grants TO spyglass_billing_worker;

GRANT SELECT, UPDATE ON identity_notification_outbox TO spyglass_notification_worker;

GRANT SELECT ON catalog_publications TO spyglass_entitlement_worker;
GRANT SELECT, INSERT, UPDATE ON entitlement_catalog_rollouts,
  entitlement_recompute_queue, entitlement_snapshots TO spyglass_entitlement_worker;
GRANT SELECT, UPDATE ON accounts TO spyglass_entitlement_worker;
GRANT SELECT, INSERT, DELETE ON entitlement_grants TO spyglass_entitlement_worker;

GRANT SELECT, UPDATE ON account_closure_requests, accounts TO spyglass_account_lifecycle_worker;
GRANT SELECT ON memberships, subscriptions, billing_checkout_attempts TO spyglass_account_lifecycle_worker;
GRANT INSERT ON account_lifecycle_events TO spyglass_account_lifecycle_worker;

GRANT EXECUTE ON FUNCTION spyglass_prune_passkey_ceremonies(timestamptz,bigint,integer),
  spyglass_passkey_ceremony_retention_stats(timestamptz,bigint)
  TO spyglass_identity_maintenance_worker;

GRANT SELECT ON accounts TO spyglass_work_reconciler;
GRANT SELECT, UPDATE ON entitlement_usage_counters, entitlement_usage_reservations
  TO spyglass_work_reconciler;

GRANT SELECT ON accounts, entitlement_snapshots TO spyglass_baseline_maintenance_worker;
GRANT SELECT, INSERT, UPDATE ON entitlement_usage_counters, entitlement_usage_reservations
  TO spyglass_baseline_maintenance_worker;
GRANT EXECUTE ON FUNCTION spyglass_lock_account_entitlement_version(uuid)
  TO spyglass_baseline_maintenance_worker;
