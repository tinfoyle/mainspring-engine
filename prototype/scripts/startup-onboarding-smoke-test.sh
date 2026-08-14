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

post_step() {
  local path="$1"
  shift
  local status
  status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
    --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
    --data-urlencode "csrf_token=$csrf_token" "$@" "$base_url$path")"
  [[ "$status" == "303" ]]
}

request --output /dev/null --cookie-jar "$cookies" \
  --data-urlencode "email=$email" --data-urlencode "password=$password" "$base_url/login"
request --cookie "$cookies" --output "$page" "$base_url/development"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
[[ -n "$csrf_token" ]]

post_step /development/reset-onboarding --data-urlencode "confirm=reset"
request --cookie "$cookies" --output "$page" "$base_url/onboarding?legacy=1"
grep -q 'What are you bringing to Mainspring' "$page"
grep -q 'Trades' "$page"
grep -q 'Start a new business' "$page"

post_step /onboarding/template --data-urlencode "template_selection=startup"
request --cookie "$cookies" --output "$page" "$base_url/onboarding?legacy=1"
grep -q 'What kind of business are you planning' "$page"
grep -q 'SaaS or software product' "$page"

post_step /onboarding/template --data-urlencode "template_selection=saas"
request --cookie "$cookies" --output "$page" "$base_url/onboarding?legacy=1"
grep -q 'Describe the business you want to start' "$page"
grep -q 'Systems you have chosen so far' "$page"

post_step /onboarding/business \
  --data-urlencode "business_name=Relay Zero" \
  --data-urlencode "trade=Vertical SaaS" \
  --data-urlencode "services=Simple scheduling and follow-up for independent field-service companies" \
  --data-urlencode "service_area=Independent HVAC contractors in the Southeast" \
  --data-urlencode "time_zone=America/New_York" \
  --data-urlencode "team_size=1" \
  --data-urlencode "customer_mix=b2b" \
  --data-urlencode "working_hours=Nights and weekends until demand is validated" \
  --data-urlencode "current_systems=Email" \
  --data-urlencode "current_systems=GitHub or GitLab"

request --cookie "$cookies" --output "$page" "$base_url/onboarding?step=2&legacy=1"
grep -q 'Design the first operating playbook' "$page"
grep -q 'biggest unknown that could make this business fail' "$page"

post_step /onboarding/operations \
  --data-urlencode "lead_intake=The founder will interview HVAC owners and recruit three design partners." \
  --data-urlencode "scheduling=Only problems confirmed by interviews enter a two-week prototype cycle." \
  --data-urlencode "estimate_to_job=Interview evidence becomes a narrow problem statement and prototype test." \
  --data-urlencode "job_to_invoice=Design partners receive a guided pilot before a paid monthly plan." \
  --data-urlencode "payments=Testing a per-company monthly subscription with no annual commitment." \
  --data-urlencode "vendor_bills=Hosting, email, domain, and minimal design contractor spend." \
  --data-urlencode "biggest_bottleneck=The founder does not yet know whether the problem is urgent enough to pay for." \
  --data-urlencode "important_exceptions=The launch must stay under a five-thousand-dollar validation budget."

post_step /onboarding/priorities \
  --data-urlencode "priorities=Validate the problem and ideal customer" \
  --data-urlencode "priorities=Define the offer, pricing, and revenue model" \
  --data-urlencode "priorities=Build the website and go-to-market plan"

request --cookie "$cookies" --output "$page" "$base_url/onboarding?step=4&legacy=1"
grep -q 'Startup Operations Lead' "$page"
grep -q 'Startup Finance &amp; Revenue Planner' "$page"
grep -q 'Product &amp; Market Strategist' "$page"
grep -q 'Website Conversion Advisor' "$page"

post_step /onboarding/team \
  --data-urlencode "enabled_software_ops_manager=true" --data-urlencode "name_software_ops_manager=Morgan" \
  --data-urlencode "enabled_revenue_analyst=true" --data-urlencode "name_revenue_analyst=Casey" \
  --data-urlencode "enabled_product_manager=true" --data-urlencode "name_product_manager=Avery" \
  --data-urlencode "enabled_website_advisor=true" --data-urlencode "name_website_advisor=Robin"

post_step /onboarding/permissions \
  --data-urlencode "read_business_records=true" \
  --data-urlencode "research_public_web=true" \
  --data-urlencode "comment_on_documents=true" \
  --data-urlencode "prepare_invoice_drafts=true" \
  --data-urlencode "draft_customer_email=true" \
  --data-urlencode "propose_schedule_edits=true" \
  --data-urlencode "propose_payments=true"

request --cookie "$cookies" --output "$page" "$base_url/onboarding?step=6&legacy=1"
grep -q 'Starting from scratch' "$page"
grep -q 'Launch Room' "$page"
post_step /onboarding/launch

request --cookie "$cookies" --output "$page" "$base_url/onboarding"
grep -q 'Launch Room is ready' "$page"
grep -q 'three riskiest unknowns' "$page"
request --cookie "$cookies" --output "$page" "$base_url/"
grep -q 'Launch Room' "$page"

printf 'Mainspring startup onboarding smoke test passed: stage fork, planning questions, launch priorities, founding personas, review, and launch.\n'
