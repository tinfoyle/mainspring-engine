#!/usr/bin/env bash

set -euo pipefail

base_url="${MAINSPRING_SMOKE_URL:-http://127.0.0.1:8088}"
tenant_host="${MAINSPRING_SMOKE_HOST:-demo.localhost}"
email="${MAINSPRING_SMOKE_EMAIL:-owner@example.test}"
password="${MAINSPRING_SMOKE_PASSWORD:-correct-horse-battery-staple}"

work_dir="$(mktemp -d)"
trap 'rm -rf -- "$work_dir"' EXIT
cookies="$work_dir/cookies"
page="$work_dir/page.html"
headers="$work_dir/headers"
request() { curl --silent --show-error --fail-with-body -H "Host: $tenant_host" "$@"; }

request --output /dev/null --cookie-jar "$cookies" \
  --data-urlencode "email=$email" --data-urlencode "password=$password" "$base_url/login"
request --cookie "$cookies" --output "$page" "$base_url/agents/new"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
boardroom_id="$(grep -oE 'option value="[0-9a-f-]+"' "$page" | head -n 1 | cut -d'"' -f2)"

create_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" --data-urlencode "boardroom_id=$boardroom_id" \
  --data-urlencode 'name=Customization Smoke Agent' --data-urlencode 'role=Workflow Auditor' \
  --data-urlencode 'description=Verifies that owner-defined agent controls reach a durable invocation.' \
  --data-urlencode 'system_instructions=Review the owner request carefully, stay within granted tools, and provide one concise operational finding.' \
  --data-urlencode 'position=1' --data-urlencode 'enabled=true' --data-urlencode 'provider=mock' \
  --data-urlencode 'model=custom-smoke-model' --data-urlencode 'reasoning_effort=high' \
  --data-urlencode 'temperature=0.3' --data-urlencode 'top_p=0.8' \
  --data-urlencode 'context_token_limit=3456' --data-urlencode 'max_output_tokens=789' \
  --data-urlencode 'timeout_seconds=45' --data-urlencode 'max_tool_calls=2' --data-urlencode 'max_cost_micros=7' \
  --data-urlencode 'response_style=concise' --data-urlencode 'citation_policy=required_for_research' \
  --data-urlencode 'action_policy=disabled' --data-urlencode 'knowledge_mode=all' \
  --data-urlencode 'knowledge_max_results=2' "$base_url/agents")"
[[ "$create_status" == "303" ]]
agent_path="$(awk 'tolower($1) == "location:" {gsub("\r", "", $2); print $2}' "$headers" | tail -n 1 | cut -d'?' -f1)"
[[ "$agent_path" == /agents/* ]]

request --cookie "$cookies" --output "$page" "$base_url/agents"
grep -q 'Customization Smoke Agent' "$page"
request --cookie "$cookies" --output "$page" "$base_url$agent_path"
grep -q 'custom-smoke-model' "$page"
grep -q 'value="3456"' "$page"
grep -q 'value="789"' "$page"
grep -q 'max_results' "$page"
grep -q 'name="tool_web.search" value="true"' "$page"
grep -q 'name="tool_web.read" value="true"' "$page"

request --cookie "$cookies" --output "$page" "$base_url/"
room_path="$(grep -oE '/boardrooms/[0-9a-f-]+' "$page" | head -n 1)"
curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --dump-header "$headers" --output "$page" \
  --data-urlencode "csrf_token=$csrf_token" --data-urlencode 'prompt=Run the agent customization smoke check.' \
  "$base_url${room_path}/runs"
conversation_path="$(awk 'BEGIN { IGNORECASE=1 } /^location:/ { gsub("\r", "", $2); print $2 }' "$headers" | tail -n 1)"
for _ in $(seq 1 120); do
  request --cookie "$cookies" --output "$page" "$base_url$conversation_path"
  grep -q 'run-status-completed' "$page" && break
  sleep 0.5
done
grep -q 'run-status-completed' "$page"
grep -q 'Workflow Auditor' "$page"

request --cookie "$cookies" --output "$page" "$base_url$agent_path"
grep -q 'Version 1' "$page"

update_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" --data-urlencode "boardroom_id=$boardroom_id" \
  --data-urlencode 'name=Customization Smoke Agent (inactive)' --data-urlencode 'role=Workflow Auditor' \
  --data-urlencode 'description=Verified customization and is now inactive.' \
  --data-urlencode 'system_instructions=Review the owner request carefully, stay within granted tools, and provide one concise operational finding.' \
  --data-urlencode 'position=1' --data-urlencode 'provider=mock' --data-urlencode 'model=custom-smoke-model' \
  --data-urlencode 'reasoning_effort=high' --data-urlencode 'temperature=0.3' --data-urlencode 'top_p=0.8' \
  --data-urlencode 'context_token_limit=3456' --data-urlencode 'max_output_tokens=789' --data-urlencode 'timeout_seconds=45' \
  --data-urlencode 'max_tool_calls=2' --data-urlencode 'max_cost_micros=7' --data-urlencode 'response_style=concise' \
  --data-urlencode 'citation_policy=required_for_research' --data-urlencode 'action_policy=disabled' \
  --data-urlencode 'knowledge_mode=all' --data-urlencode 'knowledge_max_results=2' \
  "$base_url$agent_path")"
[[ "$update_status" == "303" ]]
request --cookie "$cookies" --output "$page" "$base_url$agent_path"
grep -q 'name="tool_web.search" value="true"' "$page"
grep -q 'name="tool_web.read" value="true"' "$page"

duplicate_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" "$base_url$agent_path/duplicate")"
[[ "$duplicate_status" == "303" ]]
request --cookie "$cookies" --output "$page" "$base_url/agents"
grep -q 'Customization Smoke Agent (inactive)' "$page"
grep -q 'Customization Smoke Agent (inactive) copy' "$page"

printf 'Mainspring agent customization smoke test passed: create, provider controls, limits, conditional tools, durable versioning, update, deactivate, and duplicate.\n'
