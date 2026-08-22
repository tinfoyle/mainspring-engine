#!/usr/bin/env bash
set -euo pipefail

compose=(docker compose --project-name spyglass-local --env-file env/local.env --file compose.yml --file compose.local.yml --profile test)
object_store_was_running="$("${compose[@]}" ps --quiet object-store 2>/dev/null || true)"
extractor_was_running="$("${compose[@]}" ps --quiet document-extractor 2>/dev/null || true)"

bash ../../verify-process-inventory.sh

cleanup() {
  "${compose[@]}" rm --stop --force test-db >/dev/null 2>&1 || true
  if [[ -z "${object_store_was_running}" ]]; then
    "${compose[@]}" rm --stop --force object-store-init object-store >/dev/null 2>&1 || true
  fi
  if [[ -z "${extractor_was_running}" ]]; then
    "${compose[@]}" rm --stop --force document-extractor >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

"${compose[@]}" build website-test go-test
"${compose[@]}" run --rm go-test
