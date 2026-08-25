#!/usr/bin/env bash
set -euo pipefail

tls_port="${SPYGLASS_LOCAL_TLS_PORT:-8444}"
smtp_port="${SPYGLASS_LOCAL_SMTP_PORT:-1026}"
mailpit_port="${SPYGLASS_LOCAL_MAILPIT_PORT:-8025}"
public_origin="https://web.infiniteocean.localhost:${tls_port}"
app_origin="https://app.infiniteocean.localhost:${tls_port}"
mcp_origin="https://mcp.infiniteocean.localhost:${tls_port}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local --env-file "$script_dir/env/local.env" --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml")
root_ca=/tmp/spyglass-local-caddy-root.crt
bash "$script_dir/../../verify-process-inventory.sh"
"${compose[@]}" cp edge:/data/caddy/pki/authorities/local/root.crt "$root_ca" >/dev/null
curl_common=(--fail --silent --show-error --cacert "$root_ca")

curl "${curl_common[@]}" --resolve "app.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${app_origin}/health/ready" >/tmp/spyglass-local-app-ready.json
grep -q '"status":"ready"' /tmp/spyglass-local-app-ready.json
curl "${curl_common[@]}" --resolve "app.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${app_origin}/.well-known/oauth-authorization-server" >/tmp/spyglass-local-oauth-metadata.json
jq -e --arg issuer "$app_origin" --arg resource "$mcp_origin" '.issuer==$issuer and .authorization_endpoint==($issuer+"/oauth/authorize") and .client_id_metadata_document_supported==true and (.scopes_supported==["spyglass:mcp"])' /tmp/spyglass-local-oauth-metadata.json >/dev/null
curl "${curl_common[@]}" --resolve "mcp.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${mcp_origin}/.well-known/oauth-protected-resource" >/tmp/spyglass-local-mcp-metadata.json
jq -e --arg resource "$mcp_origin" --arg issuer "$app_origin" '.resource==$resource and .authorization_servers==[$issuer] and .scopes_supported==["spyglass:mcp"]' /tmp/spyglass-local-mcp-metadata.json >/dev/null

for service in app-router mcp-gateway app-api-a app-api-b admission-api; do
  "${compose[@]}" exec --no-TTY "$service" /spyglass healthcheck \
    --url=http://127.0.0.1:8080/health/ready
done

worker_services=(billing-worker notification-worker entitlement-worker account-lifecycle-worker account-export-build-worker-a account-export-build-worker-b account-export-expiry-worker identity-maintenance-worker work-reconciler-a work-reconciler-b \
  baseline-maintenance-worker-a baseline-maintenance-worker-b \
  route-receipt-worker-a route-receipt-worker-b agent-dispatch-worker-a \
  agent-dispatch-worker-b schedule-execution-worker-a schedule-execution-worker-b \
  agent-projection-worker-a agent-projection-worker-b)
for service in "${worker_services[@]}"; do
  "${compose[@]}" exec --no-TTY "$service" /spyglass healthcheck \
    --url=http://127.0.0.1:8081/health/ready
done

worker_ids=()
for service in "${worker_services[@]}"; do
  worker_ids+=("$("${compose[@]}" ps --quiet "$service")")
done
worker_statuses="$(docker inspect -f '{{(index .NetworkSettings.Networks "spyglass-local_application").IPAddress}}' "${worker_ids[@]}" | \
  xargs -I{} curl --fail --silent --show-error http://{}:8081/health/status)"
jq -s -e --argjson expected "${#worker_services[@]}" 'length == $expected and all(.[]; (.failures // 0) == 0)' <<<"$worker_statuses" >/dev/null

cell_count="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT count(*) FROM cells WHERE state='active' AND route_origin IS NOT NULL")"
test "$cell_count" = "2"

runtime_role_count="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
    --command="SELECT count(*) FROM pg_roles WHERE rolname IN ('spyglass_account_api','spyglass_app_router','spyglass_mcp_gateway','spyglass_admission_api','spyglass_billing_worker','spyglass_notification_worker','spyglass_entitlement_worker','spyglass_account_lifecycle_worker','spyglass_account_export_build_worker','spyglass_account_export_expiry_worker','spyglass_work_reconciler','spyglass_baseline_maintenance_worker','spyglass_prototype_migration','spyglass_integration_connector_worker') AND NOT rolsuper AND NOT rolbypassrls")"
test "$runtime_role_count" = "14"

export_global_privileges="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT has_table_privilege('spyglass_account_export_build_worker','accounts','SELECT') AND has_table_privilege('spyglass_account_export_build_worker','account_export_requests','SELECT') AND has_column_privilege('spyglass_account_export_build_worker','account_export_requests','state','UPDATE') AND NOT has_column_privilege('spyglass_account_export_build_worker','account_export_requests','account_id','UPDATE') AND has_table_privilege('spyglass_account_export_build_worker','account_export_events','INSERT') AND NOT has_table_privilege('spyglass_account_export_build_worker','billing_event_inbox','SELECT') AND has_table_privilege('spyglass_account_export_expiry_worker','account_export_requests','SELECT') AND has_column_privilege('spyglass_account_export_expiry_worker','account_export_requests','deleted_at','UPDATE') AND NOT has_column_privilege('spyglass_account_export_expiry_worker','account_export_requests','artifact_sha256','UPDATE') AND has_table_privilege('spyglass_account_export_expiry_worker','account_export_events','INSERT') AND NOT has_table_privilege('spyglass_account_export_expiry_worker','accounts','SELECT')")"
test "$export_global_privileges" = "t"

mcp_export_privileges="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT has_table_privilege('spyglass_mcp_gateway','account_export_requests','SELECT,INSERT,UPDATE') AND NOT has_table_privilege('spyglass_mcp_gateway','account_export_requests','DELETE') AND has_table_privilege('spyglass_mcp_gateway','account_export_events','INSERT') AND NOT has_table_privilege('spyglass_mcp_gateway','account_export_events','SELECT,UPDATE,DELETE') AND has_function_privilege('spyglass_mcp_gateway','spyglass_authenticate_mcp_access_token(bytea,text,text,timestamptz)','EXECUTE')")"
test "$mcp_export_privileges" = "t"

for database in cell-a-db cell-b-db; do
  cell_runtime_role_count="$("${compose[@]}" exec --no-TTY "$database" psql \
    --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
    --command="SELECT count(*) FROM pg_roles WHERE rolname IN ('spyglass_app_api','spyglass_route_receipt_worker','spyglass_work_reconciler','spyglass_agent_dispatch_worker','spyglass_schedule_execution_worker','spyglass_agent_projection_worker','spyglass_knowledge_document_worker','spyglass_baseline_maintenance_worker','spyglass_prototype_migration','spyglass_runner_controller','spyglass_runner_broker','spyglass_integration_connector_worker','spyglass_account_export_build_worker') AND NOT rolsuper AND NOT rolbypassrls")"
  test "$cell_runtime_role_count" = "13"
  export_cell_privileges="$("${compose[@]}" exec --no-TTY "$database" psql \
    --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
    --command="SELECT has_table_privilege('spyglass_account_export_build_worker','spyglass.work_items','SELECT') AND has_table_privilege('spyglass_account_export_build_worker','spyglass.knowledge_documents','SELECT') AND has_table_privilege('spyglass_account_export_build_worker','spyglass.marketing_assets','SELECT') AND NOT has_table_privilege('spyglass_account_export_build_worker','spyglass.integration_credentials','SELECT') AND NOT has_table_privilege('spyglass_account_export_build_worker','spyglass.work_items','INSERT,UPDATE,DELETE')")"
  test "$export_cell_privileges" = "t"
  document_event_privileges="$("${compose[@]}" exec --no-TTY "$database" psql \
    --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
    --command="SELECT has_table_privilege('spyglass_knowledge_document_worker','spyglass.knowledge_document_events','SELECT,INSERT,UPDATE') AND NOT has_table_privilege('spyglass_knowledge_document_worker','spyglass.knowledge_document_events','DELETE') AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgname='knowledge_document_events_immutable' AND tgenabled<>'D')")"
  test "$document_event_privileges" = "t"
done

assert_role_denied() {
  local database="$1" role="$2" query="$3"
  if "${compose[@]}" exec --no-TTY "$database" psql --username=spyglass_migrator \
    --dbname=spyglass --set=ON_ERROR_STOP=1 --command="SET ROLE ${role}; ${query}" >/dev/null 2>&1; then
    printf 'role %s unexpectedly executed forbidden query in %s\n' "$role" "$database" >&2
    return 1
  fi
}

assert_role_denied global-db spyglass_billing_worker 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_mcp_gateway 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_mcp_gateway 'SELECT count(*) FROM mcp_oauth_access_tokens'
assert_role_denied global-db spyglass_mcp_gateway 'SELECT count(*) FROM account_export_events'
assert_role_denied global-db spyglass_mcp_gateway 'DELETE FROM account_export_requests'
assert_role_denied global-db spyglass_notification_worker 'SELECT count(*) FROM accounts'
assert_role_denied global-db spyglass_entitlement_worker 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_account_lifecycle_worker 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_work_reconciler 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_baseline_maintenance_worker 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_prototype_migration 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_integration_connector_worker 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_account_export_build_worker 'SELECT count(*) FROM billing_event_inbox'
assert_role_denied global-db spyglass_account_export_build_worker 'DELETE FROM account_export_requests'
assert_role_denied global-db spyglass_account_export_expiry_worker 'SELECT count(*) FROM accounts'
assert_role_denied global-db spyglass_account_export_expiry_worker 'UPDATE accounts SET display_name=display_name'
for database in cell-a-db cell-b-db; do
  assert_role_denied "$database" spyglass_route_receipt_worker 'SELECT count(*) FROM spyglass.work_items'
  assert_role_denied "$database" spyglass_work_reconciler 'SELECT count(*) FROM spyglass.agent_invocations'
  assert_role_denied "$database" spyglass_agent_dispatch_worker 'SELECT count(*) FROM spyglass.work_items'
  assert_role_denied "$database" spyglass_schedule_execution_worker 'SELECT count(*) FROM spyglass.schedules'
  assert_role_denied "$database" spyglass_schedule_execution_worker 'SELECT count(*) FROM spyglass.schedule_dispatch_queue'
  assert_role_denied "$database" spyglass_agent_projection_worker 'SELECT count(*) FROM spyglass.work_items'
  assert_role_denied "$database" spyglass_knowledge_document_worker 'SELECT count(*) FROM spyglass.work_items'
  assert_role_denied "$database" spyglass_knowledge_document_worker 'SELECT count(*) FROM spyglass.knowledge_claims'
  assert_role_denied "$database" spyglass_baseline_maintenance_worker 'SELECT count(*) FROM spyglass.knowledge_claims'
  assert_role_denied "$database" spyglass_baseline_maintenance_worker 'SELECT count(*) FROM spyglass.baseline_maintenance_queue'
  assert_role_denied "$database" spyglass_prototype_migration 'SELECT count(*) FROM spyglass.work_items'
  assert_role_denied "$database" spyglass_prototype_migration 'SELECT count(*) FROM spyglass.knowledge_facts'
  assert_role_denied "$database" spyglass_runner_controller 'SELECT count(*) FROM spyglass.work_items'
  assert_role_denied "$database" spyglass_runner_controller 'SELECT count(*) FROM spyglass.runner_invocation_exchanges'
  assert_role_denied "$database" spyglass_runner_broker 'SELECT count(*) FROM spyglass.runner_invocation_exchanges'
  assert_role_denied "$database" spyglass_integration_connector_worker 'SELECT count(*) FROM spyglass.integration_credentials'
  assert_role_denied "$database" spyglass_integration_connector_worker 'SELECT count(*) FROM spyglass.marketing_campaigns'
  assert_role_denied "$database" spyglass_account_export_build_worker 'SELECT count(*) FROM spyglass.integration_credentials'
  assert_role_denied "$database" spyglass_account_export_build_worker 'UPDATE spyglass.work_items SET version=version'
done

curl --fail --silent --show-error "http://127.0.0.1:${mailpit_port}/readyz" >/dev/null
mail_count_before="$(curl --fail --silent --show-error "http://127.0.0.1:${mailpit_port}/api/v1/messages" | jq -r '.total')"
smtp_transcript="$(printf 'EHLO verify.local\r\nMAIL FROM:<verify@infiniteocean.localhost>\r\nRCPT TO:<capture@infiniteocean.localhost>\r\nDATA\r\nFrom: verify@infiniteocean.localhost\r\nTo: capture@infiniteocean.localhost\r\nSubject: Spyglass TLS SMTP verification\r\n\r\nContent-free local verification.\r\n.\r\nQUIT\r\n' | \
  timeout 10s openssl s_client -connect "127.0.0.1:${smtp_port}" -servername mailpit \
    -verify_hostname mailpit -CAfile "$script_dir/mailpit/ca.crt" -verify_return_error -quiet 2>&1)"
grep -q 'queued as' <<<"$smtp_transcript"
for _ in $(seq 1 20); do
  mail_count_after="$(curl --fail --silent --show-error "http://127.0.0.1:${mailpit_port}/api/v1/messages" | jq -r '.total')"
  if ((mail_count_after > mail_count_before)); then
    break
  fi
  sleep 0.1
done
test "$mail_count_after" -gt "$mail_count_before"

curl "${curl_common[@]}" --dump-header /tmp/spyglass-local-website-headers.txt \
  --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${public_origin}/" >/tmp/spyglass-local-website.html
grep -qi '^content-security-policy:' /tmp/spyglass-local-website-headers.txt
grep -q 'Infinite Ocean: Spyglass' /tmp/spyglass-local-website.html

curl "${curl_common[@]}" --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${public_origin}/pricing" >/tmp/spyglass-local-pricing.html
grep -q "https://app.infiniteocean.localhost:${tls_port}/signup" /tmp/spyglass-local-pricing.html

curl "${curl_common[@]}" --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${public_origin}/catalog.json" >/tmp/spyglass-local-catalog.json
jq -e '.version > 0 and (.packages | length) > 0' /tmp/spyglass-local-catalog.json >/dev/null

private_routes=(
  /app/your-turn
  /app/your-turn/work-item/10000000-0000-4000-8000-000000000001
  /app/work/10000000-0000-4000-8000-000000000001
  /app/knowledge/claims/10000000-0000-4000-8000-000000000001
  /app/agents/boardrooms/10000000-0000-4000-8000-000000000001
  /app/agents/boardrooms/10000000-0000-4000-8000-000000000001/conversations/20000000-0000-4000-8000-000000000002
  /app/schedules
  /app/schedules/10000000-0000-4000-8000-000000000001
  /app/account
  /app/billing
  /app/security
  /app/account-exports
  /app/account-closures
  /app/privacy
  /app/checkout
  /app/affiliate
)
for route in "${private_routes[@]}"; do
  curl "${curl_common[@]}" --resolve "app.infiniteocean.localhost:${tls_port}:127.0.0.1" \
    "${app_origin}${route}" >/tmp/spyglass-local-private-ui.html
  grep -q '<div id="app"></div>' /tmp/spyglass-local-private-ui.html
  grep -q '/ui-assets/' /tmp/spyglass-local-private-ui.html
done

routed_status="$(curl --silent --show-error --cacert "$root_ca" \
  --resolve "app.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  --output /tmp/spyglass-local-routed.json --write-out '%{http_code}' \
  "${app_origin}/api/v1/accounts/92000000-0000-4000-8000-000000000002/work-items")"
test "$routed_status" = "401"

printf '%s\n' "local Docker smoke verification passed"
