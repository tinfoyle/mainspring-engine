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

status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" \
  --data-urlencode 'prompt=[demo:web-read-recovery] Check this source' \
  "$base_url${room_path}/conversations")"
[[ "$status" == "303" ]]
conversation_path="$(location_from_headers)"
[[ "$conversation_path" =~ ^/conversations/[0-9a-f-]+$ ]]

for _ in $(seq 1 180); do
  request --cookie "$cookies" --output "$page" "$base_url$conversation_path"
  if grep -q 'run-status-completed' "$page"; then
    break
  fi
  if grep -q 'run-status-failed' "$page"; then
    printf 'web.read recovery run failed instead of degrading gracefully\n' >&2
    exit 1
  fi
  sleep 0.5
done

grep -q 'run-status-completed' "$page"
grep -q 'continued the run using the evidence that remained available' "$page"
grep -q 'not treated as verified evidence' "$page"
grep -q 'The research request did not complete' "$page"

printf 'Mainspring web.read recovery smoke test passed: a failed page read remained visible and did not fail the run.\n'
