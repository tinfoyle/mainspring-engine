#!/usr/bin/env bash
set -euo pipefail

script_path="$(readlink -f -- "${BASH_SOURCE[0]}")"
stack_dir="$(cd "$(dirname "$script_path")" && pwd)"
global_sql="$stack_dir/reset-stage-signup-global.sql"
cell_sql="$stack_dir/reset-stage-signup-cell.sql"

project_name="${SPYGLASS_STAGE_RESET_PROJECT_NAME:-spyglass-stage}"
global_container="${SPYGLASS_STAGE_RESET_GLOBAL_CONTAINER:-spyglass-stage-global-db-1}"
cell_a_container="${SPYGLASS_STAGE_RESET_CELL_A_CONTAINER:-spyglass-stage-cell-a-db-1}"
cell_b_container="${SPYGLASS_STAGE_RESET_CELL_B_CONTAINER:-spyglass-stage-cell-b-db-1}"
evidence_root="${SPYGLASS_STAGE_RESET_EVIDENCE_ROOT:-/opt/spyglass-stage/evidence/test-resets}"

usage() {
  cat <<'EOF'
Stage-only reset for one unprogressed signup test fixture.

Usage:
  reset-stage-signup.sh inspect EMAIL
  reset-stage-signup.sh execute EMAIL ACCOUNT_ID CONFIRM_EMAIL

Run inspect first. Execution succeeds only when ACCOUNT_ID is the exact Account
shown by inspect and CONFIRM_EMAIL exactly repeats the normalized EMAIL. The
command refuses paid/activated Accounts, additional members, billing, product,
Affiliate, Operations, privacy-rights, MCP, Account movement/closure/erasure,
or any non-empty cell data.

This is not the customer Account-erasure workflow and must never be used for a
real customer. It is physically restricted to the spyglass-stage Compose stack.
EOF
}

die() {
  printf 'reset-stage-signup: %s\n' "$*" >&2
  exit 1
}

if [[ ${1:-} == "--help" || ${1:-} == "-h" ]]; then
  usage
  exit 0
fi

mode="${1:-}"
email_input="${2:-}"
case "$mode" in
  inspect)
    [[ $# -eq 2 ]] || { usage >&2; exit 2; }
    ;;
  execute)
    [[ $# -eq 4 ]] || { usage >&2; exit 2; }
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

email="$(printf '%s' "$email_input" | tr '[:upper:]' '[:lower:]')"
[[ "$email" == "$email_input" ]] || die "EMAIL must already be lowercase and normalized"
[[ "$email" =~ ^[^[:space:]@\|]+@[^[:space:]@\|]+\.[^[:space:]@\|]+$ ]] ||
  die "EMAIL is not a conservative normalized email address"

confirmed_account_id=""
if [[ "$mode" == "execute" ]]; then
  confirmed_account_id="$3"
  confirm_email="$4"
  [[ "$confirmed_account_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]] ||
    die "ACCOUNT_ID must be the exact lowercase v4 UUID shown by inspect"
  [[ "$confirm_email" == "$email" ]] || die "CONFIRM_EMAIL must exactly repeat EMAIL"
fi

[[ -r "$global_sql" && -r "$cell_sql" ]] || die "revision-controlled SQL guards are missing beside this script"
command -v docker >/dev/null 2>&1 || die "docker is required"

require_stage_container() {
  local container="$1" label
  label="$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project" }}' "$container" 2>/dev/null)" ||
    die "required container $container is not running"
  [[ "$label" == "$project_name" && "$project_name" == "spyglass-stage" ]] ||
    die "container $container is not owned by the exact spyglass-stage Compose project"
}

require_stage_container "$global_container"
require_stage_container "$cell_a_container"
require_stage_container "$cell_b_container"

global_psql() {
  docker exec -i "$global_container" sh -lc \
    'exec psql -X -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"' sh "$@"
}

cell_psql() {
  local container="$1"
  shift
  docker exec -i "$container" sh -lc \
    'exec psql -X -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"' sh "$@"
}

target_file="$(mktemp)"
operation_output="$(mktemp)"
chmod 600 "$target_file" "$operation_output"
restore_needed=0
cell_container=""
user_id=""
account_id=""
cell_id=""
placement_generation=""

restore_cell_namespace() {
  [[ "$restore_needed" -eq 1 ]] || return 0
  printf 'Execution did not complete; restoring the empty cell namespace...\n' >&2
  if cell_psql "$cell_container" \
      -v target_account_id="$account_id" \
      -v placement_generation="$placement_generation" <<'SQL'
BEGIN;
CREATE TEMP TABLE stage_signup_reset_restore (
  account_id uuid PRIMARY KEY,
  placement_generation bigint NOT NULL
) ON COMMIT DROP;
INSERT INTO stage_signup_reset_restore VALUES
  (:'target_account_id'::uuid,:'placement_generation'::bigint);
INSERT INTO spyglass.account_namespaces (account_id,placement_generation,state,created_at)
SELECT account_id,placement_generation,'active',statement_timestamp()
FROM stage_signup_reset_restore
ON CONFLICT (account_id) DO NOTHING;
DO $restore_guard$
BEGIN
  IF (SELECT count(*) FROM spyglass.account_namespaces
      WHERE (account_id,placement_generation)=(SELECT account_id,placement_generation FROM stage_signup_reset_restore)
        AND state='active')<>1 THEN
    RAISE EXCEPTION 'restored namespace does not match the reset target';
  END IF;
END
$restore_guard$;
COMMIT;
SQL
  then
    restore_needed=0
  else
    printf 'URGENT: automatic namespace restoration failed for Account %s in %s\n' "$account_id" "$cell_id" >&2
  fi
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM HUP
  restore_cell_namespace || true
  rm -f -- "$target_file" "$operation_output"
  exit "$status"
}
trap cleanup EXIT INT TERM HUP

global_psql -A -t -F $'\t' -v target_email="$email" >"$target_file" <<'SQL'
SELECT u.id,a.id,a.cell_id,a.placement_generation
FROM users u
JOIN accounts a ON a.created_by_user_id=u.id
WHERE u.primary_email=lower(btrim(:'target_email'))
ORDER BY a.created_at,a.id;
SQL

mapfile -t target_rows <"$target_file"
[[ ${#target_rows[@]} -eq 1 ]] || die "EMAIL must resolve to exactly one created Account; found ${#target_rows[@]}"
IFS=$'\t' read -r user_id account_id cell_id placement_generation <<<"${target_rows[0]}"
[[ -n "$user_id" && -n "$account_id" && -n "$cell_id" && -n "$placement_generation" ]] ||
  die "database returned an incomplete signup target"

case "$cell_id" in
  cell-us-east-01) cell_container="$cell_a_container" ;;
  cell-us-west-01) cell_container="$cell_b_container" ;;
  *) die "Account is assigned to unreviewed Stage cell $cell_id" ;;
esac

if [[ "$mode" == "execute" && "$confirmed_account_id" != "$account_id" ]]; then
  die "ACCOUNT_ID does not match the Account currently owned by EMAIL"
fi

printf 'Environment: stage\nEmail: %s\nUser: %s\nAccount: %s\nCell: %s (generation %s)\n' \
  "$email" "$user_id" "$account_id" "$cell_id" "$placement_generation"

global_psql \
  -v target_email="$email" \
  -v target_account_id="$account_id" \
  -v target_cell_id="$cell_id" \
  -v placement_generation="$placement_generation" \
  -v execute_reset=false \
  -f - <"$global_sql"
cell_psql "$cell_container" \
  -v target_account_id="$account_id" \
  -v placement_generation="$placement_generation" \
  -v execute_reset=false \
  -f - <"$cell_sql"

if [[ "$mode" == "inspect" ]]; then
  printf '\nResult: resettable inactive signup fixture. No data was changed.\n'
  printf 'Execute only if this is synthetic test data:\n  %q execute %q %q %q\n' "$0" "$email" "$account_id" "$email"
  exit 0
fi

umask 077
install -d -m 700 "$evidence_root"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
email_digest="$(printf '%s' "$email" | sha256sum | awk '{print $1}')"
evidence_file="$evidence_root/${timestamp}-${account_id}.log"
{
  printf 'stage signup fixture reset\n'
  printf 'timestamp_utc=%s\n' "$timestamp"
  printf 'operator=%s\n' "$(id -un)"
  printf 'email_sha256=%s\n' "$email_digest"
  printf 'user_id=%s\naccount_id=%s\ncell_id=%s\nplacement_generation=%s\n' \
    "$user_id" "$account_id" "$cell_id" "$placement_generation"
} >"$evidence_file"
chmod 600 "$evidence_file"

# Delete the proven-empty namespace first. If the guarded global transaction
# refuses or fails, the EXIT trap recreates that empty namespace.
restore_needed=1
cell_psql "$cell_container" \
  -v target_account_id="$account_id" \
  -v placement_generation="$placement_generation" \
  -v execute_reset=true \
  -f - <"$cell_sql" | tee -a "$evidence_file"

if global_psql \
    -v target_email="$email" \
    -v target_account_id="$account_id" \
    -v target_cell_id="$cell_id" \
    -v placement_generation="$placement_generation" \
    -v execute_reset=true \
    -f - <"$global_sql" >"$operation_output"; then
  # The global commit is authoritative. Never recreate the namespace after it.
  restore_needed=0
else
  tee -a "$evidence_file" <"$operation_output" >&2 || true
  exit 1
fi
tee -a "$evidence_file" <"$operation_output"

printf 'result=completed\n' >>"$evidence_file"
printf '\nResult: synthetic Stage signup fixture reset. Evidence: %s\n' "$evidence_file"
