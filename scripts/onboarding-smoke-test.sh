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

ready=false
for _ in $(seq 1 40); do
  if curl --silent --output /dev/null --fail -H "Host: $tenant_host" "$base_url/login"; then
    ready=true
    break
  fi
  sleep 0.25
done
[[ "$ready" == "true" ]]

request --output /dev/null --cookie-jar "$cookies" \
  --data-urlencode "email=$email" --data-urlencode "password=$password" "$base_url/login"

request --cookie "$cookies" --output "$page" "$base_url/development"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
[[ -n "$csrf_token" ]]

reset_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" --data-urlencode "confirm=reset" \
  "$base_url/development/reset-onboarding")"
[[ "$reset_status" == "303" ]]

request --cookie "$cookies" --output "$page" "$base_url/onboarding"
grep -q 'Step 1 of 6' "$page"
grep -q 'Mia' "$page"
grep -q 'I am starting from scratch' "$page"

post_step() {
  local path="$1"
  shift
  local status
  status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
    --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
    --data-urlencode "csrf_token=$csrf_token" "$@" "$base_url$path")"
  [[ "$status" == "303" ]]
}

post_step /onboarding/stage --data-urlencode "business_stage=operating"
request --cookie "$cookies" --output "$page" "$base_url/onboarding"
grep -q 'Primary trade' "$page"

post_step /onboarding/business \
  --data-urlencode "business_name=Smoke Test Plumbing" \
  --data-urlencode "website_url=https://www.smoketestplumbing.example" \
  --data-urlencode "trade=plumbing" \
  --data-urlencode "services=Residential service calls and water heater replacements" \
  --data-urlencode "service_area=Charlotte, North Carolina" \
  --data-urlencode "time_zone=America/New_York" \
  --data-urlencode "team_size=7" \
  --data-urlencode "customer_mix=residential" \
  --data-urlencode "working_hours=Monday through Friday, 7 AM to 5 PM" \
  --data-urlencode "emergency_service=true" \
  --data-urlencode "current_systems=Email" \
  --data-urlencode "current_systems=QuickBooks"

post_step /onboarding/operations \
  --data-urlencode "lead_intake=Calls and web requests reach the owner." \
  --data-urlencode "scheduling=The owner assigns jobs from a shared calendar." \
  --data-urlencode "estimate_to_job=Approved estimates are copied into the calendar." \
  --data-urlencode "job_to_invoice=Technicians submit paper tickets at the end of the day." \
  --data-urlencode "payments=The owner follows up after 30 days." \
  --data-urlencode "vendor_bills=Bills are entered into QuickBooks on Fridays." \
  --data-urlencode "biggest_bottleneck=Missing technician notes delay invoices." \
  --data-urlencode "important_exceptions=Commercial jobs require a purchase order."

post_step /onboarding/priorities \
  --data-urlencode "priorities=Get completed work invoiced faster" \
  --data-urlencode "priorities=Reduce scheduling mistakes" \
  --data-urlencode "priorities=Catch jobs falling through the cracks"

request --cookie "$cookies" --output "$page" "$base_url/onboarding?step=4"
grep -q 'Website Advisor' "$page"
grep -q 'name_website_advisor' "$page"

post_step /onboarding/team \
  --data-urlencode "enabled_office_manager=true" --data-urlencode "name_office_manager=Alex" \
  --data-urlencode "enabled_bookkeeper=true" --data-urlencode "name_bookkeeper=Bailey" \
  --data-urlencode "enabled_dispatcher=true" --data-urlencode "name_dispatcher=Cameron" \
  --data-urlencode "enabled_business_developer=true" --data-urlencode "name_business_developer=Devon" \
  --data-urlencode "enabled_market_analyst=true" --data-urlencode "name_market_analyst=Emerson" \
  --data-urlencode "enabled_website_advisor=true" --data-urlencode "name_website_advisor=Harper" \
  --data-urlencode "enabled_legal_advisor=true" --data-urlencode "name_legal_advisor=Frankie" \
  --data-urlencode "enabled_hr_safety=true" --data-urlencode "name_hr_safety=Gray"

post_step /onboarding/permissions \
  --data-urlencode "read_business_records=true" \
  --data-urlencode "research_public_web=true" \
  --data-urlencode "comment_on_documents=true" \
  --data-urlencode "prepare_invoice_drafts=true" \
  --data-urlencode "draft_customer_email=true" \
  --data-urlencode "read_email_inbox=true" \
  --data-urlencode "propose_schedule_edits=true" \
  --data-urlencode "propose_payments=true"

request --cookie "$cookies" --output "$page" "$base_url/onboarding?step=6"
grep -q 'Smoke Test Plumbing' "$page"
grep -q 'Alex' "$page"

post_step /onboarding/launch
request --cookie "$cookies" --output "$page" "$base_url/onboarding"
grep -q 'Your back office is ready' "$page"
grep -q 'Smoke Test Plumbing' "$page"

request --cookie "$cookies" --output "$page" "$base_url/"
room_path="$(grep -oE '/boardrooms/[0-9a-f-]+' "$page" | head -n 1)"
request --cookie "$cookies" --output "$page" "$base_url$room_path"
grep -q 'Alex' "$page"
grep -q 'Bailey' "$page"
grep -q 'Cameron' "$page"
grep -q 'Devon' "$page"
grep -q 'Emerson' "$page"
grep -q 'Harper' "$page"
grep -q 'Frankie' "$page"
grep -q 'Gray' "$page"

if [[ "${MAINSPRING_SMOKE_RESET_AFTER:-false}" == "true" ]]; then
  post_step /development/reset-onboarding --data-urlencode "confirm=reset"
  request --cookie "$cookies" --output "$page" "$base_url/onboarding"
  grep -q 'Step 1 of 6' "$page"
  grep -q 'Demo Trade Co.' "$page"
fi

printf 'Mainspring onboarding smoke test passed: development reset, six guided steps, launch, and persona application.\n'
