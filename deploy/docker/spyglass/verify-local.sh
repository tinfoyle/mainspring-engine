#!/usr/bin/env bash
set -euo pipefail

tls_port="${SPYGLASS_LOCAL_TLS_PORT:-8444}"
smtp_port="${SPYGLASS_LOCAL_SMTP_PORT:-1026}"
mailpit_port="${SPYGLASS_LOCAL_MAILPIT_PORT:-8025}"
public_origin="https://web.infiniteocean.localhost:${tls_port}"
app_origin="https://app.infiniteocean.localhost:${tls_port}"
mcp_origin="https://mcp.infiniteocean.localhost:${tls_port}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose_project="${SPYGLASS_COMPOSE_PROJECT_NAME:-spyglass-local}"
compose=(docker compose --project-name "$compose_project" --env-file "$script_dir/env/local.env" --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml")
root_ca=/tmp/spyglass-local-caddy-root.crt
bash "$script_dir/../../verify-process-inventory.sh"
"${compose[@]}" cp edge:/data/caddy/pki/authorities/local/root.crt "$root_ca" >/dev/null
curl_common=(--fail --silent --show-error --cacert "$root_ca")

assert_public_discovery() {
  local html_file="$1" canonical_path="$2" canonical_url
  if [[ "$canonical_path" = "/" ]]; then
    canonical_url="https://www.infiniteocean.net/"
  else
    canonical_url="https://www.infiniteocean.net${canonical_path}"
  fi
  grep -Fq '<meta name="robots" content="index, follow, max-image-preview:large">' "$html_file"
  grep -Fq '<meta property="og:site_name" content="Infinite Ocean">' "$html_file"
  grep -Fq "<meta property=\"og:url\" content=\"${canonical_url}\">" "$html_file"
  grep -Fq '<meta property="og:image" content="https://www.infiniteocean.net/og/spyglass-social.png">' "$html_file"
  grep -Fq '<meta property="og:image:width" content="1200">' "$html_file"
  grep -Fq '<meta property="og:image:height" content="630">' "$html_file"
  grep -Fq '<meta name="twitter:card" content="summary_large_image">' "$html_file"
  grep -Fq "<link rel=\"canonical\" href=\"${canonical_url}\">" "$html_file"
  grep -Fq '<script id="spyglass-structured-data" type="application/ld+json">' "$html_file"
  grep -Fq '"@context":"https://schema.org"' "$html_file"
}

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

worker_services=(billing-worker notification-worker entitlement-worker account-lifecycle-worker account-export-build-worker-a account-export-build-worker-b account-export-expiry-worker identity-maintenance-worker affiliate-retention-worker work-reconciler-a work-reconciler-b \
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
worker_statuses="$(docker inspect -f "{{(index .NetworkSettings.Networks \"${compose_project}_application\").IPAddress}}" "${worker_ids[@]}" | \
  xargs -I{} curl --fail --silent --show-error http://{}:8081/health/status)"
jq -s -e --argjson expected "${#worker_services[@]}" 'length == $expected and all(.[]; (.failures // 0) == 0)' <<<"$worker_statuses" >/dev/null

cell_count="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT count(*) FROM cells WHERE state='active' AND route_origin IS NOT NULL")"
test "$cell_count" = "2"

launch_catalog="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT version=3 AND state='published' AND content->'plans'->0->>'code'='team' AND (content->'offers'->0->>'amount_minor')::bigint=5000 AND jsonb_array_length(content->'ai_complexity_rates')=5 AND EXISTS (SELECT 1 FROM offer_provider_prices WHERE catalog_version=3 AND mode='test' GROUP BY catalog_version HAVING count(*)=3) FROM catalog_publications WHERE state='published'")"
test "$launch_catalog" = "t"

runtime_role_count="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
    --command="SELECT count(*) FROM pg_roles WHERE rolname IN ('spyglass_account_api','spyglass_app_router','spyglass_mcp_gateway','spyglass_admission_api','spyglass_billing_worker','spyglass_notification_worker','spyglass_entitlement_worker','spyglass_account_lifecycle_worker','spyglass_account_export_build_worker','spyglass_account_export_expiry_worker','spyglass_identity_maintenance_worker','spyglass_affiliate_retention_worker','spyglass_work_reconciler','spyglass_baseline_maintenance_worker','spyglass_prototype_migration','spyglass_integration_connector_worker') AND NOT rolsuper AND NOT rolbypassrls")"
test "$runtime_role_count" = "16"

export_global_privileges="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT has_table_privilege('spyglass_account_export_build_worker','accounts','SELECT') AND has_table_privilege('spyglass_account_export_build_worker','account_export_requests','SELECT') AND has_column_privilege('spyglass_account_export_build_worker','account_export_requests','state','UPDATE') AND NOT has_column_privilege('spyglass_account_export_build_worker','account_export_requests','account_id','UPDATE') AND has_table_privilege('spyglass_account_export_build_worker','account_export_events','INSERT') AND NOT has_table_privilege('spyglass_account_export_build_worker','billing_event_inbox','SELECT') AND has_table_privilege('spyglass_account_export_expiry_worker','account_export_requests','SELECT') AND has_column_privilege('spyglass_account_export_expiry_worker','account_export_requests','deleted_at','UPDATE') AND NOT has_column_privilege('spyglass_account_export_expiry_worker','account_export_requests','artifact_sha256','UPDATE') AND has_table_privilege('spyglass_account_export_expiry_worker','account_export_events','INSERT') AND NOT has_table_privilege('spyglass_account_export_expiry_worker','accounts','SELECT')")"
test "$export_global_privileges" = "t"

mcp_export_privileges="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT has_table_privilege('spyglass_mcp_gateway','account_export_requests','SELECT,INSERT,UPDATE') AND NOT has_table_privilege('spyglass_mcp_gateway','account_export_requests','DELETE') AND has_table_privilege('spyglass_mcp_gateway','account_export_events','INSERT') AND NOT has_table_privilege('spyglass_mcp_gateway','account_export_events','SELECT,UPDATE,DELETE') AND has_table_privilege('spyglass_mcp_gateway','user_mfa_methods','SELECT') AND NOT has_table_privilege('spyglass_mcp_gateway','user_mfa_methods','INSERT,UPDATE,DELETE') AND has_function_privilege('spyglass_mcp_gateway','spyglass_authenticate_mcp_access_token(bytea,text,text,timestamptz)','EXECUTE')")"
test "$mcp_export_privileges" = "t"

admission_token_privileges="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT has_table_privilege('spyglass_admission_api','catalog_publications','SELECT') AND has_table_privilege('spyglass_admission_api','ai_token_grants','SELECT,UPDATE') AND NOT has_table_privilege('spyglass_admission_api','ai_token_grants','INSERT,DELETE') AND has_table_privilege('spyglass_admission_api','ai_token_reservations','SELECT,INSERT,UPDATE') AND NOT has_table_privilege('spyglass_admission_api','ai_token_reservations','DELETE') AND has_table_privilege('spyglass_admission_api','ai_token_reservation_allocations','SELECT,INSERT') AND NOT has_table_privilege('spyglass_admission_api','ai_token_reservation_allocations','UPDATE,DELETE') AND has_table_privilege('spyglass_admission_api','ai_token_ledger_entries','SELECT,INSERT') AND NOT has_table_privilege('spyglass_admission_api','ai_token_ledger_entries','UPDATE,DELETE')")"
test "$admission_token_privileges" = "t"

subscription_lifecycle_privileges="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT has_table_privilege('spyglass_billing_worker','account_subscription_termination_jobs','SELECT,UPDATE') AND NOT has_table_privilege('spyglass_billing_worker','account_subscription_termination_jobs','INSERT,DELETE') AND has_function_privilege('spyglass_billing_worker','spyglass_project_subscription_lifecycle(uuid,text,text,timestamptz,timestamptz,timestamptz)','EXECUTE') AND has_table_privilege('spyglass_notification_worker','account_subscription_lifecycle_notices','SELECT,UPDATE') AND NOT has_table_privilege('spyglass_notification_worker','account_subscription_lifecycle_notices','INSERT,DELETE') AND has_table_privilege('spyglass_notification_worker','identity_notification_outbox','SELECT,INSERT,UPDATE') AND NOT has_table_privilege('spyglass_notification_worker','identity_notification_outbox','DELETE') AND has_table_privilege('spyglass_notification_worker','account_subscription_lifecycles','SELECT') AND has_table_privilege('spyglass_notification_worker','accounts','SELECT') AND has_table_privilege('spyglass_notification_worker','memberships','SELECT') AND has_table_privilege('spyglass_notification_worker','users','SELECT') AND has_function_privilege('spyglass_account_lifecycle_worker','spyglass_claim_subscription_lifecycle(timestamptz,bigint)','EXECUTE') AND has_function_privilege('spyglass_account_lifecycle_worker','spyglass_advance_subscription_lifecycle(uuid,uuid,timestamptz,bigint)','EXECUTE') AND NOT has_table_privilege('spyglass_account_lifecycle_worker','account_subscription_lifecycles','SELECT,INSERT,UPDATE,DELETE')")"
test "$subscription_lifecycle_privileges" = "t"

app_router_security_privileges="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT has_table_privilege('spyglass_app_router','user_mfa_methods','SELECT') AND NOT has_table_privilege('spyglass_app_router','user_mfa_methods','INSERT,UPDATE,DELETE')")"
test "$app_router_security_privileges" = "t"

admission_security_privileges="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT has_table_privilege('spyglass_admission_api','user_mfa_methods','SELECT') AND NOT has_table_privilege('spyglass_admission_api','user_mfa_methods','INSERT,UPDATE,DELETE')")"
test "$admission_security_privileges" = "t"

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
assert_role_denied global-db spyglass_admission_api 'INSERT INTO ai_token_grants DEFAULT VALUES'
assert_role_denied global-db spyglass_admission_api 'DELETE FROM ai_token_ledger_entries'
assert_role_denied global-db spyglass_billing_worker 'INSERT INTO account_subscription_termination_jobs DEFAULT VALUES'
assert_role_denied global-db spyglass_notification_worker 'DELETE FROM account_subscription_lifecycle_notices'
assert_role_denied global-db spyglass_notification_worker 'SELECT count(*) FROM catalog_publications'
assert_role_denied global-db spyglass_entitlement_worker 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_account_lifecycle_worker 'SELECT count(*) FROM users'
assert_role_denied global-db spyglass_account_lifecycle_worker 'SELECT count(*) FROM account_subscription_lifecycles'
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
assert_public_discovery /tmp/spyglass-local-website.html /

curl "${curl_common[@]}" --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${public_origin}/pricing" >/tmp/spyglass-local-pricing.html
grep -q "https://app.infiniteocean.localhost:${tls_port}/signup" /tmp/spyglass-local-pricing.html
assert_public_discovery /tmp/spyglass-local-pricing.html /pricing

for route in features privacy sms-consent affiliate-terms; do
  curl "${curl_common[@]}" --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
    "${public_origin}/${route}" >"/tmp/spyglass-local-${route}.html"
  grep -q '<meta name="description" content="' "/tmp/spyglass-local-${route}.html"
  grep -q '<h1' "/tmp/spyglass-local-${route}.html"
  assert_public_discovery "/tmp/spyglass-local-${route}.html" "/${route}"
done
grep -q 'Complete feature map' /tmp/spyglass-local-features.html
grep -q 'Privacy, in plain language' /tmp/spyglass-local-privacy.html
grep -q 'Text messages are optional and only used for security codes' /tmp/spyglass-local-sms-consent.html
grep -q 'Affiliate program terms' /tmp/spyglass-local-affiliate-terms.html

public_feature_slugs=(your-turn work knowledge baseline agents schedules finance marketing integrations account-administration security export-lifecycle)
for slug in "${public_feature_slugs[@]}"; do
  curl "${curl_common[@]}" --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
    "${public_origin}/features/${slug}" >"/tmp/spyglass-local-feature-${slug}.html"
  grep -q '<meta name="description" content="' "/tmp/spyglass-local-feature-${slug}.html"
  grep -q '<h1' "/tmp/spyglass-local-feature-${slug}.html"
  grep -q 'Primary workflows' "/tmp/spyglass-local-feature-${slug}.html"
  grep -q 'Governance boundaries' "/tmp/spyglass-local-feature-${slug}.html"
  assert_public_discovery "/tmp/spyglass-local-feature-${slug}.html" "/features/${slug}"
done

curl "${curl_common[@]}" --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${public_origin}/robots.txt" >/tmp/spyglass-local-robots.txt
grep -Fq 'User-agent: *' /tmp/spyglass-local-robots.txt
grep -Fq 'Allow: /' /tmp/spyglass-local-robots.txt
grep -Fq 'Disallow: /api/' /tmp/spyglass-local-robots.txt
grep -Fq 'Sitemap: https://www.infiniteocean.net/sitemap.xml' /tmp/spyglass-local-robots.txt

curl "${curl_common[@]}" --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${public_origin}/sitemap.xml" >/tmp/spyglass-local-sitemap.xml
grep -Fq '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">' /tmp/spyglass-local-sitemap.xml
public_routes=(/ /features)
for slug in "${public_feature_slugs[@]}"; do public_routes+=("/features/${slug}"); done
public_routes+=(/pricing /privacy /terms /sms-consent /affiliate-terms)
test "$(grep -o '<url>' /tmp/spyglass-local-sitemap.xml | wc -l)" -eq "${#public_routes[@]}"
for route in "${public_routes[@]}"; do
  if [[ "$route" = "/" ]]; then
    expected_location='https://www.infiniteocean.net/'
  else
    expected_location="https://www.infiniteocean.net${route}"
  fi
  grep -Fq "<loc>${expected_location}</loc>" /tmp/spyglass-local-sitemap.xml
done

curl "${curl_common[@]}" --dump-header /tmp/spyglass-local-social-card-headers.txt \
  --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${public_origin}/og/spyglass-social.png" >/tmp/spyglass-local-social-card.png
grep -qi '^content-type: image/png' /tmp/spyglass-local-social-card-headers.txt
test "$(stat -c '%s' /tmp/spyglass-local-social-card.png)" -gt 100000
test "$(od -An -tx1 -N8 /tmp/spyglass-local-social-card.png | tr -d ' \n')" = '89504e470d0a1a0a'
file /tmp/spyglass-local-social-card.png | grep -Fq 'PNG image data, 1200 x 630'

grep -hoE 'href="/[^"#?]*([#?][^"]*)?"' \
  /tmp/spyglass-local-website.html \
  /tmp/spyglass-local-pricing.html \
  /tmp/spyglass-local-features.html \
  /tmp/spyglass-local-privacy.html \
  /tmp/spyglass-local-affiliate-terms.html \
  /tmp/spyglass-local-feature-*.html | \
  sed -E 's/^href="//; s/"$//; s/[?#].*$//' | \
  grep -Ev '^/_nuxt/' | sort -u >/tmp/spyglass-local-public-links.txt
while IFS= read -r route; do
  status="$(curl --silent --show-error --cacert "$root_ca" --output /dev/null --write-out '%{http_code}' \
    --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" "${public_origin}${route}")"
  if [[ "$status" -lt 200 || "$status" -ge 400 ]]; then
    printf 'public link %s returned HTTP %s\n' "$route" "$status" >&2
    exit 1
  fi
done </tmp/spyglass-local-public-links.txt

curl "${curl_common[@]}" --dump-header /tmp/spyglass-local-catalog-headers.txt \
  --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${public_origin}/catalog.json" >/tmp/spyglass-local-catalog.json
jq -e '.version > 0 and (.packages | length) > 0' /tmp/spyglass-local-catalog.json >/dev/null
grep -qi '^x-spyglass-catalog-state: fresh' /tmp/spyglass-local-catalog-headers.txt
if grep -Eqi '"(stripe|provider|price_id|product_id|customer_id|subscription_id)[^"]*"[[:space:]]*:' /tmp/spyglass-local-catalog.json; then
  printf '%s\n' "public Catalog leaked a provider-specific field" >&2
  exit 1
fi

private_routes=(
  /app/your-turn
  /app/your-turn/work-item/10000000-0000-4000-8000-000000000001
  /app/work/10000000-0000-4000-8000-000000000001
  /app/knowledge/claims/10000000-0000-4000-8000-000000000001
  /app/agents/boardrooms/10000000-0000-4000-8000-000000000001
  /app/agents/boardrooms/10000000-0000-4000-8000-000000000001/conversations/20000000-0000-4000-8000-000000000002
  /app/schedules
  /app/schedules/10000000-0000-4000-8000-000000000001
  /app/finance
  /app/finance/ledgers/10000000-0000-4000-8000-000000000001
  /app/finance/accounts/10000000-0000-4000-8000-000000000002
  /app/finance/entries/10000000-0000-4000-8000-000000000003
  /app/finance/reconciliations/10000000-0000-4000-8000-000000000004
  /app/integrations
  /app/integrations/connections/10000000-0000-4000-8000-000000000001
  /app/integrations/executions/10000000-0000-4000-8000-000000000002
  /app/integrations/authorizations/10000000-0000-4000-8000-000000000003
  /app/baseline
  /app/baseline/10000000-0000-4000-8000-000000000001
  /app/marketing
  /app/marketing/campaigns/10000000-0000-4000-8000-000000000001
  /app/marketing/releases/10000000-0000-4000-8000-000000000002
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
