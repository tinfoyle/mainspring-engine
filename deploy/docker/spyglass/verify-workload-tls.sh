#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local-tls \
  --env-file "$script_dir/env/local.env" \
  --env-file "$script_dir/env/workload-tls.env" \
  --file "$script_dir/compose.yml" \
  --file "$script_dir/compose.local.yml" \
  --file "$script_dir/compose.workload-tls.yml")

"${compose[@]}" up --detach --build --wait --force-recreate

for canary in route-canary-cell-a route-canary-cell-b route-canary-admission-a; do
  "${compose[@]}" run --rm --no-deps "$canary"
done

for service in app-api-a app-api-b admission-api agent-dispatch-worker-a agent-dispatch-worker-b; do
  test "$("${compose[@]}" ps --format json "$service" | jq -r '.Health')" = "healthy"
done

for service in app-api-a app-api-b admission-api; do
  logs="$("${compose[@]}" logs --no-color "$service")"
  grep -q 'Spyglass HTTPS listening' <<<"$logs"
done

printf '%s\n' "secure-local workload TLS verification passed"
