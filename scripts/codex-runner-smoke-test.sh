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
request --cookie "$cookies" --output "$page" "$base_url/"
room_path="$(grep -oE '/boardrooms/[0-9a-f-]+' "$page" | head -n 1)"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"

curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" \
  --data-urlencode "csrf_token=$csrf_token" \
  --data-urlencode 'prompt=Give one concise operational recommendation. This is a real Codex runner integration check.' \
  "$base_url${room_path}/runs"
conversation_path="$(awk 'BEGIN { IGNORECASE=1 } /^location:/ { gsub("\r", "", $2); print $2 }' "$headers" | tail -n 1)"
[[ "$conversation_path" =~ ^/conversations/[0-9a-f-]+$ ]]

for _ in $(seq 1 300); do
  request --cookie "$cookies" --output "$page" "$base_url$conversation_path"
  grep -q 'run-status-completed' "$page" && break
  if grep -q 'run-status-failed' "$page"; then
    printf 'The Codex-backed run failed:\n' >&2
    grep -oE 'run-error[^<]*|Invocation failed[^<]*' "$page" >&2 || true
    exit 1
  fi
  sleep 1
done

grep -q 'run-status-completed' "$page"
grep -q 'class="message' "$page"
if command -v docker >/dev/null 2>&1; then
  [[ "$(docker ps --filter label=com.mainspring.ephemeral=true --format '{{.ID}}' | wc -l | tr -d ' ')" == "0" ]]
fi

printf 'Real Codex CLI runner smoke test passed: Temporal completed a structured single-turn invocation in an ephemeral container.\n'
