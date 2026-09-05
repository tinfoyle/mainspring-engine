#!/usr/bin/env bash
set -euo pipefail
set +x

stack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
release_file="${1:?usage: stage-operations-staff.sh RELEASE_ENV STAGE_ENV show|assign|revoke ...}"
env_file="${2:?usage: stage-operations-staff.sh RELEASE_ENV STAGE_ENV show|assign|revoke ...}"
shift 2
test "$#" -gt 0 || { echo "staff command is required" >&2; exit 2; }
case "$(stat -c %a "$env_file")" in 400|600) ;; *) echo "Stage env must be mode 400 or 600" >&2; exit 1;; esac
test "$(grep -c '^SPYGLASS_GLOBAL_MIGRATION_DATABASE_URL=' "$env_file")" = 1
test "$(grep -c '^SPYGLASS_ENVIRONMENT=stage$' "$env_file")" = 1
export SPYGLASS_GLOBAL_MIGRATION_DATABASE_URL
SPYGLASS_GLOBAL_MIGRATION_DATABASE_URL="$(sed -n 's/^SPYGLASS_GLOBAL_MIGRATION_DATABASE_URL=//p' "$env_file")"
export SPYGLASS_ENVIRONMENT=stage
# Forward the value through the process environment, never a command argument.
docker compose --project-name spyglass-stage --env-file "$release_file" --env-file "$env_file" \
  --file "$stack_dir/compose.yml" --file "$stack_dir/compose.stage.yml" --file "$stack_dir/compose.stage-runner.yml" \
  --profile knowledge-processing run --rm --no-deps -T \
  --env SPYGLASS_GLOBAL_MIGRATION_DATABASE_URL --env SPYGLASS_ENVIRONMENT \
  global-migrate operations-staff "$@"
