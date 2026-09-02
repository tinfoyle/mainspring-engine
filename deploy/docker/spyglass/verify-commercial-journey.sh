#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project=spyglass-commercial-journey
# Keep this disposable stack separate from an operator's normal local stack.
export SPYGLASS_LOCAL_EDGE_PORT=8191
export SPYGLASS_LOCAL_TLS_PORT=8495
export SPYGLASS_LOCAL_SMTP_PORT=1097
export SPYGLASS_LOCAL_MAILPIT_PORT=8126
export SPYGLASS_APPLICATION_IMAGE=spyglass-application:local
compose=(docker compose --project-name "$project" \
  --env-file "$script_dir/env/local.env" \
  --file "$script_dir/compose.yml" \
  --file "$script_dir/compose.local.yml" \
  --file "$script_dir/compose.commercial-journey.yml")

cleanup() {
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
}
cleanup
trap 'status=$?; if (( status != 0 )); then "${compose[@]}" logs --no-color account-api billing-worker stripe-fixture notification-worker >&2 || true; else cleanup; fi; exit "$status"' EXIT

"${compose[@]}" up --detach --build --wait
"${compose[@]}" --profile agent-cert build agent-journey-cert

if [[ $# -eq 2 && $1 == "--out" ]]; then
  report_target="$2"
  report_directory="$(dirname -- "$report_target")"
  mkdir -p -- "$report_directory"
  report_temp="$(mktemp --tmpdir="$report_directory" .commercial-journey.XXXXXX)"
  "${compose[@]}" --profile agent-cert run --rm --no-deps agent-journey-cert >"$report_temp"
  chmod 0600 "$report_temp"
  mv -- "$report_temp" "$report_target"
elif [[ $# -eq 0 ]]; then
  "${compose[@]}" --profile agent-cert run --rm --no-deps agent-journey-cert
else
  printf '%s\n' "usage: $0 [--out HOST_REPORT_PATH]" >&2
  exit 2
fi

printf '%s\n' "local signup-to-subscription commercial journey passed"
