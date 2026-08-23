#!/usr/bin/env bash
set -euo pipefail

prometheus_port="${SPYGLASS_LOCAL_PROMETHEUS_PORT:-9090}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compose=(docker compose --project-name spyglass-local \
  --env-file "$script_dir/env/local.env" \
  --file "$script_dir/compose.yml" \
  --file "$script_dir/compose.local.yml" \
  --profile observability)

"${compose[@]}" up --detach --wait prometheus

base_url="http://127.0.0.1:${prometheus_port}"
for _ in $(seq 1 30); do
  if curl --fail --silent --show-error "$base_url/-/ready" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl --fail --silent --show-error "$base_url/-/ready" >/dev/null

for _ in $(seq 1 30); do
  targets="$(curl --fail --silent --show-error "$base_url/api/v1/targets?state=active")"
  if jq -e '[.data.activeTargets[] | select(.health == "up")] | length == 23' <<<"$targets" >/dev/null; then
    break
  fi
  sleep 1
done

jq -e '
  (.status == "success") and
  (.data.activeTargets | length == 23) and
  ([.data.activeTargets[].health] | all(. == "up")) and
  ([.data.activeTargets[].labels.job] | unique | sort == ["spyglass-cell-workers", "spyglass-global-workers", "spyglass-http"])
' <<<"$targets" >/dev/null

query="$(curl --get --fail --silent --show-error --data-urlencode 'query=count(up == 1)' "$base_url/api/v1/query")"
jq -e '.status == "success" and .data.resultType == "vector" and .data.result[0].value[1] == "23"' <<<"$query" >/dev/null

printf '%s\n' "local observability verification passed"
