#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
generated="$script_dir/workload-tls/generated"
mkdir -p "$generated/runner-identities-a" "$generated/runner-identities-b"
export SPYGLASS_STAGE_SECRETS_DIRECTORY="$generated"

compose=(docker compose --project-name spyglass-agent-journey \
  --env-file "$script_dir/env/local.env" \
  --env-file "$script_dir/env/workload-tls.env" \
  --env-file "$script_dir/env/agent-journey.env" \
  --file "$script_dir/compose.yml" \
  --file "$script_dir/compose.local.yml" \
  --file "$script_dir/compose.workload-tls.yml" \
  --file "$script_dir/compose.stage-runner.yml" \
  --file "$script_dir/compose.agent-journey.yml")

# Keep the one-shot certificate service out of the topology startup. Starting it
# here and then using `run` below would execute the journey twice and consume the
# fixture's deliberate first-call provider failure during the hidden first run.
"${compose[@]}" --profile agent-execution up --detach --build --wait
# Reset the in-memory failure counter so every invocation, including a rerun
# against an existing isolated project, proves the same fail-once/retry path.
"${compose[@]}" --profile agent-execution up --detach --force-recreate --no-deps --wait openai-fixture

if [[ $# -eq 2 && $1 == "--out" ]]; then
  report_target="$2"
  report_directory="$(dirname -- "$report_target")"
  mkdir -p -- "$report_directory"
  report_temp="$(mktemp --tmpdir="$report_directory" .agent-journey.XXXXXX)"
  trap 'rm -f -- "$report_temp"' EXIT
  "${compose[@]}" --profile agent-execution --profile agent-cert run --rm --no-deps agent-journey-cert >"$report_temp"
  chmod 0600 "$report_temp"
  mv -- "$report_temp" "$report_target"
  trap - EXIT
elif [[ $# -eq 0 ]]; then
  "${compose[@]}" --profile agent-execution --profile agent-cert run --rm --no-deps agent-journey-cert
else
  printf '%s\n' "usage: $0 [--out HOST_REPORT_PATH]" >&2
  exit 2
fi

printf '%s\n' "local external-agent journey certification passed"
