#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local --env-file "$script_dir/env/local.env" --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml" --profile integration-connectors --profile knowledge-processing)
app_origin=https://app.infiniteocean.localhost:8444
oauth_host=oauth.infiniteocean.localhost
app_host=app.infiniteocean.localhost
root_ca="$(mktemp /tmp/spyglass-google-oauth-ca.XXXXXX)"
headers="$(mktemp /tmp/spyglass-google-oauth-headers.XXXXXX)"
response="$(mktemp /tmp/spyglass-google-oauth-response.XXXXXX)"
session_id="$(cat /proc/sys/kernel/random/uuid)"
assessment_id="$(cat /proc/sys/kernel/random/uuid)"
grant_id="$(cat /proc/sys/kernel/random/uuid)"

cleanup() {
  "${compose[@]}" exec --no-TTY global-db psql --username=spyglass_migrator --dbname=spyglass \
    --command="DELETE FROM sessions WHERE id='$session_id'" >/dev/null 2>&1 || true
  rm -f -- "$root_ca" "$headers" "$response"
}
trap cleanup EXIT

"${compose[@]}" build global-migrate google-oauth-fixture provider-secret-init
"${compose[@]}" stop integration-connector-worker-a integration-connector-worker-b >/dev/null 2>&1 || true
"${compose[@]}" up --detach --wait google-oauth-fixture provider-secret-init app-api-a app-api-b app-router edge
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
    AND e.effective_packages @> '[{\"code\":\"integrations\",\"mode\":\"enabled\"}]'::jsonb
  ORDER BY a.created_at LIMIT 1")"
IFS='|' read -r account_id user_id security_version <<<"$identity"
test -n "${account_id:-}" -a -n "${user_id:-}" -a -n "${security_version:-}"

session_token="$(openssl rand -hex 32)"
token_hash="$(printf '%s' "$session_token" | sha256sum | cut -d' ' -f1)"
"${compose[@]}" exec --no-TTY global-db psql --username=spyglass_migrator --dbname=spyglass --set=ON_ERROR_STOP=1 \
  --command="
    INSERT INTO sessions(id,user_id,token_hash,security_version,authenticated_at,reauthenticated_at,last_seen_at,rotated_at,expires_at,client_label,authentication_method,reauthentication_method)
    VALUES ('$session_id','$user_id',decode('$token_hash','hex'),$security_version,statement_timestamp(),statement_timestamp(),statement_timestamp(),statement_timestamp(),statement_timestamp()+interval '1 hour','local Google OAuth certification','passkey','passkey')" >/dev/null

cookie="__Host-spyglass_session=$session_token"
curl_app=(curl --silent --show-error --cacert "$root_ca" --resolve "$app_host:8444:127.0.0.1" --header "Origin: $app_origin" --header "Cookie: $cookie")

operation_id="$(cat /proc/sys/kernel/random/uuid)"
status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' --request POST \
  --header 'Content-Type: application/json' --header "Idempotency-Key: $operation_id" \
  --data '{"name":"Local Google Drive certification","kind":"google_drive","capabilities":["google_drive.read"],"scope":{"drive_folder_ids":["folder-a"]}}' \
  "$app_origin/api/v1/accounts/$account_id/integrations/connections")"
test "$status" = 201 -o "$status" = 200 || { sed -n '1,20p' "$response" >&2; exit 1; }
connection_id="$(jq -er '.id' "$response")"

operation_id="$(cat /proc/sys/kernel/random/uuid)"
redirect_uri="$app_origin/api/v1/accounts/$account_id/integrations/google/authorization-callback"
status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' --request POST \
  --header 'Content-Type: application/json' --header "Idempotency-Key: $operation_id" \
  --data "$(jq -cn --arg redirect_uri "$redirect_uri" '{redirect_uri:$redirect_uri}')" \
  "$app_origin/api/v1/accounts/$account_id/integrations/connections/$connection_id/authorizations")"
test "$status" = 201 || { sed -n '1,20p' "$response" >&2; exit 1; }
authorization_id="$(jq -er '.authorization.id' "$response")"
authorization_url="$(jq -er '.authorization_url' "$response")"
if jq -e 'paths | map(tostring) | join("_") | test("state|pkce|scope|reference|token|code")' "$response" >/dev/null; then
  echo "OAuth begin response exposed secret-bearing protocol fields" >&2
  exit 1
fi

curl --silent --show-error --cacert "$root_ca" --resolve "$oauth_host:8444:127.0.0.1" \
  --dump-header "$headers" --output /dev/null "$authorization_url"
callback_url="$(sed -n 's/^[Ll]ocation: //p' "$headers" | tr -d '\r')"
test -n "$callback_url"
status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' "$callback_url")"
test "$status" = 200 || { sed -n '1,20p' "$response" >&2; exit 1; }
test "$(jq -er '.status' "$response")" = completed

status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' \
  "$app_origin/api/v1/accounts/$account_id/integrations/authorizations/$authorization_id")"
test "$status" = 200
test "$(jq -er '.status' "$response")" = completed
if jq -e 'paths | map(tostring) | join("_") | test("state|pkce|scope|redirect|reference|token|code")' "$response" >/dev/null; then
  echo "OAuth status response exposed secret-bearing protocol fields" >&2
  exit 1
fi

"${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --set=ON_ERROR_STOP=1 \
  --command="
    BEGIN;
    SELECT set_config('app.account_id','$account_id',true);
    INSERT INTO spyglass.baseline_assessments(account_id,id,catalog_version,scope_policy_version,state,created_by_user_id,version,created_at,updated_at)
    VALUES ('$account_id','$assessment_id','local-google-oauth','local-drive-scope-v1','active','$user_id',1,statement_timestamp(),statement_timestamp());
    INSERT INTO spyglass.baseline_source_grants(account_id,id,assessment_id,connection_id,source_kind,folders,state,granted_by_user_id,version,created_at,updated_at)
    VALUES ('$account_id','$grant_id','$assessment_id','$connection_id','google_drive',ARRAY['folder-a'],'active','$user_id',1,statement_timestamp(),statement_timestamp());
    COMMIT;" >/dev/null

SPYGLASS_CONNECTOR_ADAPTER=local-google "${compose[@]}" up --detach --build --force-recreate --wait \
  integration-connector-worker-a knowledge-document-worker-a

capture_count=0
document_state=""
revision_state=""
for _ in $(seq 1 300); do
  IFS='|' read -r capture_count document_state revision_state <<<"$("${compose[@]}" exec --no-TTY cell-a-db \
    psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align --field-separator='|' --command="
      SELECT count(*),coalesce(max(document.state),''),coalesce(max(revision.state),'')
      FROM spyglass.integration_source_captures capture
      JOIN spyglass.knowledge_documents document ON document.account_id=capture.account_id AND document.id=capture.document_id
      JOIN spyglass.knowledge_document_revisions revision ON revision.account_id=capture.account_id AND revision.id=capture.document_revision_id
      WHERE capture.account_id='$account_id' AND capture.grant_id='$grant_id'")"
  if [[ "$capture_count" == 1 && "$document_state" == ready && "$revision_state" == ready ]]; then
    break
  fi
  sleep 0.2
done
test "$capture_count" = 1 -a "$document_state" = ready -a "$revision_state" = ready

"${compose[@]}" exec --no-TTY integration-connector-worker-a /spyglass healthcheck --url=http://127.0.0.1:8081/health/ready
"${compose[@]}" exec --no-TTY knowledge-document-worker-a /spyglass healthcheck --url=http://127.0.0.1:8081/health/ready

operation_id="$(cat /proc/sys/kernel/random/uuid)"
status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' --request POST \
  --header "Idempotency-Key: $operation_id" \
  "$app_origin/api/v1/accounts/$account_id/integrations/connections/$connection_id/credential-revocations")"
test "$status" = 200 || { sed -n '1,20p' "$response" >&2; exit 1; }
test "$(jq -er '.state' "$response")" = completed

database_state="$("${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT state FROM spyglass.integration_connections WHERE account_id='$account_id' AND id='$connection_id'")"
test "$database_state" = revoked

post_revoke="$("${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align --field-separator='|' \
  --command="SELECT (SELECT count(*) FROM spyglass.integration_source_sync_queue WHERE account_id='$account_id' AND grant_id='$grant_id'),
    (SELECT count(*) FROM spyglass.integration_source_captures WHERE account_id='$account_id' AND grant_id='$grant_id')")"
test "$post_revoke" = "0|1"

printf '%s\n' "local Google OAuth, Drive-to-Knowledge capture, and revocation certification passed"
