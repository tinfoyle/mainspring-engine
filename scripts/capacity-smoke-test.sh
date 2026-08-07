#!/usr/bin/env bash

set -euo pipefail

base_url="${MAINSPRING_SMOKE_URL:-http://127.0.0.1:8088}"
tenant_host="${MAINSPRING_SMOKE_HOST:-demo.localhost}"
email="${MAINSPRING_SMOKE_EMAIL:-owner@example.test}"
password="${MAINSPRING_SMOKE_PASSWORD:-correct-horse-battery-staple}"
compose_file="${MAINSPRING_COMPOSE_FILE:-deploy/docker/compose.dev.yml}"

work_dir="$(mktemp -d)"
cookies="$work_dir/cookies"
page="$work_dir/page.html"
headers="$work_dir/headers"

restore() {
  docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo \
    -c "update tenant_execution_policy set max_queued_runs=100,monthly_token_limit=10000000 where singleton" >/dev/null 2>&1 || true
  docker compose -f "$compose_file" start worker-demo >/dev/null 2>&1 || true
  rm -rf -- "$work_dir"
}
trap restore EXIT

request() { curl --silent --show-error --fail-with-body -H "Host: $tenant_host" "$@"; }
policy() {
  docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -c "$1" >/dev/null
}

request --output /dev/null --cookie-jar "$cookies" \
  --data-urlencode "email=$email" --data-urlencode "password=$password" "$base_url/login"
request --cookie "$cookies" --output "$page" "$base_url/"
room_path="$(grep -oE '/boardrooms/[0-9a-f-]+' "$page" | head -n 1)"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"

docker compose -f "$compose_file" stop worker-demo >/dev/null
policy "update tenant_execution_policy set max_queued_runs=1 where singleton"

curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --dump-header "$headers" --output "$page" \
  --data-urlencode "csrf_token=$csrf_token" --data-urlencode 'prompt=Capacity smoke queued run.' \
  "$base_url${room_path}/runs"
grep -qi '^location: /conversations/' "$headers"
queued_conversation="$(awk 'BEGIN { IGNORECASE=1 } /^location:/ { gsub("\r", "", $2); print $2 }' "$headers" | tail -n 1)"

status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" --data-urlencode 'prompt=This run must be rejected by queue admission.' \
  "$base_url${room_path}/runs")"
[[ "$status" == "400" ]]
grep -q 'tenant boardroom run queue is full' "$page"

policy "update tenant_execution_policy set max_queued_runs=100 where singleton"
docker compose -f "$compose_file" start worker-demo >/dev/null
for _ in $(seq 1 120); do

  request --cookie "$cookies" --output "$page" "$base_url$queued_conversation"
  grep -qE 'run-status-(completed|failed)' "$page" && break
  sleep 1
done
grep -q 'run-status-completed' "$page"

policy "update tenant_execution_policy set monthly_token_limit=1 where singleton"
curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --dump-header "$headers" --output "$page" \
  --data-urlencode "csrf_token=$csrf_token" --data-urlencode 'prompt=This run must be rejected by token admission.' \
  "$base_url${room_path}/runs"
conversation_path="$(awk 'BEGIN { IGNORECASE=1 } /^location:/ { gsub("\r", "", $2); print $2 }' "$headers" | tail -n 1)"
for _ in $(seq 1 60); do
  request --cookie "$cookies" --output "$page" "$base_url$conversation_path"
  grep -q 'run-status-failed' "$page" && break
  sleep 1
done
grep -q 'run-status-failed' "$page"
quota_error="$(docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -Atc \
  "select coalesce(error,'') from boardroom_runs where prompt='This run must be rejected by token admission.' order by created_at desc limit 1")"
[[ "$quota_error" == *"monthly token allowance has been reached"* ]]

printf 'Mainspring capacity smoke test passed: queued-run ceiling and monthly token admission are enforced durably.\n'
