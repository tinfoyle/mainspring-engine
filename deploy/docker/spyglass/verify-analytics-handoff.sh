#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
tls_port="${SPYGLASS_LOCAL_TLS_PORT:-8444}"
public_origin="https://web.infiniteocean.localhost:${tls_port}"
app_origin="https://app.infiniteocean.localhost:${tls_port}"
cookie_jar="$(mktemp /tmp/spyglass-analytics-handoff-cookies.XXXXXX)"
response_headers="$(mktemp /tmp/spyglass-analytics-handoff-headers.XXXXXX)"
consent_state="$(mktemp /tmp/spyglass-analytics-handoff-consent.XXXXXX)"
root_ca="$(mktemp /tmp/spyglass-analytics-handoff-ca.XXXXXX)"
handoff_event_id="$(tr -d '\n' </proc/sys/kernel/random/uuid)"
private_event_id="$(tr -d '\n' </proc/sys/kernel/random/uuid)"
erased=false
compose=(docker compose --project-name spyglass-local --env-file "$script_dir/env/local.env" --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml")

cleanup() {
  if [[ "$erased" != true && -s "$cookie_jar" ]]; then
    curl --silent --show-error --cacert "$root_ca" \
      --resolve "app.infiniteocean.localhost:${tls_port}:127.0.0.1" \
      --cookie "$cookie_jar" --cookie-jar "$cookie_jar" \
      --request DELETE --header "Origin: ${app_origin}" \
      "${app_origin}/api/v1/privacy/data" >/dev/null 2>&1 || true
    curl --silent --show-error --cacert "$root_ca" \
      --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
      --cookie "$cookie_jar" --cookie-jar "$cookie_jar" \
      --request DELETE --header "Origin: ${public_origin}" \
      "${public_origin}/api/v1/privacy/data" >/dev/null 2>&1 || true
  fi
  rm -f "$cookie_jar" "$response_headers" "$consent_state" "$root_ca"
}
trap cleanup EXIT

"${compose[@]}" cp edge:/data/caddy/pki/authorities/local/root.crt "$root_ca" >/dev/null
curl_common=(--fail --silent --show-error --cacert "$root_ca")

curl "${curl_common[@]}" \
  --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  --cookie "$cookie_jar" --cookie-jar "$cookie_jar" \
  --request PUT --header "Origin: ${public_origin}" --header 'Content-Type: application/json' \
  --data-binary '{"analytics":true,"marketing":false}' \
  "${public_origin}/api/v1/privacy/consent" >/dev/null

awk '$1 == "#HttpOnly_web.infiniteocean.localhost" && $2 == "FALSE" && $3 == "/" && $4 == "TRUE" && $6 == "__Host-spyglass_privacy" { found=1 } END { exit !found }' "$cookie_jar"
curl "${curl_common[@]}" \
  --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  --cookie "$cookie_jar" --cookie-jar "$cookie_jar" \
  "${public_origin}/api/v1/privacy/consent" >"$consent_state"
jq -e '.surface == "public" and .decided == true and .analytics == true and .marketing == false and .renewal_required == false' "$consent_state" >/dev/null
handoff_occurred_at="$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)"

curl "${curl_common[@]}" \
  --resolve "web.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  --cookie "$cookie_jar" --cookie-jar "$cookie_jar" --dump-header "$response_headers" \
  --request POST --header "Origin: ${public_origin}" --header 'Content-Type: application/json' \
  --data-binary "{\"event_id\":\"${handoff_event_id}\",\"name\":\"signup_handoff_started\",\"occurred_at\":\"${handoff_occurred_at}\",\"fields\":{\"offer_code\":\"team-monthly-v1\"}}" \
  "${public_origin}/api/v1/analytics/events" >/dev/null

grep -Eqi '^set-cookie: __Secure-spyglass_analytics_handoff=' "$response_headers"
grep -Eqi '^set-cookie: __Secure-spyglass_analytics_handoff=.*Domain=infiniteocean\.localhost([;[:space:]]|$)' "$response_headers"
grep -Eqi '^set-cookie: __Secure-spyglass_analytics_handoff=.*Path=/api/v1([;[:space:]]|$)' "$response_headers"
grep -Eqi '^set-cookie: __Secure-spyglass_analytics_handoff=.*HttpOnly([;[:space:]]|$)' "$response_headers"
grep -Eqi '^set-cookie: __Secure-spyglass_analytics_handoff=.*Secure([;[:space:]]|$)' "$response_headers"
grep -Eqi '^set-cookie: __Secure-spyglass_analytics_handoff=.*SameSite=Lax([;[:space:]]|$)' "$response_headers"
handoff_max_age="$(sed -nE 's/^set-cookie: __Secure-spyglass_analytics_handoff=.*Max-Age=([0-9]+).*/\1/ip' "$response_headers")"
test "$handoff_max_age" -gt 0
test "$handoff_max_age" -le 86400
awk '$1 == "#HttpOnly_.infiniteocean.localhost" && $2 == "TRUE" && $3 == "/api/v1" && $4 == "TRUE" && $6 == "__Secure-spyglass_analytics_handoff" { found=1 } END { exit !found }' "$cookie_jar"

curl "${curl_common[@]}" \
  --resolve "app.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  --cookie "$cookie_jar" --cookie-jar "$cookie_jar" \
  --request PUT --header "Origin: ${app_origin}" --header 'Content-Type: application/json' \
  --data-binary '{"analytics":true,"marketing":false}' \
  "${app_origin}/api/v1/privacy/consent" >/dev/null
private_occurred_at="$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)"

curl "${curl_common[@]}" \
  --resolve "app.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  --cookie "$cookie_jar" --cookie-jar "$cookie_jar" \
  --request POST --header "Origin: ${app_origin}" --header 'Content-Type: application/json' \
  --data-binary "{\"event_id\":\"${private_event_id}\",\"name\":\"registration_started\",\"occurred_at\":\"${private_occurred_at}\",\"fields\":{\"offer_code\":\"team-monthly-v1\"}}" \
  "${app_origin}/api/v1/analytics/events" >/dev/null

mirrored_count="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT count(*) FROM analytics_conversion_events c JOIN analytics_events public_event ON public_event.event_id=c.receipt_event_id JOIN analytics_events private_event ON private_event.event_id='${private_event_id}'::uuid WHERE c.receipt_event_id='${handoff_event_id}'::uuid AND c.event_name='registration_started' AND c.fields->>'offer_code'='team-monthly-v1' AND c.source_subject_id=public_event.subject_id AND private_event.subject_id<>public_event.subject_id")"
test "$mirrored_count" = "1"

curl "${curl_common[@]}" \
  --resolve "app.infiniteocean.localhost:${tls_port}:127.0.0.1" \
  --cookie "$cookie_jar" --cookie-jar "$cookie_jar" \
  --request DELETE --header "Origin: ${app_origin}" \
  "${app_origin}/api/v1/privacy/data" >/dev/null
erased=true

remaining_count="$("${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT (SELECT count(*) FROM analytics_events WHERE event_id IN ('${handoff_event_id}'::uuid, '${private_event_id}'::uuid)) + (SELECT count(*) FROM analytics_conversion_events WHERE receipt_event_id='${handoff_event_id}'::uuid)")"
test "$remaining_count" = "0"

if awk '$6 == "__Secure-spyglass_analytics_handoff" { found=1 } END { exit !found }' "$cookie_jar"; then
  printf 'analytics handoff cookie remained after privacy erasure\n' >&2
  exit 1
fi

printf 'local analytics handoff verification passed\n'
