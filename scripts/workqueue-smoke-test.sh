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

post_work() {
  local path="$1"
  shift
  local status
  status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
    --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
    --data-urlencode "csrf_token=$csrf_token" "$@" "$base_url$path")"
  [[ "$status" == "303" ]]
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

request --cookie "$cookies" --output "$page" "$base_url/work"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
[[ -n "$csrf_token" ]]
grep -q 'Personal to-dos and structured tickets' "$page"

stamp="$(date +%s%N)"
todo_title="Call customer for approval $stamp"
ticket_title="Investigate overdue dispatch $stamp"

post_work /work \
  --data-urlencode "kind=todo" \
  --data-urlencode "title=$todo_title" \
  --data-urlencode "description=Confirm the scope and record the answer." \
  --data-urlencode "priority=normal" \
  --data-urlencode "due_date=2099-12-31" \
  --data-urlencode "assign_to_me=true"

request --cookie "$cookies" --get --data-urlencode "status=active" --data-urlencode "q=$todo_title" \
  --output "$page" "$base_url/work"
grep -qF "$todo_title" "$page"
grep -q 'To-do' "$page"
grep -q 'Assigned to' "$page"
todo_id="$(grep -oE '/work/[0-9a-f-]{36}/status' "$page" | head -n 1 | cut -d/ -f3)"
[[ -n "$todo_id" ]]

post_work "/work/$todo_id/status" \
  --data-urlencode "status=in_progress" \
  --data-urlencode "return_status=active" \
  --data-urlencode "return_kind=all" \
  --data-urlencode "return_q=$todo_title"
request --cookie "$cookies" --get --data-urlencode "status=active" --data-urlencode "q=$todo_title" \
  --output "$page" "$base_url/work"
grep -q 'In progress' "$page"

post_work /work \
  --data-urlencode "kind=ticket" \
  --data-urlencode "title=$ticket_title" \
  --data-urlencode "description=Find the blocked job and document the next action." \
  --data-urlencode "priority=urgent"

request --cookie "$cookies" --get --data-urlencode "status=active" --data-urlencode "kind=ticket" \
  --data-urlencode "q=$ticket_title" --output "$page" "$base_url/work"
grep -qF "$ticket_title" "$page"
grep -q 'Ticket' "$page"
grep -q 'Urgent' "$page"
grep -q 'Unassigned' "$page"
ticket_id="$(grep -oE '/work/[0-9a-f-]{36}/status' "$page" | head -n 1 | cut -d/ -f3)"
[[ -n "$ticket_id" ]]

request --cookie "$cookies" --output "$page" "$base_url/work/$ticket_id"
grep -q 'Ticket conversation' "$page"
grep -q 'Upload a new document' "$page"
grep -q 'Send to selected agents' "$page"
persona_id="$(grep -oE 'name="persona_id" value="[0-9a-f-]{36}' "$page" | head -n 1 | cut -d'"' -f4)"
[[ -n "$persona_id" ]]
workspace_document="$work_dir/ticket-evidence.txt"
printf 'Ticket workspace evidence marker: QUARTZ-%s\n' "$stamp" > "$workspace_document"
message_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --form "csrf_token=$csrf_token" \
  --form "persona_id=$persona_id" \
  --form 'prompt=[demo:ticket-workspace] Break this ticket into a concrete evidence-collection subtask.' \
  --form "document=@$workspace_document;type=text/plain" \
  "$base_url/work/$ticket_id/messages")"
[[ "$message_status" == "303" ]]

for _ in $(seq 1 120); do
  request --cookie "$cookies" --output "$page" "$base_url/work/$ticket_id"
  if grep -q 'Approve and create work item' "$page" && grep -q 'reviewed the parent ticket' "$page"; then
    break
  fi
  sleep 0.5
done
grep -q 'Approve and create work item' "$page"
grep -q 'reviewed the parent ticket' "$page"
grep -q 'ticket-evidence.txt' "$page"
approval_path="$(grep -oE '/approvals/[0-9a-f-]{36}/approve' "$page" | head -n 1)"
[[ -n "$approval_path" ]]
post_work "$approval_path"
request --cookie "$cookies" --output "$page" "$base_url/work/$ticket_id"
grep -q 'Collect supporting evidence' "$page"
grep -q 'Subtasks' "$page"

post_work "/work/$todo_id/status" --data-urlencode "status=done"
request --cookie "$cookies" --get --data-urlencode "status=done" --data-urlencode "q=$todo_title" \
  --output "$page" "$base_url/work"
grep -qF "$todo_title" "$page"
grep -q '>Done<' "$page"

# Keep repeated smoke runs from filling the active queue.
post_work "/work/$todo_id/status" --data-urlencode "status=canceled"
post_work "/work/$ticket_id/status" --data-urlencode "status=canceled"

printf 'Mainspring work queue smoke test passed: ticket details, selected-agent dialogue, document upload, approval-gated subtasks, and status lifecycle.\n'
