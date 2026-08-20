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
END
$$;

\getenv app_api_password SPYGLASS_APP_API_DATABASE_PASSWORD
\getenv route_receipt_password SPYGLASS_ROUTE_RECEIPT_WORKER_DATABASE_PASSWORD
\getenv work_reconciler_password SPYGLASS_WORK_RECONCILER_DATABASE_PASSWORD
\getenv agent_dispatch_password SPYGLASS_AGENT_DISPATCH_WORKER_DATABASE_PASSWORD
\getenv agent_projection_password SPYGLASS_AGENT_PROJECTION_WORKER_DATABASE_PASSWORD
SELECT format('ALTER ROLE spyglass_app_api PASSWORD %L', :'app_api_password') \gexec
SELECT format('ALTER ROLE spyglass_route_receipt_worker PASSWORD %L', :'route_receipt_password') \gexec
SELECT format('ALTER ROLE spyglass_work_reconciler PASSWORD %L', :'work_reconciler_password') \gexec
SELECT format('ALTER ROLE spyglass_agent_dispatch_worker PASSWORD %L', :'agent_dispatch_password') \gexec
SELECT format('ALTER ROLE spyglass_agent_projection_worker PASSWORD %L', :'agent_projection_password') \gexec

GRANT CONNECT ON DATABASE spyglass TO spyglass_app_api, spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker;
GRANT USAGE ON SCHEMA public, spyglass TO spyglass_app_api, spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker;

REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public, spyglass FROM spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public, spyglass FROM spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public, spyglass FROM spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker;
GRANT SELECT ON spyglass.account_erasure_restore_ledger TO spyglass_app_api;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA spyglass TO spyglass_app_api;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA spyglass TO spyglass_app_api;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public, spyglass TO spyglass_app_api;

GRANT SELECT ON spyglass.account_erasure_restore_ledger TO spyglass_route_receipt_worker,
  spyglass_work_reconciler, spyglass_agent_dispatch_worker, spyglass_agent_projection_worker;

GRANT SELECT, UPDATE, DELETE ON spyglass.route_context_receipt_cleanup_queue,
  spyglass.route_context_receipts TO spyglass_route_receipt_worker;

GRANT SELECT, UPDATE, DELETE ON spyglass.work_capacity_release_queue TO spyglass_work_reconciler;
GRANT SELECT, UPDATE ON spyglass.work_items TO spyglass_work_reconciler;
GRANT SELECT ON spyglass.account_namespaces TO spyglass_work_reconciler;

GRANT SELECT ON spyglass.agent_invocation_execution_plans, spyglass.agent_invocations,
  spyglass.agent_persona_versions, spyglass.agent_user_messages, spyglass.agent_messages
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

GRANT EXECUTE ON FUNCTION public.spyglass_claim_agent_result_projection(uuid,timestamptz,integer)
  TO spyglass_agent_projection_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_success(uuid,uuid,uuid,uuid,text,text,text,bytea,bytea,jsonb,text,bigint,bigint,bigint,timestamptz,timestamptz)
  TO spyglass_agent_projection_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_project_agent_invocation_failure(uuid,uuid,uuid,bytea,text,timestamptz,timestamptz)
  TO spyglass_agent_projection_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_fail_agent_result_projection(uuid,uuid,uuid,boolean,timestamptz,text,timestamptz,integer)
  TO spyglass_agent_projection_worker;
GRANT EXECUTE ON FUNCTION public.spyglass_agent_result_projection_stats(timestamptz)
  TO spyglass_agent_projection_worker;
