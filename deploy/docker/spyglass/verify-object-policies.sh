#!/usr/bin/env bash
set -euo pipefail

compose=(docker compose --project-name "${COMPOSE_PROJECT_NAME:-spyglass-local}" --env-file "${SPYGLASS_ENV_FILE:-env/local.env}" --file compose.yml --file "${SPYGLASS_COMPOSE_OVERRIDE:-compose.local.yml}")

"${compose[@]}" up --detach object-store-init >/dev/null
"${compose[@]}" wait object-store-init >/dev/null

"${compose[@]}" run --rm --no-deps --entrypoint /bin/sh object-store-init -ec '
  mc alias set app http://object-store:9000 "$SPYGLASS_OBJECT_STORE_APP_ACCESS_KEY" "$SPYGLASS_OBJECT_STORE_APP_SECRET_KEY" >/dev/null
  mc alias set worker http://object-store:9000 "$SPYGLASS_OBJECT_STORE_WORKER_ACCESS_KEY" "$SPYGLASS_OBJECT_STORE_WORKER_SECRET_KEY" >/dev/null
  mc alias set connector http://object-store:9000 "$SPYGLASS_OBJECT_STORE_CONNECTOR_ACCESS_KEY" "$SPYGLASS_OBJECT_STORE_CONNECTOR_SECRET_KEY" >/dev/null
  mc alias set migration http://object-store:9000 "$SPYGLASS_OBJECT_STORE_MIGRATION_ACCESS_KEY" "$SPYGLASS_OBJECT_STORE_MIGRATION_SECRET_KEY" >/dev/null
  mc alias set export-source http://object-store:9000 "$SPYGLASS_ACCOUNT_EXPORT_SOURCE_OBJECT_STORE_ACCESS_KEY" "$SPYGLASS_ACCOUNT_EXPORT_SOURCE_OBJECT_STORE_SECRET_KEY" >/dev/null
  mc alias set export-build http://object-store:9000 "$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUILD_ACCESS_KEY" "$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUILD_SECRET_KEY" >/dev/null
  mc alias set export-expiry http://object-store:9000 "$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_EXPIRY_ACCESS_KEY" "$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_EXPIRY_SECRET_KEY" >/dev/null
  mc alias set root http://object-store:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null
  prefix="accounts/policy-certification/documents/document/revisions/revision"
  printf source | mc pipe "app/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/source" >/dev/null
  mc cat "worker/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/source" >/dev/null
  marketing_prefix="accounts/policy-certification/marketing/campaigns/campaign/assets/asset/revisions/revision/content"
  printf marketing | mc pipe "app/$SPYGLASS_OBJECT_STORE_BUCKET/$marketing_prefix" >/dev/null
  mc cat "app/$SPYGLASS_OBJECT_STORE_BUCKET/$marketing_prefix" >/dev/null
  mc cat "connector/$SPYGLASS_OBJECT_STORE_BUCKET/$marketing_prefix" >/dev/null
  mc cat "export-source/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/source" >/dev/null
  mc cat "export-source/$SPYGLASS_OBJECT_STORE_BUCKET/$marketing_prefix" >/dev/null
  printf extracted | mc pipe "worker/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/extracted/text" >/dev/null
  mc cat "migration/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/extracted/text" >/dev/null
  printf migration-source | mc pipe "migration/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/source" >/dev/null
  export_prefix="exports/e9000000-0000-4000-8000-000000000009"
  printf export | mc pipe "export-build/$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUCKET/$export_prefix/artifact.zip" >/dev/null
  mc cat "export-build/$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUCKET/$export_prefix/artifact.zip" >/dev/null
  mc cat "export-expiry/$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUCKET/$export_prefix/artifact.zip" >/dev/null
  if printf denied | mc pipe "app/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/extracted/text" >/dev/null 2>&1; then
    echo "app API object policy permitted an extracted-object write" >&2
    exit 1
  fi
  if printf denied | mc pipe "migration/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/extracted/text" >/dev/null 2>&1; then
    echo "prototype migration object policy permitted an extracted-object write" >&2
    exit 1
  fi
  if printf denied | mc pipe "worker/$SPYGLASS_OBJECT_STORE_BUCKET/$marketing_prefix" >/dev/null 2>&1; then
    echo "document worker object policy permitted a Marketing-object write" >&2
    exit 1
  fi
  if mc cat "worker/$SPYGLASS_OBJECT_STORE_BUCKET/$marketing_prefix" >/dev/null 2>&1; then
    echo "document worker object policy permitted a Marketing-object read" >&2
    exit 1
  fi
  if mc cat "connector/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/source" >/dev/null 2>&1; then
    echo "Integration connector object policy permitted a Knowledge-source read" >&2
    exit 1
  fi
  if printf denied | mc pipe "connector/$SPYGLASS_OBJECT_STORE_BUCKET/$marketing_prefix" >/dev/null 2>&1; then
    echo "Integration connector object policy permitted a Marketing-object write" >&2
    exit 1
  fi
  if mc ls "connector/$SPYGLASS_OBJECT_STORE_BUCKET" >/dev/null 2>&1; then
    echo "Integration connector object policy permitted bucket listing" >&2
    exit 1
  fi
  if mc rm --force "connector/$SPYGLASS_OBJECT_STORE_BUCKET/$marketing_prefix" >/dev/null 2>&1; then
    echo "Integration connector object policy permitted a Marketing-object delete" >&2
    exit 1
  fi
  if mc ls "export-build/$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUCKET" >/dev/null 2>&1; then
    echo "Account export build policy permitted bucket listing" >&2
    exit 1
  fi
  if printf denied | mc pipe "export-build/$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUCKET/arbitrary/object" >/dev/null 2>&1; then
    echo "Account export build policy permitted an arbitrary-key write" >&2
    exit 1
  fi
  if mc cat "export-build/$SPYGLASS_OBJECT_STORE_BUCKET/$marketing_prefix" >/dev/null 2>&1; then
    echo "Account export build policy permitted a document-bucket read" >&2
    exit 1
  fi
  if printf denied | mc pipe "export-source/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/source" >/dev/null 2>&1; then
    echo "Account export source-reader policy permitted a source write" >&2
    exit 1
  fi
  if mc cat "export-source/$SPYGLASS_OBJECT_STORE_BUCKET/$prefix/extracted/text" >/dev/null 2>&1; then
    echo "Account export source-reader policy permitted a derived-object read" >&2
    exit 1
  fi
  if mc ls "export-source/$SPYGLASS_OBJECT_STORE_BUCKET" >/dev/null 2>&1; then
    echo "Account export source-reader policy permitted bucket listing" >&2
    exit 1
  fi
  if printf denied | mc pipe "export-expiry/$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUCKET/exports/e9000000-0000-4000-8000-000000000010/artifact.zip" >/dev/null 2>&1; then
    echo "Account export expiry policy permitted artifact creation" >&2
    exit 1
  fi
  if mc ls "export-expiry/$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUCKET" >/dev/null 2>&1; then
    echo "Account export expiry policy permitted bucket listing" >&2
    exit 1
  fi
  mc rm --force "export-expiry/$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUCKET/$export_prefix/artifact.zip" >/dev/null
  mc rm --recursive --force --versions "root/$SPYGLASS_OBJECT_STORE_BUCKET/accounts/policy-certification" >/dev/null
  mc rm --recursive --force --versions "root/$SPYGLASS_ACCOUNT_EXPORT_OBJECT_STORE_BUCKET/exports/e9000000-0000-4000-8000-000000000009" >/dev/null
'

echo "object credential policies verified"
