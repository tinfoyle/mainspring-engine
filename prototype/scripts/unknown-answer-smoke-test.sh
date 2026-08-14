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
location_from_headers() { awk 'tolower($1) == "location:" {gsub("\r", "", $2); print $2}' "$headers" | tail -n 1 | cut -d'?' -f1; }

request --output /dev/null --cookie-jar "$cookies" \
  --data-urlencode "email=$email" --data-urlencode "password=$password" "$base_url/login"
request --cookie "$cookies" --output "$page" "$base_url/development"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
room_path="$(grep -oE '/boardrooms/[0-9a-f-]+/conversations' "$page" | head -n 1 | sed 's#/conversations$##')"
[[ -n "$csrf_token" && -n "$room_path" ]]
grep -q 'Test an unknown answer' "$page"
grep -q 'Run unknown-answer test' "$page"

run_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" \
  --data-urlencode 'prompt=[demo:unknown-answer:legal-compliance] Are we in legal compliance?' \
  "$base_url${room_path}/conversations")"
[[ "$run_status" == "303" ]]
conversation_path="$(location_from_headers)"
[[ "$conversation_path" =~ ^/conversations/[0-9a-f-]+$ ]]

for _ in $(seq 1 180); do
  request --cookie "$cookies" --output "$page" "$base_url$conversation_path"
  if grep -q 'run-status-awaiting_approval' "$page" && grep -q 'Approve and create work item' "$page"; then
    break
  fi
  sleep 0.5
done
grep -q 'run-status-awaiting_approval' "$page"
grep -q 'searched the authorized document library' "$page"
grep -q 'do not contain sufficient evidence' "$page"
grep -q 'compliance assessment' "$page"
grep -q '3 turns' "$page"
grep -q 'Proposed next step' "$page"
grep -q 'Create work item' "$page"
grep -q 'Work plan' "$page"
grep -q 'Definition of done' "$page"

approval_path="$(grep -oE '/approvals/[0-9a-f-]{36}/approve' "$page" | head -n 1)"
[[ -n "$approval_path" ]]
approval_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" "$base_url$approval_path")"
[[ "$approval_status" == "303" ]]

request --cookie "$cookies" --get --data-urlencode 'status=active' \
  --data-urlencode 'kind=ticket' --data-urlencode 'q=Determine legal-compliance status' \
  --output "$page" "$base_url/work"
grep -q 'Determine legal-compliance status' "$page"
grep -q 'Confirm jurisdictions' "$page"
grep -q 'qualified legal counsel' "$page"
grep -q 'High' "$page"

printf 'Mainspring unknown-answer smoke test passed: search, visible compliance-specialist handoff, manager synthesis, approval, and actionable linked work.\n'
