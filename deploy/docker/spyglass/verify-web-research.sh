#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local --env-file "$script_dir/env/local.env" --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml" --profile knowledge-processing --profile integration-connectors)
app_origin=https://app.infiniteocean.localhost:8444
app_host=app.infiniteocean.localhost
root_ca="$(mktemp /tmp/spyglass-web-research-ca.XXXXXX)"
response="$(mktemp /tmp/spyglass-web-research-response.XXXXXX)"
session_id="$(cat /proc/sys/kernel/random/uuid)"

cleanup() {
  "${compose[@]}" exec --no-TTY global-db psql --username=spyglass_migrator --dbname=spyglass \
    --command="DELETE FROM sessions WHERE id='$session_id'" >/dev/null 2>&1 || true
  rm -f -- "$root_ca" "$response"
}
trap cleanup EXIT
trap 'printf "web research certification failed at line %s\n" "$LINENO" >&2' ERR

"${compose[@]}" build global-migrate web-research-fixture provider-secret-init provider-credential-fixture-a
"${compose[@]}" up --detach --force-recreate --wait web-research-fixture provider-secret-init google-oauth-fixture \
  app-api-a app-api-b app-router edge knowledge-document-worker-a
"${compose[@]}" cp edge:/data/caddy/pki/authorities/local/root.crt "$root_ca" >/dev/null
"${compose[@]}" exec --no-TTY global-db psql --username=spyglass_migrator --dbname=spyglass \
  --file=/dev/stdin <"$script_dir/seed/integration-connector-global.sql" >/dev/null
"${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass \
  --file=/dev/stdin <"$script_dir/seed/integration-connector-cell.sql" >/dev/null

identity="$("${compose[@]}" exec --no-TTY global-db psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align --field-separator='|' --command="
  SELECT a.id,u.id,u.security_version
  FROM accounts a
  JOIN memberships m ON m.account_id=a.id AND m.role IN ('owner','administrator')
  JOIN users u ON u.id=m.user_id AND u.state='active'
  JOIN entitlement_snapshots e ON e.account_id=a.id AND e.version=a.entitlement_version
  WHERE a.cell_id='cell-us-east-01' AND a.state='active'
    AND e.effective_packages @> '[{\"code\":\"integrations\",\"mode\":\"enabled\"},{\"code\":\"knowledge\",\"mode\":\"enabled\"}]'::jsonb
  ORDER BY a.created_at LIMIT 1")"
IFS='|' read -r account_id user_id security_version <<<"$identity"
test -n "${account_id:-}" -a -n "${user_id:-}" -a -n "${security_version:-}"

session_token="$(openssl rand -hex 32)"
token_hash="$(printf '%s' "$session_token" | sha256sum | cut -d' ' -f1)"
"${compose[@]}" exec --no-TTY global-db psql --username=spyglass_migrator --dbname=spyglass --set=ON_ERROR_STOP=1 --command="
  INSERT INTO sessions(id,user_id,token_hash,security_version,authenticated_at,reauthenticated_at,last_seen_at,rotated_at,expires_at,client_label,authentication_method,reauthentication_method)
  VALUES ('$session_id','$user_id',decode('$token_hash','hex'),$security_version,statement_timestamp(),statement_timestamp(),statement_timestamp(),statement_timestamp(),statement_timestamp()+interval '1 hour','local web research certification','passkey','passkey')" >/dev/null

cookie="__Host-spyglass_session=$session_token"
curl_app=(curl --silent --show-error --cacert "$root_ca" --resolve "$app_host:8444:127.0.0.1" --header "Origin: $app_origin" --header "Cookie: $cookie")

operation_id="$(cat /proc/sys/kernel/random/uuid)"
status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' --request POST \
  --header 'Content-Type: application/json' --header "Idempotency-Key: $operation_id" \
  --data '{"name":"Local public web research","kind":"web_research","capabilities":["web.research"],"scope":{"https_origin":"https://research.infiniteocean.test","path_prefix":"/authoritative"}}' \
  "$app_origin/api/v1/accounts/$account_id/integrations/connections")"
test "$status" = 201 -o "$status" = 200 || { sed -n '1,20p' "$response" >&2; exit 1; }
connection_id="$(jq -er '.id' "$response")"
connection_version="$(jq -er '.version' "$response")"

binding_id="$(cat /proc/sys/kernel/random/uuid)"
reference='local://web-research-fixture/firecrawl-v1'
reference_sha256="$(printf '%s' "$reference" | sha256sum | cut -d' ' -f1)"
"${compose[@]}" run --rm --no-deps \
  --env SPYGLASS_FIXTURE_ACCOUNT_ID="$account_id" \
  --env SPYGLASS_FIXTURE_BINDING_REQUEST_ID="$binding_id" \
  --env SPYGLASS_FIXTURE_CREDENTIAL_GENERATION=1 \
  --env SPYGLASS_FIXTURE_CREDENTIAL_PROVIDER=firecrawl \
  --env SPYGLASS_FIXTURE_CREDENTIAL_REFERENCE="$reference" \
  --env SPYGLASS_FIXTURE_CREDENTIAL_MATERIAL=local-web-research-fixture-only \
  provider-credential-fixture-a

status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' --request POST \
  --header 'Content-Type: application/json' --header "Idempotency-Key: $binding_id" --header "If-Match: W/\"$connection_version\"" \
  --data "$(jq -cn --arg digest "$reference_sha256" '{provider:"firecrawl",reference_sha256:$digest}')" \
  "$app_origin/api/v1/accounts/$account_id/integrations/connections/$connection_id/credential-bindings")"
test "$status" = 200 || { sed -n '1,20p' "$response" >&2; exit 1; }
credential_id="$(jq -er '.credential_id' "$response")"

SPYGLASS_CONNECTOR_ADAPTER=local-google "${compose[@]}" up --detach --build --force-recreate --wait integration-connector-worker-a

healthy_observations=0
for _ in $(seq 1 300); do
  healthy_observations="$("${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align --command="
    SELECT count(*) FROM spyglass.integration_health_observations
    WHERE account_id='$account_id' AND connection_id='$connection_id' AND credential_id='$credential_id' AND state='healthy'")"
  if [[ "$healthy_observations" -ge 1 ]]; then
    break
  fi
  sleep 0.2
done
test "$healthy_observations" -ge 1
baseline_stats="$("${compose[@]}" exec --no-TTY web-research-fixture /web-research-fixture stats)"
baseline_searches="$(jq -er '.searches' <<<"$baseline_stats")"
baseline_reads="$(jq -er '.reads' <<<"$baseline_stats")"

status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' --request POST \
  --header 'Content-Type: application/json' \
  --data "$(jq -cn --arg connection "$connection_id" '{connection_id:$connection,query:"authoritative scoped research",limit:3}')" \
  "$app_origin/api/v1/accounts/$account_id/integrations/web-research/search")"
test "$status" = 200 || { sed -n '1,20p' "$response" >&2; exit 1; }
test "$(jq -er '.items|length' "$response")" = 2
jq -e 'all(.items[]; (.url|startswith("https://research.infiniteocean.test/authoritative/")) and (.citation_id|startswith("web:")))' "$response" >/dev/null

read_id="$(cat /proc/sys/kernel/random/uuid)"
read_body="$(jq -cn --arg connection "$connection_id" '{connection_id:$connection,url:"https://research.infiniteocean.test/authoritative/article"}')"
status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' --request POST \
  --header 'Content-Type: application/json' --header "Idempotency-Key: $read_id" --data "$read_body" \
  "$app_origin/api/v1/accounts/$account_id/integrations/web-research/read")"
test "$status" = 201 || { sed -n '1,20p' "$response" >&2; exit 1; }
first_capture="$(jq -er '.capture_id' "$response")"
document_id="$(jq -er '.document_id' "$response")"
first_revision="$(jq -er '.document_revision_id' "$response")"
grep -q 'Deterministic content version one' "$response"
! grep -q 'secretNoise' "$response"
first_stats="$("${compose[@]}" exec --no-TTY web-research-fixture /web-research-fixture stats)"
test "$(jq -er '.searches' <<<"$first_stats")" = "$((baseline_searches + 1))"
test "$(jq -er '.reads' <<<"$first_stats")" = "$((baseline_reads + 1))"

status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' --request POST \
  --header 'Content-Type: application/json' --header "Idempotency-Key: $read_id" --data "$read_body" \
  "$app_origin/api/v1/accounts/$account_id/integrations/web-research/read")"
test "$status" = 201
test "$(jq -er '.capture_id' "$response")" = "$first_capture"
test "$(jq -er '.document_revision_id' "$response")" = "$first_revision"
stats="$("${compose[@]}" exec --no-TTY web-research-fixture /web-research-fixture stats)"
test "$(jq -er '.searches' <<<"$stats")" = "$(jq -er '.searches' <<<"$first_stats")"
test "$(jq -er '.reads' <<<"$stats")" = "$(jq -er '.reads' <<<"$first_stats")"

first_ready=0
for _ in $(seq 1 300); do
  first_ready="$("${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align --command="
    SELECT count(*) FROM spyglass.knowledge_document_revisions WHERE account_id='$account_id' AND id='$first_revision' AND state='ready'")"
  if [[ "$first_ready" == 1 ]]; then
    break
  fi
  sleep 0.2
done
test "$first_ready" = 1

"${compose[@]}" exec --no-TTY web-research-fixture /web-research-fixture control-change
changed_id="$(cat /proc/sys/kernel/random/uuid)"
status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' --request POST \
  --header 'Content-Type: application/json' --header "Idempotency-Key: $changed_id" --data "$read_body" \
  "$app_origin/api/v1/accounts/$account_id/integrations/web-research/read")"
test "$status" = 201 || { sed -n '1,20p' "$response" >&2; exit 1; }
second_revision="$(jq -er '.document_revision_id' "$response")"
test "$second_revision" != "$first_revision"
test "$(jq -er '.document_id' "$response")" = "$document_id"
grep -q 'Deterministic content version two' "$response"

captures=0
ready_revisions=0
for _ in $(seq 1 300); do
  IFS='|' read -r captures ready_revisions <<<"$("${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align --field-separator='|' --command="
    SELECT
      (SELECT count(*) FROM spyglass.integration_web_research_captures WHERE account_id='$account_id' AND connection_id='$connection_id'),
      (SELECT count(*) FROM spyglass.knowledge_document_revisions WHERE account_id='$account_id' AND document_id='$document_id' AND state='ready')")"
  if [[ "$captures" == 2 && "$ready_revisions" == 2 ]]; then
    break
  fi
  sleep 0.2
done
test "$captures" = 2
test "$ready_revisions" = 2

persisted_secret_count="$("${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align --command="
  SELECT count(*) FROM spyglass.integration_credentials WHERE account_id='$account_id' AND id='$credential_id' AND provider='firecrawl' AND reference_sha256=decode('$reference_sha256','hex')")"
test "$persisted_secret_count" = 1
if "${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align --command="
  SELECT canonical_url,title,excerpt FROM spyglass.integration_web_research_captures WHERE account_id='$account_id'" | grep -q 'local-web-research-fixture-only'; then
  echo "web research capture persisted provider credential material" >&2
  exit 1
fi

printf '%s\n' "local HTTPS public-web search, SSRF fence, replay, immutable Knowledge revision, and secret-boundary certification passed"
