#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd -- "$script_dir/.." && pwd)"
inventory="$script_dir/spyglass-process-inventory.json"
source_file="$repo_dir/cmd/spyglass/main.go"

jq -e '
  .schema_version == 1 and
  .binary == "spyglass" and
  (.processes | length > 0) and
  ([.processes[].mode] | length == (unique | length)) and
  all(.processes[];
    (.mode | type == "string" and length > 0) and
    (.kind | type == "string" and length > 0) and
    (.scope | type == "string" and length > 0) and
    (.database_roles | type == "array") and
    (.configuration | type == "array") and
    (.configuration_profiles | type == "array") and
    (.dependencies | type == "array"))
' "$inventory" >/dev/null

source_modes="$(sed -n '/switch mode {/,/default:/p' "$source_file" | sed -n 's/^[[:space:]]*case "\([^"]*\)".*/\1/p' | sort)"
inventory_modes="$(jq -r '.processes[].mode' "$inventory" | sort)"
test "$source_modes" = "$inventory_modes"

printf '%s\n' "Spyglass process inventory matches the executable modes"
