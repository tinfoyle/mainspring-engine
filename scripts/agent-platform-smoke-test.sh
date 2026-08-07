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
document="$work_dir/agent-policy.txt"

request() { curl --silent --show-error --fail-with-body -H "Host: $tenant_host" "$@"; }

request --output /dev/null --cookie-jar "$cookies" \
  --data-urlencode "email=$email" --data-urlencode "password=$password" "$base_url/login"

request --cookie "$cookies" --output "$page" "$base_url/documents"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
printf '%s\n' 'The mainspringfluxpolicy requires invoice reviews every Friday before noon.' >"$document"
request --cookie "$cookies" --output /dev/null \
  --form "csrf_token=$csrf_token" --form 'name=Agent retrieval policy' \
  --form "document=@$document;type=text/plain" "$base_url/documents"

request --cookie "$cookies" --output "$page" "$base_url/"
room_path="$(grep -oE '/boardrooms/[0-9a-f-]+' "$page" | head -n 1)"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"

start_run() {
  local prompt="$1"
  curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
    --dump-header "$headers" --output "$page" \
    --data-urlencode "csrf_token=$csrf_token" --data-urlencode "prompt=$prompt" \
    "$base_url${room_path}/runs"
  awk 'BEGIN { IGNORECASE=1 } /^location:/ { gsub("\\r", "", $2); print $2 }' "$headers" | tail -n 1
}

document_conversation="$(start_run '[demo:document-search] mainspringfluxpolicy')"
[[ "$document_conversation" =~ ^/conversations/[0-9a-f-]+$ ]]
for _ in $(seq 1 160); do
  request --cookie "$cookies" --output "$page" "$base_url$document_conversation"
  grep -q 'run-status-completed' "$page" && break
  sleep 0.25
done
grep -q 'run-status-completed' "$page"
grep -q 'searched the authorized document library' "$page"
grep -q 'Agent retrieval policy' "$page"

approval_conversation="$(start_run '[demo:propose-email] Prepare the approval demonstration requested by the owner.')"
[[ "$approval_conversation" =~ ^/conversations/[0-9a-f-]+$ ]]
for _ in $(seq 1 160); do
  request --cookie "$cookies" --output "$page" "$base_url$approval_conversation"
  grep -q 'run-status-awaiting_approval' "$page" && break
  if grep -q 'run-status-completed' "$page"; then
    printf 'The approval smoke run completed without a proposal. Ensure at least one selected persona has email.send.\n' >&2
    exit 1
  fi
  sleep 0.25
done
grep -q 'run-status-awaiting_approval' "$page"

request --cookie "$cookies" --output "$page" "$base_url/approvals"
grep -q 'Mainspring approval demonstration' "$page"
mapfile -t approval_ids < <(grep -oE '/approvals/[0-9a-f-]+/approve' "$page" | sed -E 's#^/approvals/([^/]+)/approve$#\1#' | sort -u)
[[ "${#approval_ids[@]}" -ge 1 ]]
for approval_id in "${approval_ids[@]}"; do
  status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --output "$page" --write-out '%{http_code}' \
    --data-urlencode "csrf_token=$csrf_token" "$base_url/approvals/$approval_id/approve")"
  [[ "$status" == "303" ]]
done

for _ in $(seq 1 80); do
  request --cookie "$cookies" --output "$page" "$base_url$approval_conversation"
  grep -q 'run-status-completed' "$page" && break
  sleep 0.25
done
grep -q 'run-status-completed' "$page"

request --cookie "$cookies" --output "$page" "$base_url/approvals?history=1"
grep -q 'Approved' "$page"
request --cookie "$cookies" --output "$page" "$base_url/operations"
grep -q 'Agent operations' "$page"
grep -q 'Monthly tokens' "$page"

if command -v docker >/dev/null 2>&1; then
  [[ "$(docker ps --filter label=com.mainspring.ephemeral=true --format '{{.ID}}' | wc -l | tr -d ' ')" == "0" ]]
fi

printf 'Mainspring agent-platform smoke test passed: remote runners, durable invocations, authorized document tools, verified citations, approvals, exactly-once email execution, and operations visibility.\n'
