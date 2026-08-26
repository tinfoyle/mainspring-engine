#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local --env-file "$script_dir/env/local.env" --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml")

"${compose[@]}" build identity-browser-test
# Connected identity tests deliberately exercise the production anonymous
# throttles. Reset only the two local anonymous actor scopes so repeated
# certificates do not inherit an earlier developer run's one-hour budget.
"${compose[@]}" exec --no-TTY global-db psql \
  --username=spyglass_migrator \
  --dbname=spyglass \
  --set=ON_ERROR_STOP=1 \
  --command="DELETE FROM network_actor_rate_limits WHERE scope IN ('identity_login', 'identity_recovery')"
"${compose[@]}" run --rm --no-deps identity-browser-test

printf '%s\n' "local identity browser verification passed"
