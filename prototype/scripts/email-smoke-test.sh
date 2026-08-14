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

request() {
  curl --silent --show-error --fail-with-body -H "Host: $tenant_host" "$@"
}

request --output /dev/null --cookie-jar "$cookies" \
  --data-urlencode "email=$email" --data-urlencode "password=$password" "$base_url/login"

request --cookie "$cookies" --output "$page" "$base_url/email"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
[[ -n "$csrf_token" ]]

settings_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" \
  --data-urlencode "name=Company inbox" \
  --data-urlencode "email_address=office@example.test" \
  --data-urlencode "display_name=Smoke Test Plumbing" \
  --data-urlencode "imap_host=imap.example.test" --data-urlencode "imap_port=993" --data-urlencode "imap_security=tls" \
  --data-urlencode "imap_username=office@example.test" --data-urlencode "imap_password=development-app-password" \
  --data-urlencode "smtp_host=smtp.example.test" --data-urlencode "smtp_port=465" --data-urlencode "smtp_security=tls" \
  --data-urlencode "smtp_username=office@example.test" --data-urlencode "smtp_password=development-app-password" \
  "$base_url/email/settings")"
[[ "$settings_status" == "303" ]]

request --cookie "$cookies" --output "$page" "$base_url/email"
grep -q 'Development mail simulator' "$page"
grep -q 'Friday service appointment' "$page"
idempotency_key="$(grep -oE 'name="idempotency_key" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
[[ -n "$idempotency_key" ]]

request --cookie "$cookies" --output "$page" "$base_url/email/messages/103"
grep -q 'Friday service appointment' "$page"
grep -q 'simulated inbox message' "$page"

send_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" --data-urlencode "idempotency_key=$idempotency_key" \
  --data-urlencode "to=customer@example.test" --data-urlencode "subject=Smoke test message" \
  --data-urlencode "body=This message exercises the development SMTP connector." --data-urlencode "confirm_send=send" \
  "$base_url/email/send")"
[[ "$send_status" == "303" ]]

request --cookie "$cookies" --output "$page" "$base_url/email?status=sent"
grep -q 'Email sent and recorded in the outbound audit ledger' "$page"

printf 'Mainspring email smoke test passed: encrypted setup, inbox, message read, and idempotent SMTP send.\n'
