#!/usr/bin/env bash

set -euo pipefail

base_url="${MAINSPRING_SMOKE_URL:-http://127.0.0.1:8088}"
tenant_host="${MAINSPRING_SMOKE_HOST:-demo.localhost}"
email="${MAINSPRING_SMOKE_EMAIL:-owner@example.test}"
password="${MAINSPRING_SMOKE_PASSWORD:-correct-horse-battery-staple}"
setup_token="${MAINSPRING_SMOKE_SETUP_TOKEN:-mainspring-local-setup}"

work_dir="$(mktemp -d)"
trap 'rm -rf -- "$work_dir"' EXIT

cookies="$work_dir/cookies"
page="$work_dir/page.html"
headers="$work_dir/headers"
events="$work_dir/events"

request() {
  curl --silent --show-error --fail-with-body -H "Host: $tenant_host" "$@"
}

setup_status="$(request --output "$page" --write-out '%{http_code}' "$base_url/setup")"
if [[ "$setup_status" == "200" ]]; then
  request --output /dev/null --cookie-jar "$cookies" \
    --data-urlencode "setup_token=$setup_token" \
    --data-urlencode "display_name=Demo Owner" \
    --data-urlencode "email=$email" \
    --data-urlencode "password=$password" \
    "$base_url/setup"
else
  request --output /dev/null --cookie-jar "$cookies" \
    --data-urlencode "email=$email" \
    --data-urlencode "password=$password" \
    "$base_url/login"
fi

request --cookie "$cookies" --output "$page" "$base_url/"
room_path="$(grep -oE '/boardrooms/[0-9a-f-]+' "$page" | head -n 1)"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
[[ -n "$room_path" && -n "$csrf_token" ]]

run_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" \
  --data-urlencode "prompt=Review the open invoices and scheduling risks for tomorrow." \
  "$base_url${room_path}/runs")"
if [[ "$run_status" != "303" ]]; then
  printf 'Creating a boardroom run returned HTTP %s:\n' "$run_status" >&2
  sed -n '1,12p' "$page" >&2
  exit 1
fi
conversation_path="$(awk 'BEGIN { IGNORECASE=1 } /^location:/ { gsub("\\r", "", $2); print $2 }' "$headers" | tail -n 1)"
[[ "$conversation_path" =~ ^/conversations/[0-9a-f-]+$ ]]

for _ in $(seq 1 40); do
  request --cookie "$cookies" --output "$page" "$base_url$conversation_path"
  if grep -q 'run-status-completed' "$page"; then
    break
  fi
  sleep 0.25
done

grep -q 'run-status-completed' "$page"
message_count="$(grep -o 'data-message-id=' "$page" | wc -l | tr -d ' ')"
[[ "$message_count" -ge 4 ]]
run_path="$(grep -oE '/runs/[0-9a-f-]+/events' "$page" | head -n 1 | sed 's#/events$##')"
[[ "$run_path" =~ ^/runs/[0-9a-f-]+$ ]]

request --cookie "$cookies" --output "$events" "$base_url$run_path/events?after=0"
grep -q '^event: finished' "$events"

follow_up_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" \
  --data-urlencode "prompt=Which risk should I handle first, and why?" \
  "$base_url$conversation_path/runs")"
[[ "$follow_up_status" == "303" ]]
follow_up_location="$(awk 'BEGIN { IGNORECASE=1 } /^location:/ { gsub("\\r", "", $2); print $2 }' "$headers" | tail -n 1)"
[[ "$follow_up_location" == "$conversation_path" ]]

for _ in $(seq 1 40); do
  request --cookie "$cookies" --output "$page" "$base_url$conversation_path"
  message_count="$(grep -o 'data-message-id=' "$page" | wc -l | tr -d ' ')"
  if grep -q 'run-status-completed' "$page" && [[ "$message_count" -ge 8 ]]; then
    break
  fi
  sleep 0.25
done

grep -q 'Which risk should I handle first, and why?' "$page"
grep -q 'run-status-completed' "$page"
[[ "$message_count" -ge 8 ]]

request --cookie "$cookies" --output "$page" "$base_url/schedules"
schedule_name="Smoke schedule $(date +%s)"
schedule_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" \
  --data-urlencode "name=$schedule_name" \
  --data-urlencode "boardroom_id=${room_path##*/}" \
  --data-urlencode "prompt=Run a scheduled smoke review." \
  --data-urlencode "every_number=1" \
  --data-urlencode "every_unit=hours" \
  --data-urlencode "time_zone=UTC" \
  "$base_url/schedules")"
[[ "$schedule_status" == "303" ]]

request --cookie "$cookies" --output "$page" "$base_url/schedules"
grep -q "$schedule_name" "$page"
schedule_id="$(grep -oE '/schedules/[0-9a-f-]+/trigger' "$page" | head -n 1 | cut -d/ -f3)"
[[ -n "$schedule_id" ]]

trigger_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" \
  "$base_url/schedules/$schedule_id/trigger")"
[[ "$trigger_status" == "303" ]]

for _ in $(seq 1 40); do
  request --cookie "$cookies" --output "$page" "$base_url$room_path"
  if grep -q "$schedule_name" "$page"; then
    break
  fi
  sleep 0.25
done
grep -q "$schedule_name" "$page"
grep -q "$(date '+%B %-d, %Y')" "$page"

for action in pause delete; do
  extra_args=()
  if [[ "$action" == "pause" ]]; then
    extra_args+=(--data-urlencode "paused=true")
  fi
  action_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
    --output "$page" --write-out '%{http_code}' \
    --data-urlencode "csrf_token=$csrf_token" "${extra_args[@]}" \
    "$base_url/schedules/$schedule_id/$action")"
  [[ "$action_status" == "303" ]]
done

request --cookie "$cookies" --output "$page" "$base_url/schedules"
if grep -q "$schedule_name" "$page"; then
  printf 'Deleted smoke schedule is still visible.\n' >&2
  exit 1
fi

printf 'Mainspring smoke test passed: routing, auth, persistent follow-up conversation, %s messages across two durable runs, SSE replay, and schedule lifecycle.\n' "$message_count"
