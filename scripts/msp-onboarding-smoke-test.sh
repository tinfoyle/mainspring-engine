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

for _ in $(seq 1 80); do
  if curl --silent --output /dev/null --fail -H "Host: $tenant_host" "$base_url/login"; then
    break
  fi
  sleep 0.25
done

login_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie-jar "$cookies" \
  --output "$page" --write-out '%{http_code}' --data-urlencode "email=$email" \
  --data-urlencode "password=$password" "$base_url/login")"
[[ "$login_status" == "303" ]]

request --cookie "$cookies" --output "$page" "$base_url/development"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
[[ -n "$csrf_token" ]]

post_step() {
  local path="$1"
  shift
  local status
  status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
    --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
    --data-urlencode "csrf_token=$csrf_token" "$@" "$base_url$path")"
  [[ "$status" == "303" ]]
}

post_step /development/reset-onboarding --data-urlencode "confirm=reset"

request --cookie "$cookies" --output "$page" "$base_url/onboarding?legacy=1"
grep -q 'MSP' "$page"

post_step /onboarding/template --data-urlencode "template_selection=msp"
request --cookie "$cookies" --output "$page" "$base_url/onboarding?legacy=1"
grep -q 'B2B SaaS, vertical SaaS, MSP, IT consultancy' "$page"
grep -q 'Products and managed services' "$page"
grep -q 'RMM or monitoring' "$page"

post_step /onboarding/business \
  --data-urlencode "business_name=Northstar Technology" \
  --data-urlencode "website_url=https://northstar.example" \
  --data-urlencode "trade=Managed service provider (MSP)" \
  --data-urlencode "services=Managed cloud, help desk, cybersecurity, backup, and technology planning" \
  --data-urlencode "service_area=Professional firms with 20 to 250 employees" \
  --data-urlencode "time_zone=America/New_York" \
  --data-urlencode "team_size=18" \
  --data-urlencode "customer_mix=b2b" \
  --data-urlencode "working_hours=Help desk 8 AM to 6 PM with an on-call rotation" \
  --data-urlencode "emergency_service=true" \
  --data-urlencode "current_systems=Billing or PSA platform" \
  --data-urlencode "current_systems=CRM or help desk" \
  --data-urlencode "current_systems=RMM or monitoring"

request --cookie "$cookies" --output "$page" "$base_url/onboarding?step=2&legacy=1"
grep -q 'ticket triage, SLA queues' "$page"
grep -q 'subscriptions, contracts, failed payments, and renewals' "$page"

post_step /onboarding/operations \
  --data-urlencode "lead_intake=Referrals, vendor partners, and website assessments create opportunities." \
  --data-urlencode "scheduling=PSA queues are prioritized by SLA, impact, project commitments, and maintenance windows." \
  --data-urlencode "estimate_to_job=Discovery becomes a scoped project or recurring service agreement." \
  --data-urlencode "job_to_invoice=Tickets, projects, and recurring services feed agreement and usage billing." \
  --data-urlencode "payments=Contracts renew annually and failed ACH payments create account tasks." \
  --data-urlencode "vendor_bills=Licensing, cloud, telecom, and subcontractor costs are reconciled monthly." \
  --data-urlencode "biggest_bottleneck=Ticket handoffs and stale client decisions put SLAs at risk." \
  --data-urlencode "important_exceptions=P1 incidents page the on-call engineer and strategic clients have custom SLAs."

post_step /onboarding/priorities \
  --data-urlencode "priorities=Improve SLA delivery and ticket flow" \
  --data-urlencode "priorities=Reduce churn, contract, and renewal risk" \
  --data-urlencode "priorities=Monitor reliability, security, and incident follow-up"

request --cookie "$cookies" --output "$page" "$base_url/onboarding?step=4&legacy=1"
grep -q 'Software Operations Manager' "$page"
grep -q 'Service Delivery Manager' "$page"
grep -q 'Technical Account Manager' "$page"
grep -q 'Cloud &amp; Systems Advisor' "$page"
grep -q 'Security &amp; Compliance Advisor' "$page"

post_step /onboarding/team \
  --data-urlencode "enabled_software_ops_manager=true" --data-urlencode "name_software_ops_manager=Morgan" \
  --data-urlencode "enabled_revenue_analyst=true" --data-urlencode "name_revenue_analyst=Casey" \
  --data-urlencode "enabled_customer_success=true" --data-urlencode "name_customer_success=Riley" \
  --data-urlencode "enabled_service_delivery_manager=true" --data-urlencode "name_service_delivery_manager=Cameron" \
  --data-urlencode "enabled_technical_account_manager=true" --data-urlencode "name_technical_account_manager=Emerson" \
  --data-urlencode "enabled_cloud_operations_advisor=true" --data-urlencode "name_cloud_operations_advisor=Blake" \
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

request --cookie "$cookies" --output "$page" "$base_url/onboarding?step=6&legacy=1"
grep -q 'Northstar Technology' "$page"
grep -q 'Delivery to customer' "$page"

post_step /onboarding/launch
request --cookie "$cookies" --output "$page" "$base_url/"
grep -q 'Company Operating Room' "$page"
grep -q 'cross-functional operating team' "$page"
room_path="$(grep -oE '/boardrooms/[0-9a-f-]+' "$page" | head -n 1)"
request --cookie "$cookies" --output "$page" "$base_url$room_path"
grep -q 'Service Delivery Manager' "$page"
grep -q 'Technical Account Manager' "$page"
grep -q 'Cloud &amp; Systems Advisor' "$page"
grep -q 'Security &amp; Compliance Advisor' "$page"

request --cookie "$cookies" --output "$page" "$base_url/schedules"
grep -q 'support and SLA risk' "$page"

printf 'Mainspring MSP variant smoke test passed: MSP onboarding, SLA priorities, launch, personas, and schedule copy.\n'
