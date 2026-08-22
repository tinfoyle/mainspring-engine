#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$root_dir"

account_id="${1:?usage: prepare-prototype-cohort.sh ACCOUNT_ID MEMBERSHIP_ID OWNER_USER_ID}"
membership_id="${2:?usage: prepare-prototype-cohort.sh ACCOUNT_ID MEMBERSHIP_ID OWNER_USER_ID}"
owner_user_id="${3:?usage: prepare-prototype-cohort.sh ACCOUNT_ID MEMBERSHIP_ID OWNER_USER_ID}"
account_slug="prototype-migration-${account_id:0:8}"
project_name="${COMPOSE_PROJECT_NAME:-spyglass-local}"
global_db="${project_name}-global-db-1"
cell_db="${project_name}-cell-a-db-1"
if ! docker inspect "$global_db" "$cell_db" >/dev/null 2>&1; then
  echo "the local Spyglass global and cell-a databases must be running" >&2
  exit 1
fi

docker exec --interactive "$global_db" psql \
  --set=ON_ERROR_STOP=1 \
  --set=account_id="$account_id" \
  --set=account_slug="$account_slug" \
  --set=membership_id="$membership_id" \
  --set=owner_user_id="$owner_user_id" \
  --username=spyglass_migrator --dbname=spyglass <<'SQL'
BEGIN;

WITH publication AS (
  SELECT version FROM catalog_publications WHERE state='published' ORDER BY version DESC LIMIT 1
), package AS (
  SELECT '[{"code":"knowledge","mode":"enabled","limits":{"documents":25},"sources":["support_override"],"version":1,"limit_policies":{"documents":{"kind":"capacity","combine":"replace"}}}]'::jsonb AS value
)
INSERT INTO accounts(
  id,slug,display_name,account_type,state,cell_id,placement_generation,
  entitlement_version,last_catalog_reconciled_version,version,created_by_user_id,created_at
)
SELECT :'account_id'::uuid,:'account_slug','Prototype Migration Cohort',
  'free','active','cell-us-east-01',1,1,publication.version,1,:'owner_user_id'::uuid,statement_timestamp()
FROM publication;

INSERT INTO memberships(id,account_id,user_id,role,state,version,created_at)
VALUES (:'membership_id'::uuid,:'account_id'::uuid,:'owner_user_id'::uuid,'owner','active',1,statement_timestamp());

WITH publication AS (
  SELECT version FROM catalog_publications WHERE state='published' ORDER BY version DESC LIMIT 1
), package AS (
  SELECT '[{"code":"knowledge","mode":"enabled","limits":{"documents":25},"sources":["support_override"],"version":1,"limit_policies":{"documents":{"kind":"capacity","combine":"replace"}}}]'::jsonb AS value
)
INSERT INTO entitlement_snapshots(account_id,version,catalog_version,evaluated_at,source_hash,effective_packages)
SELECT :'account_id'::uuid,1,publication.version,statement_timestamp(),
  sha256(convert_to(package.value::text,'UTF8')),package.value
FROM publication CROSS JOIN package;

INSERT INTO account_directory(account_id,cell_id,placement_generation,state,data_region,updated_at)
VALUES (:'account_id'::uuid,'cell-us-east-01',1,'active','us-east',statement_timestamp());

UPDATE cells SET assigned_accounts=assigned_accounts+1 WHERE id='cell-us-east-01';

COMMIT;
SQL

docker exec --interactive "$cell_db" psql \
  --set=ON_ERROR_STOP=1 \
  --set=account_id="$account_id" \
  --username=spyglass_migrator --dbname=spyglass <<'SQL'
INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
VALUES (:'account_id'::uuid,1,'active',statement_timestamp());
SQL

printf 'prepared isolated prototype cohort Account %s in cell-us-east-01\n' "$account_id"
