#!/usr/bin/env bash
set -euo pipefail

tls_port="${SPYGLASS_LOCAL_TLS_PORT:-8444}"
smtp_port="${SPYGLASS_LOCAL_SMTP_PORT:-1026}"
mailpit_port="${SPYGLASS_LOCAL_MAILPIT_PORT:-8025}"
public_origin="https://web.infiniteocean.localhost:${tls_port}"
app_origin="https://app.infiniteocean.localhost:${tls_port}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local --env-file "$script_dir/env/local.env" --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml")
root_ca=/tmp/spyglass-local-caddy-root.crt
bash "$script_dir/../../verify-process-inventory.sh"
"${compose[@]}" cp edge:/data/caddy/pki/authorities/local/root.crt "$root_ca" >/dev/null
curl_common=(--fail --silent --show-error --cacert "$root_ca")

curl "${curl_common[@]}" --resolve "app.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${app_origin}/health/ready" >/tmp/spyglass-local-app-ready.json
grep -q '"status":"ready"' /tmp/spyglass-local-app-ready.json

for service in app-router app-api-a app-api-b admission-api; do
  "${compose[@]}" exec --no-TTY "$service" /spyglass healthcheck \
    --url=http://127.0.0.1:8080/health/ready
done

worker_services=(billing-worker notification-worker entitlement-worker account-lifecycle-worker identity-maintenance-worker work-reconciler-a work-reconciler-b \
  baseline-maintenance-worker-a baseline-maintenance-worker-b \
  route-receipt-worker-a route-receipt-worker-b agent-dispatch-worker-a \
  agent-dispatch-worker-b agent-projection-worker-a agent-projection-worker-b)
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
  --command="SELECT count(*) FROM pg_roles WHERE rolname IN ('spyglass_account_api','spyglass_app_router','spyglass_admission_api','spyglass_billing_worker','spyglass_notification_worker','spyglass_entitlement_worker','spyglass_account_lifecycle_worker','spyglass_work_reconciler','spyglass_baseline_maintenance_worker') AND NOT rolsuper AND NOT rolbypassrls")"
test "$runtime_role_count" = "9"

for database in cell-a-db cell-b-db; do
  cell_runtime_role_count="$("${compose[@]}" exec --no-TTY "$database" psql \
    --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
    --command="SELECT count(*) FROM pg_roles WHERE rolname IN ('spyglass_app_api','spyglass_route_receipt_worker','spyglass_work_reconciler','spyglass_agent_dispatch_worker','spyglass_agent_projection_worker','spyglass_knowledge_document_worker','spyglass_baseline_maintenance_worker','spyglass_runner_controller','spyglass_runner_broker') AND NOT rolsuper AND NOT rolbypassrls")"
  test "$cell_runtime_role_count" = "9"
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
assert_role_denied global-db spyglass_notification_worker 'SELECT count(*) FROM accounts'
assert_role_denied global-db spyglass_entitlement_worker 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_account_lifecycle_worker 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_work_reconciler 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_baseline_maintenance_worker 'SELECT count(*) FROM users'
for database in cell-a-db cell-b-db; do
  assert_role_denied "$database" spyglass_route_receipt_worker 'SELECT count(*) FROM spyglass.work_items'
  assert_role_denied "$database" spyglass_work_reconciler 'SELECT count(*) FROM spyglass.agent_invocations'
  assert_role_denied "$database" spyglass_agent_dispatch_worker 'SELECT count(*) FROM spyglass.work_items'
  assert_role_denied "$database" spyglass_agent_projection_worker 'SELECT count(*) FROM spyglass.work_items'
  assert_role_denied "$database" spyglass_knowledge_document_worker 'SELECT count(*) FROM spyglass.work_items'
  assert_role_denied "$database" spyglass_knowledge_document_worker 'SELECT count(*) FROM spyglass.knowledge_claims'
  assert_role_denied "$database" spyglass_baseline_maintenance_worker 'SELECT count(*) FROM spyglass.knowledge_claims'
  assert_role_denied "$database" spyglass_baseline_maintenance_worker 'SELECT count(*) FROM spyglass.baseline_maintenance_queue'
  assert_role_denied "$database" spyglass_runner_controller 'SELECT count(*) FROM spyglass.work_items'
  assert_role_denied "$database" spyglass_runner_controller 'SELECT count(*) FROM spyglass.runner_invocation_exchanges'
  assert_role_denied "$database" spyglass_runner_broker 'SELECT count(*) FROM spyglass.runner_invocation_exchanges'
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
  "${public_origin}/signup" >/tmp/spyglass-local-signup.html
grep -q "https://app.infiniteocean.localhost:${tls_port}/signup" /tmp/spyglass-local-signup.html

curl "${curl_common[@]}" --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${public_origin}/api/catalog" >/tmp/spyglass-local-catalog.json
jq -e '.version > 0 and (.packages | length) > 0' /tmp/spyglass-local-catalog.json >/dev/null

routed_status="$(curl --silent --show-error --cacert "$root_ca" \
  --resolve "app.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  --output /tmp/spyglass-local-routed.json --write-out '%{http_code}' \
  "${app_origin}/api/v1/accounts/92000000-0000-4000-8000-000000000002/work-items")"
test "$routed_status" = "401"

printf '%s\n' "local Docker smoke verification passed"
