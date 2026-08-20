#!/usr/bin/env bash
set -euo pipefail

compose=(docker compose --project-name spyglass-local --env-file env/local.env --file compose.yml --file compose.local.yml --profile test)

cleanup() {
  "${compose[@]}" rm --stop --force test-db >/dev/null 2>&1 || true
}
trap cleanup EXIT

"${compose[@]}" build website-test go-test
"${compose[@]}" run --rm go-test
