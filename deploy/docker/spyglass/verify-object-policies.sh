#!/usr/bin/env bash
set -euo pipefail

compose=(docker compose --project-name "${COMPOSE_PROJECT_NAME:-spyglass-local}" --env-file "${SPYGLASS_ENV_FILE:-env/local.env}" --file compose.yml --file "${SPYGLASS_COMPOSE_OVERRIDE:-compose.local.yml}")

"${compose[@]}" up --detach object-store-init >/dev/null
"${compose[@]}" wait object-store-init >/dev/null

"${compose[@]}" run --rm --no-deps --entrypoint /bin/sh object-store-init -ec '
  mc alias set app http://object-store:9000 "$SPYGLASS_OBJECT_STORE_APP_ACCESS_KEY" "$SPYGLASS_OBJECT_STORE_APP_SECRET_KEY" >/dev/null
  mc alias set worker http://object-store:9000 "$SPYGLASS_OBJECT_STORE_WORKER_ACCESS_KEY" "$SPYGLASS_OBJECT_STORE_WORKER_SECRET_KEY" >/dev/null
  mc alias set root http://object-store:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null
  prefix="accounts/policy-certification/documents/document/revisions/revision"
  printf source | mc pipe "app/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/source" >/dev/null
  mc cat "worker/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/source" >/dev/null
  printf extracted | mc pipe "worker/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/extracted/text" >/dev/null
  if printf denied | mc pipe "app/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/extracted/text" >/dev/null 2>&1; then
    echo "app API object policy permitted an extracted-object write" >&2
    exit 1
  fi
  mc rm --recursive --force --versions "root/$SPYGLASS_OBJECT_STORE_BUCKET/accounts/policy-certification" >/dev/null
'

echo "object credential policies verified"
