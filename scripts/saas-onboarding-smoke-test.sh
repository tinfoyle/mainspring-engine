#!/usr/bin/env bash

set -euo pipefail

base_url="${MAINSPRING_SMOKE_URL:-http://127.0.0.1:8088}"
tenant_host="${MAINSPRING_SMOKE_HOST:-saas.localhost}"
email="${MAINSPRING_SMOKE_EMAIL:-founder@example.test}"
password="${MAINSPRING_SMOKE_PASSWORD:-correct-horse-battery-staple}"
setup_token="${MAINSPRING_SETUP_TOKEN:-mainspring-local-setup}"

work_dir="$(mktemp -d)"
trap 'rm -rf -- "$work_dir"' EXIT
cookies="$work_dir/cookies"
page="$work_dir/page.html"
headers="$work_dir/headers"

request() {
  curl --silent --show-error --fail-with-body -H "Host: $tenant_host" "$@"
}

for _ in $(seq 1 80); do
  if curl --silent --output /dev/null --fail -H "Host: $tenant_host" "$base_url/login"; then
    break
  fi
  sleep 0.25
done

login_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie-jar "$cookies" \
  --output "$page" --write-out '%{http_code}' --data-urlencode "email=$email" \
  --data-urlencode "password=$password" "$base_url/login")"

if [[ "$login_status" != "303" ]]; then
  setup_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie-jar "$cookies" \
    --output "$page" --write-out '%{http_code}' \
    --data-urlencode "setup_token=$setup_token" --data-urlencode "name=Sasha Founder" \
    --data-urlencode "display_name=Sasha Founder" \
    --data-urlencode "email=$email" --data-urlencode "password=$password" "$base_url/setup")"
  [[ "$setup_status" == "303" ]]
fi

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
grep -q 'Business model' "$page"
grep -q 'Target market' "$page"

post_step /onboarding/business \
  --data-urlencode "business_name=Relay Cloud" \
  --data-urlencode "website_url=https://relaycloud.example" \
  --data-urlencode "trade=B2B SaaS" \
  --data-urlencode "services=Workflow automation for field service teams" \
  --data-urlencode "service_area=US field service operators" \
  --data-urlencode "time_zone=America/New_York" \
  --data-urlencode "team_size=12" \
  --data-urlencode "customer_mix=b2b" \
  --data-urlencode "working_hours=Remote team, Monday through Friday" \
  --data-urlencode "emergency_service=true" \
  --data-urlencode "current_systems=Git provider" \
  --data-urlencode "current_systems=Subscription billing"

request --cookie "$cookies" --output "$page" "$base_url/onboarding?step=2"
grep -q 'How are product, project, support, and recurring-service priorities decided' "$page"

post_step /onboarding/operations \
  --data-urlencode "lead_intake=Trials and demos come from content, referrals, and partners." \
  --data-urlencode "scheduling=Product and engineering plan monthly with weekly delivery reviews." \
  --data-urlencode "estimate_to_job=Discovery is converted into roadmap problems and acceptance criteria." \
  --data-urlencode "job_to_invoice=The team releases weekly and bills subscriptions automatically." \
  --data-urlencode "payments=Failed payments enter a customer success follow-up queue." \
  --data-urlencode "vendor_bills=Cloud and software spend is reviewed monthly." \
  --data-urlencode "biggest_bottleneck=New accounts take too long to reach first value." \
  --data-urlencode "important_exceptions=Enterprise releases require a security review."

post_step /onboarding/priorities \
  --data-urlencode "priorities=Improve customer onboarding and activation" \
  --data-urlencode "priorities=Ship roadmap more predictably" \
  --data-urlencode "priorities=Monitor reliability, security, and incident follow-up"

request --cookie "$cookies" --output "$page" "$base_url/onboarding?step=4"
grep -q 'Product Manager' "$page"
grep -q 'Engineering Manager' "$page"
grep -q 'Service Delivery Manager' "$page"
grep -q 'Website Conversion Advisor' "$page"
grep -q 'Security &amp; Compliance Advisor' "$page"

post_step /onboarding/team \
  --data-urlencode "enabled_software_ops_manager=true" --data-urlencode "name_software_ops_manager=Morgan" \
  --data-urlencode "enabled_revenue_analyst=true" --data-urlencode "name_revenue_analyst=Casey" \
  --data-urlencode "enabled_customer_success=true" --data-urlencode "name_customer_success=Riley" \
  --data-urlencode "enabled_product_manager=true" --data-urlencode "name_product_manager=Avery" \
  --data-urlencode "enabled_engineering_manager=true" --data-urlencode "name_engineering_manager=Taylor" \
  --data-urlencode "enabled_reliability_advisor=true" --data-urlencode "name_reliability_advisor=Parker" \
  --data-urlencode "enabled_website_advisor=true" --data-urlencode "name_website_advisor=Robin" \
  --data-urlencode "enabled_security_advisor=true" --data-urlencode "name_security_advisor=Jordan"

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
grep -q 'Relay Cloud' "$page"
grep -q 'Company Operating Room' "$page"

post_step /onboarding/launch
request --cookie "$cookies" --output "$page" "$base_url/"
grep -q 'Company Operating Room' "$page"
grep -q 'focus on building the company' "$page"
room_path="$(grep -oE '/boardrooms/[0-9a-f-]+' "$page" | head -n 1)"
request --cookie "$cookies" --output "$page" "$base_url$room_path"
grep -q 'Product Manager' "$page"
grep -q 'Engineering Manager' "$page"
grep -q 'Website Conversion Advisor' "$page"
grep -q 'Security &amp; Compliance Advisor' "$page"

printf 'Mainspring SaaS variant smoke test passed: setup/reset, SaaS onboarding, launch, and specialist application.\n'
