#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local --env-file "$script_dir/env/local.env" --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml" --profile integration-connectors --profile knowledge-processing)
app_origin=https://app.infiniteocean.localhost:8444
app_host=app.infiniteocean.localhost
root_ca="$(mktemp /tmp/spyglass-imap-ca.XXXXXX)"
response="$(mktemp /tmp/spyglass-imap-response.XXXXXX)"
session_id="$(cat /proc/sys/kernel/random/uuid)"
assessment_id="$(cat /proc/sys/kernel/random/uuid)"
grant_id="$(cat /proc/sys/kernel/random/uuid)"

cleanup() {
  "${compose[@]}" exec --no-TTY global-db psql --username=spyglass_migrator --dbname=spyglass \
    --command="DELETE FROM sessions WHERE id='$session_id'" >/dev/null 2>&1 || true
  rm -f -- "$root_ca" "$response"
}
trap cleanup EXIT

"${compose[@]}" build global-migrate imap-fixture provider-secret-init provider-credential-fixture-a
"${compose[@]}" stop integration-connector-worker-a integration-connector-worker-b >/dev/null 2>&1 || true
"${compose[@]}" up --detach --wait imap-fixture provider-secret-init google-oauth-fixture app-api-a app-api-b app-router edge
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
    VALUES ('$session_id','$user_id',decode('$token_hash','hex'),$security_version,statement_timestamp(),statement_timestamp(),statement_timestamp(),statement_timestamp(),statement_timestamp()+interval '1 hour','local IMAP certification','passkey','passkey')" >/dev/null

cookie="__Host-spyglass_session=$session_token"
curl_app=(curl --silent --show-error --cacert "$root_ca" --resolve "$app_host:8444:127.0.0.1" --header "Origin: $app_origin" --header "Cookie: $cookie")

operation_id="$(cat /proc/sys/kernel/random/uuid)"
status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' --request POST \
  --header 'Content-Type: application/json' --header "Idempotency-Key: $operation_id" \
  --data '{"name":"Local IMAP certification","kind":"email","capabilities":["email.read"],"scope":{"email_address":"operations@infiniteocean.test"}}' \
  "$app_origin/api/v1/accounts/$account_id/integrations/connections")"
test "$status" = 201 -o "$status" = 200 || { sed -n '1,20p' "$response" >&2; exit 1; }
connection_id="$(jq -er '.id' "$response")"
connection_version="$(jq -er '.version' "$response")"

binding_id="$(cat /proc/sys/kernel/random/uuid)"
reference='local://imap-fixture/operations-v1'
reference_sha256="$(printf '%s' "$reference" | sha256sum | cut -d' ' -f1)"
material="$(jq -cn --arg address 'imap-fixture:9993' --arg username 'operations@infiniteocean.test' --arg password 'local-imap-fixture-only' \
  '{address:$address,username:$username,password:$password}')"
"${compose[@]}" run --rm --no-deps \
  --env SPYGLASS_FIXTURE_ACCOUNT_ID="$account_id" \
  --env SPYGLASS_FIXTURE_BINDING_REQUEST_ID="$binding_id" \
  --env SPYGLASS_FIXTURE_CREDENTIAL_GENERATION=1 \
  --env SPYGLASS_FIXTURE_CREDENTIAL_PROVIDER=imap \
  --env SPYGLASS_FIXTURE_CREDENTIAL_REFERENCE="$reference" \
  --env SPYGLASS_FIXTURE_CREDENTIAL_MATERIAL="$material" \
  provider-credential-fixture-a
unset material

status="$("${curl_app[@]}" --output "$response" --write-out '%{http_code}' --request POST \
  --header 'Content-Type: application/json' --header "Idempotency-Key: $binding_id" --header "If-Match: W/\"$connection_version\"" \
  --data "$(jq -cn --arg digest "$reference_sha256" '{provider:"imap",reference_sha256:$digest}')" \
  "$app_origin/api/v1/accounts/$account_id/integrations/connections/$connection_id/credential-bindings")"
test "$status" = 200 || { sed -n '1,20p' "$response" >&2; exit 1; }
credential_id="$(jq -er '.credential_id' "$response")"
test "$(jq -er '.state' "$response")" = active

"${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --set=ON_ERROR_STOP=1 \
  --command="
    BEGIN;
    SELECT set_config('app.account_id','$account_id',true);
    INSERT INTO spyglass.baseline_assessments(account_id,id,catalog_version,scope_policy_version,state,created_by_user_id,version,created_at,updated_at)
    VALUES ('$account_id','$assessment_id','local-imap','local-email-scope-v1','active','$user_id',1,statement_timestamp(),statement_timestamp());
    INSERT INTO spyglass.baseline_source_grants(account_id,id,assessment_id,connection_id,source_kind,folders,state,granted_by_user_id,version,created_at,updated_at)
    VALUES ('$account_id','$grant_id','$assessment_id','$connection_id','email',ARRAY['INBOX'],'active','$user_id',1,statement_timestamp(),statement_timestamp());
    COMMIT;" >/dev/null

SPYGLASS_CONNECTOR_ADAPTER=local-google "${compose[@]}" up --detach --build --force-recreate --wait \
  integration-connector-worker-a knowledge-document-worker-a

capture_count=0
ready_documents=0
healthy_count=0
for _ in $(seq 1 300); do
  IFS='|' read -r capture_count ready_documents healthy_count <<<"$("${compose[@]}" exec --no-TTY cell-a-db \
    psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align --field-separator='|' --command="
      SELECT
        (SELECT count(*) FROM spyglass.integration_source_captures WHERE account_id='$account_id' AND grant_id='$grant_id'),
        (SELECT count(DISTINCT document.id)
         FROM spyglass.integration_source_captures capture
         JOIN spyglass.knowledge_documents document ON document.account_id=capture.account_id AND document.id=capture.document_id AND document.state='ready'
         JOIN spyglass.knowledge_document_revisions revision ON revision.account_id=capture.account_id AND revision.id=capture.document_revision_id AND revision.state='ready'
         WHERE capture.account_id='$account_id' AND capture.grant_id='$grant_id'),
        (SELECT count(*) FROM spyglass.integration_health_observations
         WHERE account_id='$account_id' AND connection_id='$connection_id' AND credential_id='$credential_id' AND state='healthy')")"
  if [[ "$capture_count" == 3 && "$ready_documents" == 3 && "$healthy_count" -ge 1 ]]; then
    break
  fi
  sleep 0.2
done
test "$capture_count" = 3 -a "$ready_documents" = 3 -a "$healthy_count" -ge 1

persisted_secret_count="$("${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT count(*) FROM spyglass.integration_credentials WHERE account_id='$account_id' AND id='$credential_id' AND provider='imap' AND reference_sha256=decode('$reference_sha256','hex')")"
test "$persisted_secret_count" = 1
if "${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --tuples-only --no-align \
  --command="SELECT column_name FROM information_schema.columns WHERE table_schema='spyglass' AND table_name='integration_credentials' AND column_name IN ('password','material')" | grep -q .; then
  echo "cell schema exposed a plaintext credential column" >&2
  exit 1
fi

"${compose[@]}" exec --no-TTY integration-connector-worker-a /spyglass healthcheck --url=http://127.0.0.1:8081/health/ready
"${compose[@]}" exec --no-TTY knowledge-document-worker-a /spyglass healthcheck --url=http://127.0.0.1:8081/health/ready

printf '%s\n' "local implicit-TLS IMAP, MIME-to-Knowledge capture, health, and secret-boundary certification passed"
