#!/usr/bin/env bash
set -euo pipefail

tls_port="${SPYGLASS_LOCAL_TLS_PORT:-8444}"
public_origin="https://web.infiniteocean.localhost:${tls_port}"
app_origin="https://app.infiniteocean.localhost:${tls_port}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local --env-file "$script_dir/env/local.env" --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml")
root_ca=/tmp/spyglass-local-caddy-root.crt
"${compose[@]}" cp edge:/data/caddy/pki/authorities/local/root.crt "$root_ca" >/dev/null
curl_common=(--fail --silent --show-error --cacert "$root_ca")

curl "${curl_common[@]}" --resolve "app.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  "${app_origin}/health/ready" >/tmp/spyglass-local-app-ready.json
grep -q '"status":"ready"' /tmp/spyglass-local-app-ready.json

for service in app-router app-api-a app-api-b admission-api; do
  "${compose[@]}" exec --no-TTY "$service" /spyglass healthcheck \
    --url=http://127.0.0.1:8080/health/ready
done

cell_count="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT count(*) FROM cells WHERE state='active' AND route_origin IS NOT NULL")"
test "$cell_count" = "2"

runtime_role_count="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT count(*) FROM pg_roles WHERE rolname IN ('spyglass_account_api','spyglass_app_router','spyglass_admission_api') AND NOT rolsuper AND NOT rolbypassrls")"
test "$runtime_role_count" = "3"

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
