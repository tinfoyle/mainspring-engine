DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_account_api') THEN
    CREATE ROLE spyglass_account_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_app_router') THEN
    CREATE ROLE spyglass_app_router LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_mcp_gateway') THEN
    CREATE ROLE spyglass_mcp_gateway LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
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
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_affiliate_retention_worker') THEN
    CREATE ROLE spyglass_affiliate_retention_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_work_reconciler') THEN
    CREATE ROLE spyglass_work_reconciler LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_baseline_maintenance_worker') THEN
    CREATE ROLE spyglass_baseline_maintenance_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_prototype_migration') THEN
    CREATE ROLE spyglass_prototype_migration LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_integration_connector_worker') THEN
    CREATE ROLE spyglass_integration_connector_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_account_export_build_worker') THEN
    CREATE ROLE spyglass_account_export_build_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_account_export_expiry_worker') THEN
    CREATE ROLE spyglass_account_export_expiry_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_analytics_reporter') THEN
    CREATE ROLE spyglass_analytics_reporter LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_operations_identity') THEN
    CREATE ROLE spyglass_operations_identity LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_operations_projection') THEN
    CREATE ROLE spyglass_operations_projection LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_operations_billing') THEN
    CREATE ROLE spyglass_operations_billing LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_operations_privacy') THEN
    CREATE ROLE spyglass_operations_privacy LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_operations_affiliate') THEN
    CREATE ROLE spyglass_operations_affiliate LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

\getenv account_api_password SPYGLASS_ACCOUNT_API_DATABASE_PASSWORD
\getenv app_router_password SPYGLASS_APP_ROUTER_DATABASE_PASSWORD
\getenv mcp_gateway_password SPYGLASS_MCP_GATEWAY_DATABASE_PASSWORD
\getenv admission_password SPYGLASS_ADMISSION_DATABASE_PASSWORD
\getenv billing_password SPYGLASS_BILLING_WORKER_DATABASE_PASSWORD
\getenv notification_password SPYGLASS_NOTIFICATION_WORKER_DATABASE_PASSWORD
\getenv entitlement_password SPYGLASS_ENTITLEMENT_WORKER_DATABASE_PASSWORD
\getenv lifecycle_password SPYGLASS_ACCOUNT_LIFECYCLE_WORKER_DATABASE_PASSWORD
\getenv identity_maintenance_password SPYGLASS_IDENTITY_MAINTENANCE_WORKER_DATABASE_PASSWORD
\getenv affiliate_retention_password SPYGLASS_AFFILIATE_RETENTION_WORKER_DATABASE_PASSWORD
\getenv work_reconciler_password SPYGLASS_WORK_RECONCILER_DATABASE_PASSWORD
\getenv baseline_maintenance_password SPYGLASS_BASELINE_MAINTENANCE_WORKER_DATABASE_PASSWORD
\getenv prototype_migration_password SPYGLASS_PROTOTYPE_MIGRATION_DATABASE_PASSWORD
\getenv integration_connector_password SPYGLASS_INTEGRATION_CONNECTOR_WORKER_DATABASE_PASSWORD
\getenv account_export_build_password SPYGLASS_ACCOUNT_EXPORT_BUILD_WORKER_DATABASE_PASSWORD
\getenv account_export_expiry_password SPYGLASS_ACCOUNT_EXPORT_EXPIRY_WORKER_DATABASE_PASSWORD
\getenv analytics_reporter_password SPYGLASS_ANALYTICS_REPORTER_DATABASE_PASSWORD
\getenv operations_identity_password SPYGLASS_OPERATIONS_IDENTITY_DATABASE_PASSWORD
\getenv operations_projection_password SPYGLASS_OPERATIONS_PROJECTION_DATABASE_PASSWORD
\getenv operations_billing_password SPYGLASS_OPERATIONS_BILLING_DATABASE_PASSWORD
\getenv operations_privacy_password SPYGLASS_OPERATIONS_PRIVACY_DATABASE_PASSWORD
\getenv operations_affiliate_password SPYGLASS_OPERATIONS_AFFILIATE_DATABASE_PASSWORD
SELECT format('ALTER ROLE spyglass_account_api PASSWORD %L', :'account_api_password') \gexec
SELECT format('ALTER ROLE spyglass_app_router PASSWORD %L', :'app_router_password') \gexec
SELECT format('ALTER ROLE spyglass_mcp_gateway PASSWORD %L', :'mcp_gateway_password') \gexec
SELECT format('ALTER ROLE spyglass_admission_api PASSWORD %L', :'admission_password') \gexec
SELECT format('ALTER ROLE spyglass_billing_worker PASSWORD %L', :'billing_password') \gexec
SELECT format('ALTER ROLE spyglass_notification_worker PASSWORD %L', :'notification_password') \gexec
SELECT format('ALTER ROLE spyglass_entitlement_worker PASSWORD %L', :'entitlement_password') \gexec
SELECT format('ALTER ROLE spyglass_account_lifecycle_worker PASSWORD %L', :'lifecycle_password') \gexec
SELECT format('ALTER ROLE spyglass_identity_maintenance_worker PASSWORD %L', :'identity_maintenance_password') \gexec
SELECT format('ALTER ROLE spyglass_affiliate_retention_worker PASSWORD %L', :'affiliate_retention_password') \gexec
SELECT format('ALTER ROLE spyglass_work_reconciler PASSWORD %L', :'work_reconciler_password') \gexec
SELECT format('ALTER ROLE spyglass_baseline_maintenance_worker PASSWORD %L', :'baseline_maintenance_password') \gexec
SELECT format('ALTER ROLE spyglass_prototype_migration PASSWORD %L', :'prototype_migration_password') \gexec
SELECT format('ALTER ROLE spyglass_integration_connector_worker PASSWORD %L', :'integration_connector_password') \gexec
SELECT format('ALTER ROLE spyglass_account_export_build_worker PASSWORD %L', :'account_export_build_password') \gexec
SELECT format('ALTER ROLE spyglass_account_export_expiry_worker PASSWORD %L', :'account_export_expiry_password') \gexec
SELECT format('ALTER ROLE spyglass_analytics_reporter PASSWORD %L', :'analytics_reporter_password') \gexec
SELECT format('ALTER ROLE spyglass_operations_identity PASSWORD %L', :'operations_identity_password') \gexec
SELECT format('ALTER ROLE spyglass_operations_projection PASSWORD %L', :'operations_projection_password') \gexec
SELECT format('ALTER ROLE spyglass_operations_billing PASSWORD %L', :'operations_billing_password') \gexec
SELECT format('ALTER ROLE spyglass_operations_privacy PASSWORD %L', :'operations_privacy_password') \gexec
SELECT format('ALTER ROLE spyglass_operations_affiliate PASSWORD %L', :'operations_affiliate_password') \gexec

GRANT CONNECT ON DATABASE spyglass TO spyglass_account_api, spyglass_app_router, spyglass_mcp_gateway, spyglass_admission_api,
  spyglass_billing_worker, spyglass_notification_worker, spyglass_entitlement_worker,
  spyglass_account_lifecycle_worker, spyglass_identity_maintenance_worker, spyglass_affiliate_retention_worker, spyglass_work_reconciler,
  spyglass_baseline_maintenance_worker, spyglass_prototype_migration, spyglass_integration_connector_worker;
GRANT USAGE ON SCHEMA public TO spyglass_account_api, spyglass_app_router, spyglass_mcp_gateway, spyglass_admission_api,
  spyglass_billing_worker, spyglass_notification_worker, spyglass_entitlement_worker,
  spyglass_account_lifecycle_worker, spyglass_identity_maintenance_worker, spyglass_affiliate_retention_worker, spyglass_work_reconciler,
  spyglass_baseline_maintenance_worker, spyglass_prototype_migration, spyglass_integration_connector_worker;

GRANT CONNECT ON DATABASE spyglass TO spyglass_account_export_build_worker, spyglass_account_export_expiry_worker;
GRANT USAGE ON SCHEMA public TO spyglass_account_export_build_worker, spyglass_account_export_expiry_worker;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM spyglass_account_export_build_worker, spyglass_account_export_expiry_worker;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM spyglass_account_export_build_worker, spyglass_account_export_expiry_worker;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM spyglass_account_export_build_worker, spyglass_account_export_expiry_worker;

GRANT CONNECT ON DATABASE spyglass TO spyglass_analytics_reporter;
GRANT USAGE ON SCHEMA public TO spyglass_analytics_reporter;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM spyglass_analytics_reporter;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM spyglass_analytics_reporter;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM spyglass_analytics_reporter;
GRANT EXECUTE ON FUNCTION spyglass_analytics_funnel_report(timestamptz,timestamptz,text,text,integer)
  TO spyglass_analytics_reporter;

GRANT CONNECT ON DATABASE spyglass TO spyglass_operations_identity, spyglass_operations_projection;
GRANT USAGE ON SCHEMA public TO spyglass_operations_identity, spyglass_operations_projection;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM spyglass_operations_identity, spyglass_operations_projection;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM spyglass_operations_identity, spyglass_operations_projection;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM spyglass_operations_identity, spyglass_operations_projection;

GRANT SELECT ON account_erasure_restore_ledger TO spyglass_operations_identity, spyglass_operations_projection;
GRANT SELECT (user_id,provider,identifier) ON authentication_identities TO spyglass_operations_identity;
GRANT SELECT,INSERT,UPDATE,DELETE ON operations_login_challenges TO spyglass_operations_identity;
GRANT SELECT,INSERT,UPDATE ON operations_authenticators TO spyglass_operations_identity;
GRANT INSERT ON operations_authentication_events TO spyglass_operations_identity;
GRANT SELECT ON users, passkey_users, passkey_credentials, operations_staff,
  operations_staff_role_assignments, operations_access_events TO spyglass_operations_identity;
GRANT SELECT, INSERT, UPDATE, DELETE ON passkey_ceremonies TO spyglass_operations_identity;
GRANT UPDATE (encrypted_credential,encryption_nonce,encryption_key_version,sign_count,last_used_at)
  ON passkey_credentials TO spyglass_operations_identity;
GRANT INSERT ON user_security_events TO spyglass_operations_identity;
GRANT SELECT, INSERT, UPDATE ON network_actor_rate_limits, operations_sessions TO spyglass_operations_identity;

GRANT EXECUTE ON FUNCTION spyglass_operations_current_staff(uuid),
  spyglass_operations_authorize_traffic_report(uuid,uuid,timestamptz,timestamptz,text,text,text,timestamptz),
  spyglass_operations_lookup(uuid,uuid,text,text,text,text,text),
  spyglass_operations_directory(uuid,uuid,text,integer,integer,text,text,text),
  spyglass_operations_record_session_event(uuid,uuid,uuid,text,text,timestamptz),
  spyglass_operations_create_support_grant(uuid,uuid,uuid,uuid,uuid,text,text,text,timestamptz,timestamptz),
  spyglass_operations_get_support_grant(uuid,uuid),
  spyglass_operations_revoke_support_grant(uuid,uuid,uuid,bigint,text,text,text,timestamptz),
  spyglass_operations_customer_access_history(uuid,uuid,integer),
  spyglass_operations_account_view(uuid,uuid,uuid,text,text,text,timestamptz),
  spyglass_operations_analytics_report(uuid,uuid,timestamptz,timestamptz,text,text,integer,text,text,text,timestamptz),
  spyglass_operations_support_history(uuid,uuid,integer,timestamptz)
  TO spyglass_operations_projection;

GRANT CONNECT ON DATABASE spyglass TO spyglass_operations_billing, spyglass_operations_privacy, spyglass_operations_affiliate;
GRANT USAGE ON SCHEMA public TO spyglass_operations_billing, spyglass_operations_privacy, spyglass_operations_affiliate;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM spyglass_operations_billing, spyglass_operations_privacy, spyglass_operations_affiliate;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM spyglass_operations_billing, spyglass_operations_privacy, spyglass_operations_affiliate;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM spyglass_operations_billing, spyglass_operations_privacy, spyglass_operations_affiliate;

GRANT EXECUTE ON FUNCTION spyglass_inspect_billing_failures(uuid,text,text,text,text,integer),
  spyglass_replay_billing_event(uuid,text,text,text,text,text),
  spyglass_queue_billing_subscription_refresh(uuid,text,text,text,text,text)
  TO spyglass_operations_billing;
GRANT EXECUTE ON FUNCTION spyglass_list_open_privacy_rights_requests(uuid,timestamptz,integer,text,text,text),
  spyglass_inspect_privacy_rights_request(uuid,uuid,text,text,text),
  spyglass_transition_privacy_rights_request(uuid,uuid,bigint,text,text,uuid,bytea,text,text,text)
  TO spyglass_operations_privacy;
GRANT EXECUTE ON FUNCTION spyglass_inspect_affiliate_enrollment(uuid,uuid,text,text,text),
  spyglass_inspect_affiliate_risk(uuid,uuid,text,text,text),
  spyglass_transition_affiliate_enrollment(uuid,uuid,bigint,text,text,text,text)
  TO spyglass_operations_affiliate;

REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM spyglass_mcp_gateway, spyglass_billing_worker,
  spyglass_notification_worker, spyglass_entitlement_worker, spyglass_account_lifecycle_worker,
  spyglass_identity_maintenance_worker, spyglass_affiliate_retention_worker,
  spyglass_work_reconciler, spyglass_baseline_maintenance_worker, spyglass_prototype_migration, spyglass_integration_connector_worker;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM spyglass_mcp_gateway, spyglass_billing_worker,
  spyglass_notification_worker, spyglass_entitlement_worker, spyglass_account_lifecycle_worker,
  spyglass_identity_maintenance_worker, spyglass_affiliate_retention_worker,
  spyglass_work_reconciler, spyglass_baseline_maintenance_worker, spyglass_prototype_migration, spyglass_integration_connector_worker;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM spyglass_mcp_gateway, spyglass_billing_worker,
  spyglass_notification_worker, spyglass_entitlement_worker, spyglass_account_lifecycle_worker,
  spyglass_identity_maintenance_worker, spyglass_affiliate_retention_worker,
  spyglass_work_reconciler, spyglass_baseline_maintenance_worker, spyglass_prototype_migration, spyglass_integration_connector_worker;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO spyglass_account_api;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO spyglass_account_api;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO spyglass_account_api;

GRANT SELECT ON users, sessions, accounts, memberships, entitlement_snapshots,
  account_directory, cells, account_erasure_restore_ledger,
  passkey_credentials, user_recovery_code_sets, user_recovery_codes, user_mfa_methods TO spyglass_app_router;
GRANT UPDATE ON sessions TO spyglass_app_router;

GRANT SELECT ON accounts, memberships, entitlement_snapshots, account_directory, cells,
  account_erasure_restore_ledger, passkey_credentials, user_recovery_code_sets, user_recovery_codes,
  user_mfa_methods, account_export_requests
  TO spyglass_mcp_gateway;
GRANT INSERT, UPDATE ON account_export_requests TO spyglass_mcp_gateway;
GRANT INSERT ON account_export_events TO spyglass_mcp_gateway;
GRANT EXECUTE ON FUNCTION spyglass_authenticate_mcp_access_token(bytea,text,text,timestamptz)
  TO spyglass_mcp_gateway;

GRANT SELECT ON accounts, memberships, entitlement_snapshots,
  entitlement_usage_counters, entitlement_usage_reservations,
  account_erasure_restore_ledger, catalog_publications,
  passkey_credentials, user_recovery_code_sets, user_recovery_codes, user_mfa_methods,
  ai_token_grants, ai_token_reservations, ai_token_reservation_allocations,
  ai_token_ledger_entries TO spyglass_admission_api;
GRANT INSERT, UPDATE ON entitlement_usage_counters, entitlement_usage_reservations TO spyglass_admission_api;
GRANT UPDATE ON ai_token_grants TO spyglass_admission_api;
GRANT INSERT, UPDATE ON ai_token_reservations TO spyglass_admission_api;
GRANT INSERT ON ai_token_reservation_allocations, ai_token_ledger_entries TO spyglass_admission_api;
GRANT EXECUTE ON FUNCTION spyglass_lock_account_entitlement_version(uuid)
  TO spyglass_account_api, spyglass_billing_worker, spyglass_admission_api;
GRANT EXECUTE ON FUNCTION spyglass_project_subscription_lifecycle(uuid,text,text,timestamptz,timestamptz,timestamptz)
  TO spyglass_billing_worker;

GRANT SELECT ON account_erasure_restore_ledger TO spyglass_billing_worker,
  spyglass_notification_worker, spyglass_entitlement_worker, spyglass_account_lifecycle_worker,
  spyglass_identity_maintenance_worker, spyglass_affiliate_retention_worker, spyglass_work_reconciler, spyglass_baseline_maintenance_worker, spyglass_prototype_migration,
  spyglass_integration_connector_worker;

GRANT SELECT, UPDATE ON billing_event_inbox TO spyglass_billing_worker;
GRANT SELECT ON billing_profiles, offer_provider_prices, catalog_publications TO spyglass_billing_worker;
GRANT SELECT, INSERT, UPDATE ON subscriptions, entitlement_snapshots,
  billing_reconciliation_queue TO spyglass_billing_worker;
GRANT SELECT, UPDATE ON billing_checkout_attempts, accounts TO spyglass_billing_worker;
GRANT SELECT, INSERT ON account_commissioning_purchases, ai_token_ledger_entries TO spyglass_billing_worker;
GRANT SELECT, INSERT, UPDATE ON ai_token_grants TO spyglass_billing_worker;
GRANT SELECT, INSERT, DELETE ON entitlement_grants TO spyglass_billing_worker;
GRANT SELECT, UPDATE ON affiliate_attributions TO spyglass_billing_worker;
GRANT SELECT ON affiliate_enrollments, affiliate_commission_rules TO spyglass_billing_worker;
GRANT SELECT, INSERT ON affiliate_commission_entries TO spyglass_billing_worker;
GRANT SELECT, INSERT ON affiliate_commission_invoice_payments, affiliate_commission_invoice_lines TO spyglass_billing_worker;
GRANT SELECT ON affiliate_settlement_policies, affiliate_settlement_policy_current TO spyglass_billing_worker;
GRANT SELECT, INSERT, UPDATE ON affiliate_credit_reservations TO spyglass_billing_worker;
GRANT SELECT, INSERT ON affiliate_credit_allocations, affiliate_credit_reservation_events TO spyglass_billing_worker;
GRANT SELECT, INSERT, UPDATE ON affiliate_credit_reversal_adjustments TO spyglass_billing_worker;
GRANT SELECT, INSERT ON affiliate_credit_reversal_adjustment_events TO spyglass_billing_worker;
GRANT SELECT, INSERT ON affiliate_provider_adverse_events, affiliate_provider_adverse_invoice_lines TO spyglass_billing_worker;
GRANT SELECT, UPDATE ON account_subscription_termination_jobs TO spyglass_billing_worker;

GRANT SELECT, INSERT, UPDATE ON identity_notification_outbox TO spyglass_notification_worker;
GRANT SELECT, UPDATE ON account_subscription_lifecycle_notices TO spyglass_notification_worker;
GRANT SELECT ON account_subscription_lifecycles, accounts, memberships, users TO spyglass_notification_worker;

GRANT SELECT ON catalog_publications TO spyglass_entitlement_worker;
GRANT SELECT, INSERT, UPDATE ON entitlement_catalog_rollouts,
  entitlement_recompute_queue, entitlement_snapshots TO spyglass_entitlement_worker;
GRANT SELECT, UPDATE ON accounts TO spyglass_entitlement_worker;
GRANT SELECT, INSERT, DELETE ON entitlement_grants TO spyglass_entitlement_worker;

GRANT SELECT, UPDATE ON account_closure_requests, accounts TO spyglass_account_lifecycle_worker;
GRANT SELECT ON memberships, subscriptions, billing_checkout_attempts TO spyglass_account_lifecycle_worker;
GRANT INSERT ON account_lifecycle_events TO spyglass_account_lifecycle_worker;
GRANT EXECUTE ON FUNCTION spyglass_claim_subscription_lifecycle(timestamptz,bigint),
  spyglass_advance_subscription_lifecycle(uuid,uuid,timestamptz,bigint)
  TO spyglass_account_lifecycle_worker;

GRANT EXECUTE ON FUNCTION spyglass_prune_passkey_ceremonies(timestamptz,bigint,integer),
  spyglass_passkey_ceremony_retention_stats(timestamptz,bigint),
  spyglass_prune_analytics_events(timestamptz,bigint,integer),
  spyglass_analytics_retention_stats(timestamptz,bigint),
  spyglass_prune_network_actor_limits(timestamptz,bigint,integer),
  spyglass_network_actor_limit_retention_stats(timestamptz,bigint)
  TO spyglass_identity_maintenance_worker;

GRANT EXECUTE ON FUNCTION spyglass_minimize_due_affiliates(timestamptz,integer),
  spyglass_affiliate_minimization_stats(timestamptz)
  TO spyglass_affiliate_retention_worker;

GRANT SELECT ON accounts TO spyglass_work_reconciler;
GRANT SELECT, UPDATE ON entitlement_usage_counters, entitlement_usage_reservations
  TO spyglass_work_reconciler;

GRANT SELECT ON accounts, entitlement_snapshots TO spyglass_baseline_maintenance_worker;
GRANT SELECT, INSERT, UPDATE ON entitlement_usage_counters, entitlement_usage_reservations
  TO spyglass_baseline_maintenance_worker;
GRANT EXECUTE ON FUNCTION spyglass_lock_account_entitlement_version(uuid)
  TO spyglass_baseline_maintenance_worker;

GRANT SELECT ON accounts, entitlement_snapshots TO spyglass_prototype_migration;
GRANT SELECT ON accounts, entitlement_snapshots TO spyglass_integration_connector_worker;

GRANT SELECT ON account_erasure_restore_ledger, accounts, account_directory,
  account_closure_requests, account_lifecycle_events, account_membership_events,
  invitations, memberships, account_commissioning_purchases, account_subscription_lifecycles,
  billing_checkout_attempts, billing_profiles, subscriptions, operations_access_events, operations_support_grants,
  entitlement_grants, entitlement_snapshots, entitlement_usage_counters, affiliate_attributions
  TO spyglass_account_export_build_worker;
GRANT SELECT ON account_export_requests TO spyglass_account_export_build_worker;
GRANT UPDATE (state,attempt_count,next_attempt_at,lease_id,lease_expires_at,error_code,version,
  snapshot_global_at,snapshot_cell_at,artifact_reference,artifact_sha256,artifact_bytes,available_at)
  ON account_export_requests TO spyglass_account_export_build_worker;
GRANT INSERT ON account_export_events TO spyglass_account_export_build_worker;

GRANT SELECT ON account_erasure_restore_ledger TO spyglass_account_export_expiry_worker;
GRANT SELECT ON account_export_requests TO spyglass_account_export_expiry_worker;
GRANT UPDATE (state,lease_id,lease_expires_at,version,artifact_reference,deleted_at)
  ON account_export_requests TO spyglass_account_export_expiry_worker;
GRANT INSERT ON account_export_events TO spyglass_account_export_expiry_worker;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_account_provisioning_worker') THEN
    CREATE ROLE spyglass_account_provisioning_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;
\getenv account_provisioning_password SPYGLASS_ACCOUNT_PROVISIONING_WORKER_DATABASE_PASSWORD
SELECT format('ALTER ROLE spyglass_account_provisioning_worker PASSWORD %L', :'account_provisioning_password') \gexec
GRANT CONNECT ON DATABASE spyglass TO spyglass_account_provisioning_worker;
GRANT USAGE ON SCHEMA public TO spyglass_account_provisioning_worker;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM spyglass_account_provisioning_worker;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM spyglass_account_provisioning_worker;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM spyglass_account_provisioning_worker;
GRANT SELECT, UPDATE ON account_cell_provision_queue TO spyglass_account_provisioning_worker;
GRANT SELECT ON account_erasure_restore_ledger TO spyglass_account_provisioning_worker;

GRANT EXECUTE ON FUNCTION public.spyglass_schedule_report_recipient(uuid,uuid,text) TO spyglass_integration_connector_worker;

-- Replay prevention must be writable by the tool router, without granting token reads.
GRANT INSERT ON public.tool_context_receipts TO spyglass_app_router;
GRANT SELECT (request_id) ON public.tool_context_receipts TO spyglass_app_router;
