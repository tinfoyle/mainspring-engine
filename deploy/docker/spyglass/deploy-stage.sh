#!/usr/bin/env bash
set -euo pipefail

stack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
release_file="${1:?usage: deploy-stage.sh /absolute/path/to/release.env /absolute/path/to/stage.env}"
env_file="${2:?usage: deploy-stage.sh /absolute/path/to/release.env /absolute/path/to/stage.env}"
"$stack_dir/verify-stage.sh" "$release_file" "$env_file"
compose=(docker compose --project-name spyglass-stage --env-file "$release_file" --env-file "$env_file" --file "$stack_dir/compose.yml" --file "$stack_dir/compose.stage.yml")
"${compose[@]}" pull
"${compose[@]}" up --detach --wait --remove-orphans
"${compose[@]}" ps
