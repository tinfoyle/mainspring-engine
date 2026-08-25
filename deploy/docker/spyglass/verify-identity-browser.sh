#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local --env-file "$script_dir/env/local.env" --file "$script_dir/compose.yml" --file "$script_dir/compose.local.yml")

"${compose[@]}" build identity-browser-test
"${compose[@]}" run --rm --no-deps identity-browser-test

printf '%s\n' "local identity browser verification passed"
