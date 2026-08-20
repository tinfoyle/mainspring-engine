#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
identity_directory="$script_dir/workload-tls/runner-identities"
mkdir -p "$identity_directory"
export SPYGLASS_DOCKER_RUNNER_IDENTITY_DIRECTORY="$identity_directory"

compose=(docker compose --project-name spyglass-local-runner \
  --env-file "$script_dir/env/local.env" \
  --env-file "$script_dir/env/workload-tls.env" \
  --file "$script_dir/compose.yml" \
  --file "$script_dir/compose.local.yml" \
  --file "$script_dir/compose.workload-tls.yml" \
  --file "$script_dir/compose.runner.yml")

"${compose[@]}" build runner-fixture docker-runner-launcher docker-launcher-test
"${compose[@]}" up --force-recreate workload-tls-init
"${compose[@]}" up --detach --wait --force-recreate --no-deps docker-runner-launcher

SPYGLASS_DOCKER_LAUNCHER_INTEGRATION_PHASE=ensure "${compose[@]}" run --rm --no-deps docker-launcher-test
"${compose[@]}" restart docker-runner-launcher
"${compose[@]}" up --detach --wait --no-deps docker-runner-launcher
SPYGLASS_DOCKER_LAUNCHER_INTEGRATION_PHASE=reconcile "${compose[@]}" run --rm --no-deps docker-launcher-test

if docker ps --all --filter label=spyglass.io/docker-runner-managed=true --filter label=spyglass.io/invocation-id=79000000-0000-4000-8000-000000000001 --quiet | grep -q .; then
  printf '%s\n' "Docker runner fixture remained after cancellation" >&2
  exit 1
fi

printf '%s\n' "stage-only Docker runner launcher verification passed"
