#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$root_dir"

cohort_volume="${1:?usage: run-prototype-cohort-import.sh COHORT_VOLUME}"
project_name="${COMPOSE_PROJECT_NAME:-spyglass-local}"
container_name="${project_name}-prototype-cohort-import"

read_env() {
  local name="$1" value
  value="$(sed -n "s/^${name}=//p" env/local.env | tail -n 1)"
  if [[ -z "$value" ]]; then
    echo "missing $name in env/local.env" >&2
    exit 1
  fi
  printf '%s' "$value"
}

global_database_url="$(read_env SPYGLASS_PROTOTYPE_MIGRATION_GLOBAL_DATABASE_URL)"
cell_database_url="$(read_env SPYGLASS_CELL_A_PROTOTYPE_MIGRATION_DATABASE_URL)"
object_bucket="$(read_env SPYGLASS_OBJECT_STORE_BUCKET)"
object_access_key="$(read_env SPYGLASS_OBJECT_STORE_MIGRATION_ACCESS_KEY)"
object_secret_key="$(read_env SPYGLASS_OBJECT_STORE_MIGRATION_SECRET_KEY)"
object_secure="$(read_env SPYGLASS_OBJECT_STORE_SECURE)"
object_sse="$(read_env SPYGLASS_OBJECT_STORE_SERVER_SIDE_ENCRYPTION)"

cleanup() {
  docker rm --force "$container_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

docker create \
  --name "$container_name" \
  --network "${project_name}_database" \
  --volume "$cohort_volume:/cohort" \
  --env "SPYGLASS_PROTOTYPE_IMPORT_GLOBAL_DATABASE_URL=$global_database_url" \
  --env "SPYGLASS_PROTOTYPE_IMPORT_CELL_DATABASE_URL=$cell_database_url" \
  --env SPYGLASS_PROTOTYPE_IMPORT_CELL_ID=cell-us-east-01 \
  --env SPYGLASS_PROTOTYPE_IMPORT_OBJECT_ENDPOINT=object-store:9000 \
  --env SPYGLASS_PROTOTYPE_IMPORT_OBJECT_REGION=us-east-1 \
  --env "SPYGLASS_PROTOTYPE_IMPORT_OBJECT_BUCKET=$object_bucket" \
  --env "SPYGLASS_PROTOTYPE_IMPORT_OBJECT_ACCESS_KEY=$object_access_key" \
  --env "SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECRET_KEY=$object_secret_key" \
  --env "SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECURE=$object_secure" \
  --env "SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SSE=$object_sse" \
  --env SPYGLASS_PROTOTYPE_IMPORT_GLOBAL_ERASURE_CHECKPOINT_SEQUENCE=0 \
  --env SPYGLASS_PROTOTYPE_IMPORT_GLOBAL_ERASURE_CHECKPOINT_ROOT=0000000000000000000000000000000000000000000000000000000000000000 \
  --env SPYGLASS_PROTOTYPE_IMPORT_CELL_ERASURE_CHECKPOINT_SEQUENCE=0 \
  --env SPYGLASS_PROTOTYPE_IMPORT_CELL_ERASURE_CHECKPOINT_ROOT=0000000000000000000000000000000000000000000000000000000000000000 \
  --entrypoint /prototype-import \
  spyglass-application:local \
  -bundle /cohort/bundle \
  -certificate /cohort/evidence/reconciliation.json >/dev/null

docker network connect "${project_name}_application" "$container_name"
docker start --attach "$container_name"
