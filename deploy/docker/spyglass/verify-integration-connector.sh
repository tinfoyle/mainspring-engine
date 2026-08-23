#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local --env-file "$script_dir/env/local.env" \
  --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml" --profile integration-connectors)

bash "$script_dir/../../verify-process-inventory.sh"
"${compose[@]}" build global-migrate
for service in global-migrate cell-a-migrate cell-b-migrate global-roles cell-a-roles cell-b-roles; do
  "${compose[@]}" run --rm "$service"
done
"${compose[@]}" up --detach --build --force-recreate --wait integration-connector-worker-a integration-connector-worker-b

"${compose[@]}" exec --no-TTY global-db psql --username=spyglass_migrator --dbname=spyglass \
  --file=/dev/stdin <"$script_dir/seed/integration-connector-global.sql"
"${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass \
  --file=/dev/stdin <"$script_dir/seed/integration-connector-cell.sql"

execution_state=""
for _ in $(seq 1 100); do
  execution_state="$("${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass \
    --tuples-only --no-align --command="SELECT state FROM spyglass.integration_executions WHERE account_id='82100000-0000-4000-8000-000000000001' AND id='82a00000-0000-4000-8000-00000000000a'")"
  if [[ "$execution_state" == "succeeded" ]]; then
    break
  fi
  sleep 0.1
done
test "$execution_state" = "succeeded"

attempt_outcome="$("${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass \
  --tuples-only --no-align --command="SELECT mode||':'||outcome FROM spyglass.integration_execution_attempts WHERE account_id='82100000-0000-4000-8000-000000000001' AND execution_id='82a00000-0000-4000-8000-00000000000a'")"
test "$attempt_outcome" = "execute:succeeded"

health_state="$("${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass \
  --tuples-only --no-align --command="SELECT state FROM spyglass.integration_health_observations WHERE account_id='82100000-0000-4000-8000-000000000001' AND connection_id='82700000-0000-4000-8000-000000000007' ORDER BY checked_at DESC,id DESC LIMIT 1")"
test "$health_state" = "healthy"

for service in integration-connector-worker-a integration-connector-worker-b; do
  "${compose[@]}" exec --no-TTY "$service" /spyglass healthcheck --url=http://127.0.0.1:8081/health/ready
done

if "${compose[@]}" exec --no-TTY global-db psql --username=spyglass_migrator --dbname=spyglass --set=ON_ERROR_STOP=1 \
  --command="SET ROLE spyglass_integration_connector_worker; SELECT count(*) FROM users" >/dev/null 2>&1; then
  echo "Integration connector worker unexpectedly read global users" >&2
  exit 1
fi
if "${compose[@]}" exec --no-TTY cell-a-db psql --username=spyglass_migrator --dbname=spyglass --set=ON_ERROR_STOP=1 \
  --command="SET ROLE spyglass_integration_connector_worker; SELECT set_config('app.account_id','82100000-0000-4000-8000-000000000001',false); SELECT count(*) FROM spyglass.integration_credentials" >/dev/null 2>&1; then
  echo "Integration connector worker unexpectedly read credential metadata directly" >&2
  exit 1
fi

printf '%s\n' "local Integration connector execution certification passed"
