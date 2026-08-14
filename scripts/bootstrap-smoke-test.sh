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

ready=false
for _ in $(seq 1 240); do
  if curl --silent --output /dev/null --fail -H "Host: $tenant_host" "$base_url/setup"; then
    ready=true
    break
  fi
  sleep 0.5
done
[[ "$ready" == "true" ]]

setup_status="$(curl --silent --show-error -H "Host: $tenant_host" --output "$page" --write-out '%{http_code}' "$base_url/setup")"
if [[ "$setup_status" == "200" ]]; then
  create_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie-jar "$cookies" --output "$page" --write-out '%{http_code}' \
    --data-urlencode "setup_token=$setup_token" --data-urlencode 'display_name=Test Owner' \
    --data-urlencode "email=$email" --data-urlencode "password=$password" "$base_url/setup")"
  [[ "$create_status" == "303" ]]
else
  login_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie-jar "$cookies" --output "$page" --write-out '%{http_code}' \
    --data-urlencode "email=$email" --data-urlencode "password=$password" "$base_url/login")"
  [[ "$login_status" == "303" ]]
fi

home_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" --output "$page" --write-out '%{http_code}' "$base_url/")"
[[ "$home_status" == "200" || "$home_status" == "303" ]]

printf 'Mainspring bootstrap smoke test passed: tenant ready and owner authenticated.\n'
