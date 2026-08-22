DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_app_api') THEN
    CREATE ROLE spyglass_app_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_route_receipt_worker') THEN
    CREATE ROLE spyglass_route_receipt_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_work_reconciler') THEN
    CREATE ROLE spyglass_work_reconciler LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_agent_dispatch_worker') THEN
    CREATE ROLE spyglass_agent_dispatch_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_agent_projection_worker') THEN
    CREATE ROLE spyglass_agent_projection_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_knowledge_document_worker') THEN
    CREATE ROLE spyglass_knowledge_document_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_baseline_maintenance_worker') THEN
    CREATE ROLE spyglass_baseline_maintenance_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_prototype_migration') THEN
    CREATE ROLE spyglass_prototype_migration LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_runner_controller') THEN
    CREATE ROLE spyglass_runner_controller LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spyglass_runner_broker') THEN
    CREATE ROLE spyglass_runner_broker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
END
$$;

\getenv app_api_password SPYGLASS_APP_API_DATABASE_PASSWORD
\getenv route_receipt_password SPYGLASS_ROUTE_RECEIPT_WORKER_DATABASE_PASSWORD
\getenv work_reconciler_password SPYGLASS_WORK_RECONCILER_DATABASE_PASSWORD
\getenv agent_dispatch_password SPYGLASS_AGENT_DISPATCH_WORKER_DATABASE_PASSWORD
\getenv agent_projection_password SPYGLASS_AGENT_PROJECTION_WORKER_DATABASE_PASSWORD
\getenv knowledge_document_password SPYGLASS_KNOWLEDGE_DOCUMENT_WORKER_DATABASE_PASSWORD
\getenv baseline_maintenance_password SPYGLASS_BASELINE_MAINTENANCE_WORKER_DATABASE_PASSWORD
\getenv prototype_migration_password SPYGLASS_PROTOTYPE_MIGRATION_DATABASE_PASSWORD
\getenv runner_controller_password SPYGLASS_RUNNER_CONTROLLER_DATABASE_PASSWORD
\getenv runner_broker_password SPYGLASS_RUNNER_BROKER_DATABASE_PASSWORD
SELECT format('ALTER ROLE spyglass_app_api PASSWORD %L', :'app_api_password') \gexec
SELECT format('ALTER ROLE spyglass_route_receipt_worker PASSWORD %L', :'route_receipt_password') \gexec
SELECT format('ALTER ROLE spyglass_work_reconciler PASSWORD %L', :'work_reconciler_password') \gexec
SELECT format('ALTER ROLE spyglass_agent_dispatch_worker PASSWORD %L', :'agent_dispatch_password') \gexec
SELECT format('ALTER ROLE spyglass_agent_projection_worker PASSWORD %L', :'agent_projection_password') \gexec
SELECT format('ALTER ROLE spyglass_knowledge_document_worker PASSWORD %L', :'knowledge_document_password') \gexec
SELECT format('ALTER ROLE spyglass_baseline_maintenance_worker PASSWORD %L', :'baseline_maintenance_password') \gexec
SELECT format('ALTER ROLE spyglass_prototype_migration PASSWORD %L', :'prototype_migration_password') \gexec
SELECT format('ALTER ROLE spyglass_runner_controller PASSWORD %L', :'runner_controller_password') \gexec
SELECT format('ALTER ROLE spyglass_runner_broker PASSWORD %L', :'runner_broker_password') \gexec

GRANT CONNECT ON DATABASE spyglass TO spyglass_app_api, spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker, spyglass_knowledge_document_worker, spyglass_baseline_maintenance_worker, spyglass_prototype_migration,
  spyglass_runner_controller, spyglass_runner_broker;
GRANT USAGE ON SCHEMA public, spyglass TO spyglass_app_api, spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker, spyglass_knowledge_document_worker, spyglass_baseline_maintenance_worker, spyglass_prototype_migration,
  spyglass_runner_controller, spyglass_runner_broker;

REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public, spyglass FROM spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker, spyglass_knowledge_document_worker, spyglass_baseline_maintenance_worker, spyglass_prototype_migration,
  spyglass_runner_controller, spyglass_runner_broker;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public, spyglass FROM spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker, spyglass_knowledge_document_worker, spyglass_baseline_maintenance_worker, spyglass_prototype_migration,
  spyglass_runner_controller, spyglass_runner_broker;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public, spyglass FROM spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker, spyglass_knowledge_document_worker, spyglass_baseline_maintenance_worker, spyglass_prototype_migration,
  spyglass_runner_controller, spyglass_runner_broker;
GRANT SELECT ON spyglass.account_erasure_restore_ledger TO spyglass_app_api;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA spyglass TO spyglass_app_api;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA spyglass TO spyglass_app_api;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public, spyglass TO spyglass_app_api;

GRANT SELECT ON spyglass.account_erasure_restore_ledger TO spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker, spyglass_knowledge_document_worker, spyglass_baseline_maintenance_worker, spyglass_prototype_migration,
  spyglass_runner_controller, spyglass_runner_broker;

GRANT SELECT, UPDATE, DELETE ON spyglass.route_context_receipt_cleanup_queue,
  spyglass.route_context_receipts TO spyglass_route_receipt_worker;

GRANT SELECT, UPDATE, DELETE ON spyglass.work_capacity_release_queue TO spyglass_work_reconciler;
GRANT SELECT, UPDATE ON spyglass.work_items TO spyglass_work_reconciler;
GRANT SELECT ON spyglass.account_namespaces TO spyglass_work_reconciler;

GRANT SELECT ON spyglass.agent_invocation_execution_plans, spyglass.agent_invocations, spyglass.agent_runs,
  spyglass.agent_persona_versions, spyglass.agent_user_messages, spyglass.agent_messages
  TO spyglass_agent_dispatch_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_claim_work_agent_execution(uuid,timestamptz,integer)
  TO spyglass_agent_dispatch_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_heartbeat_work_agent_execution(uuid,uuid,uuid,timestamptz,integer)
  TO spyglass_agent_dispatch_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_load_work_agent_execution(uuid,uuid,uuid,uuid)
  TO spyglass_agent_dispatch_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_start_link_work_agent_execution(uuid,uuid,uuid,bigint,bigint,bytea,uuid,uuid,uuid,text,text[],uuid[],uuid[],timestamptz,timestamptz)
  TO spyglass_agent_dispatch_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_fail_work_agent_execution(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer)
  TO spyglass_agent_dispatch_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_work_agent_execution_stats(timestamptz)
  TO spyglass_agent_dispatch_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_dispatch(uuid,timestamptz,integer)
  TO spyglass_agent_dispatch_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_complete_agent_dispatch(uuid,uuid,uuid,bytea,timestamptz)
  TO spyglass_agent_dispatch_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_fail_agent_dispatch(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer)
  TO spyglass_agent_dispatch_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_agent_dispatch_stats(timestamptz)
  TO spyglass_agent_dispatch_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_provision_runner_invocation(uuid,uuid,text,timestamptz,bytea,bytea,integer,bytea,timestamptz)
  TO spyglass_agent_dispatch_worker;

GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection_v4(uuid,timestamptz,integer)
  TO spyglass_agent_projection_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_success_v3(uuid,uuid,uuid,uuid,text,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,bigint,timestamptz,timestamptz,jsonb,jsonb,uuid)
  TO spyglass_agent_projection_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_failure(uuid,uuid,uuid,bytea,text,timestamptz,timestamptz)
  TO spyglass_agent_projection_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_fail_agent_result_projection(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer)
  TO spyglass_agent_projection_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_agent_result_projection_stats(timestamptz)
  TO spyglass_agent_projection_worker;

GRANT SELECT ON spyglass.knowledge_documents TO spyglass_knowledge_document_worker;
GRANT SELECT, UPDATE ON spyglass.knowledge_document_revisions TO spyglass_knowledge_document_worker;
GRANT INSERT ON spyglass.knowledge_document_chunks TO spyglass_knowledge_document_worker;
-- Event insertion uses ON CONFLICT DO NOTHING for exact crash replay. With
-- forced RLS PostgreSQL checks SELECT and UPDATE authority on the conflict
-- target even though DO NOTHING never updates it; the immutable-history
-- trigger still rejects every actual UPDATE, and DELETE remains ungranted.
GRANT SELECT, INSERT, UPDATE ON spyglass.knowledge_document_events TO spyglass_knowledge_document_worker;
GRANT SELECT, INSERT ON spyglass.knowledge_document_deletion_receipts TO spyglass_knowledge_document_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_claim_knowledge_document_processing(uuid,timestamptz,integer)
  TO spyglass_knowledge_document_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_complete_knowledge_document_processing(uuid,uuid,uuid,timestamptz)
  TO spyglass_knowledge_document_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_fail_knowledge_document_processing(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer)
  TO spyglass_knowledge_document_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_knowledge_document_processing_stats(timestamptz)
  TO spyglass_knowledge_document_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_claim_knowledge_document_deletion(uuid,timestamptz,integer)
  TO spyglass_knowledge_document_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_complete_knowledge_document_deletion(uuid,uuid,uuid,timestamptz)
  TO spyglass_knowledge_document_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_fail_knowledge_document_deletion(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer)
  TO spyglass_knowledge_document_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_knowledge_document_deletion_stats(timestamptz)
  TO spyglass_knowledge_document_worker;

GRANT SELECT ON spyglass.baseline_assessments, spyglass.baseline_interview_answers,
  spyglass.baseline_requirements, spyglass.baseline_evidence_decisions,
  spyglass.baseline_plans, spyglass.baseline_plan_work TO spyglass_baseline_maintenance_worker;
GRANT SELECT, INSERT ON spyglass.work_items, spyglass.work_item_events,
  spyglass.work_item_number_counters TO spyglass_baseline_maintenance_worker;
GRANT UPDATE ON spyglass.work_item_number_counters TO spyglass_baseline_maintenance_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_claim_baseline_maintenance(uuid,timestamptz,integer)
  TO spyglass_baseline_maintenance_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_complete_baseline_maintenance(uuid,uuid,uuid,timestamptz,timestamptz)
  TO spyglass_baseline_maintenance_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_fail_baseline_maintenance(uuid,uuid,uuid,timestamptz,boolean,timestamptz,text,timestamptz,integer)
  TO spyglass_baseline_maintenance_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_baseline_maintenance_stats(timestamptz)
  TO spyglass_baseline_maintenance_worker;

GRANT SELECT, INSERT ON spyglass.knowledge_evidence, spyglass.knowledge_claims,
  spyglass.knowledge_claim_citations, spyglass.knowledge_events,
  spyglass.knowledge_documents, spyglass.knowledge_document_revisions,
  spyglass.knowledge_document_events, spyglass.prototype_migration_receipts,
  spyglass.prototype_migration_events TO spyglass_prototype_migration;
GRANT SELECT ON spyglass.knowledge_document_chunks TO spyglass_prototype_migration;
GRANT SELECT, INSERT, UPDATE ON spyglass.prototype_migration_runs TO spyglass_prototype_migration;

GRANT SELECT, UPDATE ON spyglass.runner_account_scheduling, spyglass.runner_invocation_queue
  TO spyglass_runner_controller;
GRANT EXECUTE ON FUNCTION public.spyglass_prune_runner_terminal_payloads(timestamptz,timestamptz,integer)
  TO spyglass_runner_controller;

GRANT EXECUTE ON FUNCTION public.spyglass_claim_runner_exchange(uuid,uuid,text,text,timestamptz)
  TO spyglass_runner_broker;
GRANT EXECUTE ON FUNCTION public.spyglass_submit_runner_result(uuid,uuid,text,text,text,bytea,bytea,integer,bytea,timestamptz)
  TO spyglass_runner_broker;
GRANT EXECUTE ON FUNCTION public.spyglass_record_runner_capability_event(uuid,uuid,uuid,uuid,uuid,text,text,text,text,timestamptz)
  TO spyglass_runner_broker;
GRANT EXECUTE ON FUNCTION public.spyglass_begin_runner_action_v2(uuid,uuid,uuid,uuid,text,bytea,uuid,timestamptz,timestamptz)
  TO spyglass_runner_broker;
GRANT EXECUTE ON FUNCTION public.spyglass_complete_runner_action_v2(uuid,uuid,uuid,uuid,text,bytea,text,uuid,timestamptz,text,text,timestamptz)
  TO spyglass_runner_broker;
GRANT EXECUTE ON FUNCTION public.spyglass_runner_action_stats(timestamptz)
  TO spyglass_runner_broker;
