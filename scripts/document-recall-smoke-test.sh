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
target_file="$work_dir/recall-target.md"
decoy_file="$work_dir/recall-decoy.md"
marker="ORCHID-7429"
decoy_marker="COBALT-1180"

printf '%s\n' '# Technician closeout recall protocol' '' "The technician closeout authorization phrase is $marker." 'Record it only after job photos and customer sign-off are attached.' >"$target_file"
printf '%s\n' '# Superseded technician closeout note' '' "The obsolete authorization phrase is $decoy_marker." 'This document is a decoy used to verify attachment isolation.' >"$decoy_file"

request() { curl --silent --show-error --fail-with-body -H "Host: $tenant_host" "$@"; }
location_from_headers() { awk 'tolower($1) == "location:" {gsub("\r", "", $2); print $2}' "$headers" | tail -n 1 | cut -d'?' -f1; }

request --output /dev/null --cookie-jar "$cookies" \
  --data-urlencode "email=$email" --data-urlencode "password=$password" "$base_url/login"
request --cookie "$cookies" --output "$page" "$base_url/documents"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
[[ -n "$csrf_token" ]]

upload_document() {
  local name="$1"
  local path="$2"
  local status
  status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
    --form "csrf_token=$csrf_token" --form "name=$name" --form "document=@$path;type=text/markdown" "$base_url/documents")"
  [[ "$status" == "303" ]]
  location_from_headers
}

target_path="$(upload_document 'Recall target protocol' "$target_file")"
decoy_path="$(upload_document 'Recall decoy protocol' "$decoy_file")"
target_id="${target_path#/documents/}"
decoy_id="${decoy_path#/documents/}"
[[ "$target_id" =~ ^[0-9a-f-]+$ && "$decoy_id" =~ ^[0-9a-f-]+$ && "$target_id" != "$decoy_id" ]]

request --cookie "$cookies" --output "$page" "$base_url/"
room_path="$(grep -oE '/boardrooms/[0-9a-f-]+' "$page" | head -n 1)"
[[ -n "$room_path" ]]
request --cookie "$cookies" --output "$page" "$base_url$room_path"
grep -q "name=\"document_id\" value=\"$target_id\"" "$page"
grep -q "name=\"document_id\" value=\"$decoy_id\"" "$page"

run_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" --data-urlencode "document_id=$target_id" \
  --data-urlencode 'prompt=[demo:document-search] What is the technician closeout authorization phrase?' \
  "$base_url${room_path}/conversations")"
[[ "$run_status" == "303" ]]
conversation_path="$(location_from_headers)"
[[ "$conversation_path" =~ ^/conversations/[0-9a-f-]+$ ]]

for _ in $(seq 1 180); do
  request --cookie "$cookies" --output "$page" "$base_url$conversation_path"
  if grep -q 'run-status-completed' "$page" && grep -q "$marker" "$page"; then
    break
  fi
  sleep 0.5
done
grep -q 'run-status-completed' "$page"
grep -q 'Attached knowledge' "$page"
grep -q 'Recall target protocol' "$page"
grep -q "$marker" "$page"
! grep -q "$decoy_marker" "$page"
! grep -q 'Needs Attention' "$page"

follow_up_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" \
  --data-urlencode 'prompt=[demo:document-search] Repeat the technician closeout authorization phrase from the attached document.' \
  "$base_url$conversation_path/runs")"
[[ "$follow_up_status" == "303" ]]

for _ in $(seq 1 180); do
  request --cookie "$cookies" --output "$page" "$base_url$conversation_path"
  marker_count="$(grep -o "$marker" "$page" | wc -l | tr -d ' ')"
  if grep -q 'run-status-completed' "$page" && [[ "$marker_count" -ge 2 ]]; then
    break
  fi
  sleep 0.5
done
[[ "$marker_count" -ge 2 ]]
grep -q 'Recall target protocol' "$page"
! grep -q "$decoy_marker" "$page"

printf 'Mainspring document recall smoke test passed: attach, retrieve, cite, isolate, persist, and recall on follow-up.\n'
