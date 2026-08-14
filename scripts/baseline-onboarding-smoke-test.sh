#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "$script_dir/.." && pwd)"
compose_file="${MAINSPRING_COMPOSE_FILE:-$repo_dir/deploy/docker/compose.dev.yml}"
base_url="${MAINSPRING_SMOKE_URL:-http://127.0.0.1:8088}"
tenant_host="${MAINSPRING_SMOKE_HOST:-demo.localhost}"
email="${MAINSPRING_SMOKE_EMAIL:-owner@example.test}"
password="${MAINSPRING_SMOKE_PASSWORD:-correct-horse-battery-staple}"

work_dir="$(mktemp -d)"
trap 'rm -rf -- "$work_dir"' EXIT
cookies="$work_dir/cookies"
page="$work_dir/page.html"
headers="$work_dir/headers"
evidence_file="$work_dir/operating-license.md"

printf '%s\n' \
  '# Smoke Test Plumbing operating license' \
  '' \
  'License number: BASELINE-SMOKE-2042' \
  'Jurisdiction: Charlotte, North Carolina' \
  'Renewal owner: Business owner' >"$evidence_file"

request() {
  curl --silent --show-error --fail-with-body -H "Host: $tenant_host" "$@"
}

post_form() {
  local path="$1"
  shift
  local status
  status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
    --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
    --data-urlencode "csrf_token=$csrf_token" "$@" "$base_url$path")"
  if [[ "$status" != "303" ]]; then
    printf 'Unexpected HTTP %s from %s\n' "$status" "$path" >&2
    grep -oE '<div class="alert[^>]*>[^<]+' "$page" >&2 || true
    return 1
  fi
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

post_form /development/reset-onboarding --data-urlencode "confirm=reset"
reset_document_state="$(docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -Atc \
	"SELECT (SELECT count(*) FROM documents) || '|' || (SELECT count(*) FROM conversation_documents) || '|' || (SELECT count(*) FROM boardroom_run_documents) || '|' || (SELECT count(*) FROM work_items) || '|' || (SELECT count(*) FROM external_actions WHERE action_type='tickets.create')")"
[[ "$reset_document_state" == "0|0|0|0|0" ]]
request --cookie "$cookies" --output "$page" "$base_url/onboarding?legacy=1"
grep -q 'Start a new business' "$page"
post_form /onboarding/template --data-urlencode "template_selection=trades"

request --cookie "$cookies" --output "$page" "$base_url/baseline"
grep -q 'Interview with Mia' "$page"
grep -q 'What name does the business operate under?' "$page"

answers=(
  'Smoke Test Plumbing'
  'https://smoke-test-plumbing.example'
  'Residential plumbing and field service'
  'Charlotte, North Carolina'
  'Emergency plumbing, water heaters, and fixture replacement'
  '7'
  'Confirm licenses, insurance, and the technician closeout process'
)
for answer in "${answers[@]}"; do
  post_form /baseline/interview --data-urlencode "answer=$answer"
done

message_sequence="$(docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -Atc \
  "SELECT string_agg(role || ':' || question_key, ',' ORDER BY message_order) FROM baseline_interview_messages WHERE assessment_id=(SELECT id FROM baseline_assessments WHERE tenant_id='00000000-0000-0000-0000-000000000001' AND status <> 'archived')")"
expected_sequence='agent:business_name,user:business_name,agent:website_url,user:website_url,agent:industry,user:industry,agent:primary_location,user:primary_location,agent:services,user:services,agent:team_size,user:team_size,agent:immediate_concern,user:immediate_concern,agent:'
[[ "$message_sequence" == "$expected_sequence" ]]

request --cookie "$cookies" --output "$page" "$base_url/baseline"
grep -q 'Evidence interview with Mia' "$page"
grep -q '0 of 14' "$page"
grep -q 'One topic at a time' "$page"
! grep -q '<strong>Sources</strong>' "$page"
! grep -q 'Recent financial statements' "$page"

upload_path="$(grep -oE '/baseline/evidence/[0-9a-f-]{36}/upload' "$page" | head -n 1)"
[[ -n "$upload_path" ]]
uploaded_requirement_id="$(cut -d/ -f4 <<<"$upload_path")"
upload_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --form "csrf_token=$csrf_token" \
  --form "document=@$evidence_file;type=text/markdown" \
  "$base_url$upload_path")"
[[ "$upload_status" == "303" ]]

request --cookie "$cookies" --output "$page" "$base_url/baseline?saved=evidence"
grep -q '1 of 14' "$page"
grep -q 'verified evidence' "$page"

interview_choices=(search_sources create_it obtain_it have_it not_applicable)
for index in $(seq 0 20); do
  request --cookie "$cookies" --output "$page" "$base_url/baseline"
  if grep -q 'Interview complete' "$page"; then
    break
  fi
  interview_path="$(grep -oE '/baseline/evidence/[0-9a-f-]{36}/interview' "$page" | head -n 1)"
  [[ -n "$interview_path" ]]
  choice="${interview_choices[$((index % ${#interview_choices[@]}))]}"
  post_form "$interview_path" --data-urlencode "choice=$choice" --data-urlencode "answer=Smoke interview answer $index for $choice"
done
request --cookie "$cookies" --output "$page" "$base_url/baseline"
grep -q 'Interview complete' "$page"
grep -q '14 of 14' "$page"
grep -q 'Review Mia' "$page"
grep -qi 'location: /baseline#evidence-interview-complete' "$headers"

post_form /baseline/advance --data-urlencode 'phase=gap_review'
request --cookie "$cookies" --output "$page" "$base_url/baseline"
grep -q 'What Mia heard' "$page"
grep -q 'Review the documented gaps' "$page"
grep -q 'You told Mia' "$page"
grep -q 'Recorded outcome' "$page"
grep -q 'Correct this summary' "$page"

mapfile -t resolution_paths < <(grep -oE '/baseline/evidence/[0-9a-f-]{36}' "$page" | sort -u)
[[ "${#resolution_paths[@]}" -ge 10 ]]
dispositions=(search_sources create_it obtain_it have_it)
responsibilities=(agent owner shared external)
for index in "${!resolution_paths[@]}"; do
  extra=()
  if [[ "$index" == "0" ]]; then
    extra+=(--data-urlencode 'renewal_due=2099-12-31')
  fi
  post_form "${resolution_paths[$index]}" \
    --data-urlencode "disposition=${dispositions[$((index % 4))]}" \
    --data-urlencode "responsibility=${responsibilities[$((index % 4))]}" \
    "${extra[@]}"
done

post_form /baseline/advance --data-urlencode 'phase=plan_approval'
request --cookie "$cookies" --output "$page" "$base_url/baseline"
grep -q 'Create the Business Baseline Plan' "$page"
grep -q 'agent tasks' "$page"
grep -q 'external dependencies' "$page"

unapproved_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --output "$page" --write-out '%{http_code}' \
  --data-urlencode "csrf_token=$csrf_token" "$base_url/baseline/plan")"
[[ "$unapproved_status" == "400" ]]
grep -q 'Confirm approval before creating' "$page"

post_form /baseline/plan --data-urlencode 'approve=true'
parent_id="$(grep -oiE 'work_item=[0-9a-f-]{36}' "$headers" | head -n 1 | cut -d= -f2)"
[[ -n "$parent_id" ]]
parent_path="/work/$parent_id"
request --cookie "$cookies" --output "$page" "$base_url/baseline"
grep -q 'Your Mainspring workspace is ready' "$page"
grep -q 'Finish onboarding' "$page"
grep -q 'View work queue' "$page"
! grep -q 'Open baseline work plan' "$page"

request --cookie "$cookies" --output "$page" "$base_url/?welcome=1"
grep -q 'Welcome to Mainspring' "$page"
grep -q 'Your baseline work plan is running in the background' "$page"
grep -q 'Your business toolkit' "$page"
grep -q '>Baseline<' "$page"
grep -q '>Boardrooms<' "$page"
grep -q '>Work<' "$page"
grep -q '>Documents<' "$page"
grep -q '>Agents<' "$page"
grep -q '>Operations<' "$page"

request --cookie "$cookies" --output "$page" "$base_url/email?return_to=baseline"
grep -q 'Connect a mailbox to the documented baseline' "$page"
grep -q 'name="imap_username"' "$page"
grep -q 'name="imap_password"' "$page"
grep -q 'name="smtp_username"' "$page"
grep -q 'name="smtp_password"' "$page"
grep -q 'name="return_to" value="baseline"' "$page"
grep -q 'Replace with a different mailbox' "$page"
! grep -q 'value="office@example.test"' "$page"
! grep -q 'value="imap.example.test"' "$page"
! grep -q 'value="smtp.example.test"' "$page"

request --cookie "$cookies" --output "$page" "$base_url$parent_path"
grep -q 'Establish the documented business baseline' "$page"
grep -q 'Subtasks' "$page"
grep -Eq 'Search connected sources for|Create:|Obtain:|Locate and verify:' "$page"
mapfile -t child_paths < <(grep -oE '/work/[0-9a-f-]{36}' "$page" | sort -u | grep -vFx "$parent_path")
[[ "${#child_paths[@]}" -ge 10 ]]

assessment_state="$(docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -Atc \
  "SELECT ba.status || '|' || ba.phase || '|' || count(bwi.work_item_id) || '|' || count(DISTINCT wi.responsibility) FROM baseline_assessments ba JOIN baseline_work_items bwi ON bwi.assessment_id=ba.id JOIN work_items wi ON wi.id=bwi.work_item_id WHERE ba.tenant_id='00000000-0000-0000-0000-000000000001' AND ba.status <> 'archived' GROUP BY ba.id,ba.status,ba.phase")"
IFS='|' read -r baseline_status baseline_phase work_count responsibility_count <<<"$assessment_state"
[[ "$baseline_status" == "active" ]]
[[ "$baseline_phase" == "active" ]]
[[ "$work_count" -ge 10 ]]
[[ "$responsibility_count" -eq 4 ]]

onboarding_status="$(docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -Atc \
  "SELECT status FROM tenant_onboarding WHERE tenant_id='00000000-0000-0000-0000-000000000001'")"
[[ "$onboarding_status" == "completed" ]]

# Exercise the real startup maintenance pass with deterministic past-due
# timestamps. It must stale expired evidence and create both kinds of follow-up
# work without duplicating the original baseline plan.
docker compose -f "$compose_file" exec -T postgres psql -v ON_ERROR_STOP=1 -U mainspring -d tenant_demo \
  -c "UPDATE evidence_requirements SET status='confirmed',renewal_due_at=now()-interval '1 day',updated_at=now() WHERE id='$uploaded_requirement_id'; UPDATE baseline_assessments SET next_reassessment_at=now()-interval '1 day',updated_at=now() WHERE tenant_id='00000000-0000-0000-0000-000000000001' AND status <> 'archived';" >/dev/null
docker compose -f "$compose_file" restart tenant-demo >/dev/null
ready=false
for _ in $(seq 1 80); do
  if curl --silent --output /dev/null --fail -H "Host: $tenant_host" "$base_url/login"; then
    ready=true
    break
  fi
  sleep 0.25
done
[[ "$ready" == "true" ]]

renewal_id=""
for _ in $(seq 1 40); do
  renewal_id="$(docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -Atc \
    "SELECT id::text FROM work_items WHERE baseline_requirement_id='$uploaded_requirement_id' AND title LIKE 'Renew:%' AND status='open' ORDER BY created_at DESC LIMIT 1")"
  [[ -n "$renewal_id" ]] && break
  sleep 0.25
done
[[ -n "$renewal_id" ]]
[[ "$(docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -Atc "SELECT status FROM evidence_requirements WHERE id='$uploaded_requirement_id'")" == "stale" ]]
[[ "$(docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -Atc "SELECT count(*) FROM work_items wi JOIN baseline_work_items bwi ON bwi.work_item_id=wi.id JOIN baseline_assessments ba ON ba.id=bwi.assessment_id WHERE ba.status <> 'archived' AND bwi.requirement_id IS NULL AND wi.title='Reassess the documented business baseline' AND wi.status='open'")" -eq 1 ]]

# Completing linked work confirms its evidence requirement. Once every child is
# complete, the baseline must become ready without a separate administrative step.
post_form "/work/$renewal_id/status" --data-urlencode 'status=done' --data-urlencode 'return_to=detail'
for child_path in "${child_paths[@]}"; do
  post_form "$child_path/status" --data-urlencode 'status=done' --data-urlencode 'return_to=detail'
done
request --cookie "$cookies" --output "$page" "$base_url/baseline"
grep -q 'The business now has a documented baseline' "$page"
grep -q 'Start reassessment now' "$page"

post_form /baseline/reassess
request --cookie "$cookies" --output "$page" "$base_url/baseline"
grep -q 'Evidence interview with Mia' "$page"
reassessment_state="$(docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -Atc \
  "SELECT baseline_version || '|' || phase || '|' || (SELECT count(*) FROM business_facts bf WHERE bf.assessment_id=ba.id) || '|' || (SELECT count(*) FROM evidence_requirements er WHERE er.assessment_id=ba.id) FROM baseline_assessments ba WHERE tenant_id='00000000-0000-0000-0000-000000000001' AND status <> 'archived'")"
[[ "$reassessment_state" == "2|inventory|7|14" ]]

# The business description, not the initially selected demo template, scopes
# the evidence interview. A software company must not receive trade prompts.
post_form /development/reset-onboarding --data-urlencode "confirm=reset"
reset_work_state="$(docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -Atc \
	"SELECT count(*) || '|' || (SELECT last_value::text || '|' || is_called::text FROM work_items_number_seq) FROM work_items")"
[[ "$reset_work_state" == "0|1|false" || "$reset_work_state" == "0|1|f" ]]
post_form /onboarding/template --data-urlencode "template_selection=trades"
request --cookie "$cookies" --output "$page" "$base_url/baseline"
software_answers=(
  'Smoke Test Software'
  'https://software.example'
  'B2B software company'
  'Raleigh, North Carolina'
  'Subscription SaaS for independent retailers'
  '8'
  'Document our security, privacy, and release practices'
)
for answer in "${software_answers[@]}"; do
  post_form /baseline/interview --data-urlencode "answer=$answer"
done
request --cookie "$cookies" --output "$page" "$base_url/baseline"
grep -q 'Scoped for a Software and SaaS business' "$page"
grep -q 'Information security and access controls' "$page"
grep -q 'Development, release, and change workflow' "$page"
! grep -q 'Required licenses and permits' "$page"
! grep -q 'Quality and closeout checklist' "$page"
software_requirement_state="$(docker compose -f "$compose_file" exec -T postgres psql -U mainspring -d tenant_demo -Atc \
  "SELECT count(*) || '|' || count(*) FILTER (WHERE requirement_key IN ('product_definition','software_delivery','security_access_controls','privacy_data_handling','incident_continuity')) || '|' || count(*) FILTER (WHERE requirement_key IN ('licenses_permits','quality_closeout')) FROM evidence_requirements WHERE assessment_id=(SELECT id FROM baseline_assessments WHERE tenant_id='00000000-0000-0000-0000-000000000001' AND status <> 'archived')")"
[[ "$software_requirement_state" == "14|5|0" ]]

printf 'Mainspring business baseline smoke test passed: interview, business-adaptive evidence scope, research, evidence, approval, parent/child work, renewals, readiness, and reassessment.\n'
