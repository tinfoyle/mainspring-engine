#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "$script_dir/.." && pwd)"
compose_file="${MAINSPRING_COMPOSE_FILE:-$repo_dir/deploy/docker/compose.dev.yml}"
project="${MAINSPRING_TEST_PROJECT:-mainspring-test}"

if [[ ! "$project" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
  printf 'MAINSPRING_TEST_PROJECT must contain only lowercase letters, digits, hyphens, and underscores.\n' >&2
  exit 2
fi

export COMPOSE_PROJECT_NAME="$project"
export MAINSPRING_EDGE_PORT="${MAINSPRING_TEST_EDGE_PORT:-18088}"
export MAINSPRING_TEMPORAL_UI_PORT="${MAINSPRING_TEST_TEMPORAL_UI_PORT:-18233}"
export MAINSPRING_RUNNER_PORT="${MAINSPRING_TEST_RUNNER_PORT:-18090}"
export MAINSPRING_RUNNER_NETWORK="${MAINSPRING_TEST_RUNNER_NETWORK:-${project}_agent-egress}"
export MAINSPRING_RUNNER_PROVIDER=mock
export MAINSPRING_SMOKE_URL="http://127.0.0.1:$MAINSPRING_EDGE_PORT"
export MAINSPRING_COMPOSE_FILE="$compose_file"

compose=(docker compose -f "$compose_file")
started_at="$(date +%s)"

cleanup() {
  local status=$?
  trap - EXIT
  if [[ "$status" -ne 0 ]]; then
    printf '\nFull-suite failure. Container status:\n' >&2
    "${compose[@]}" ps >&2 || true
    printf '\nRecent service logs:\n' >&2
    "${compose[@]}" logs --no-color --tail=160 tenant-demo worker-demo runner-controller rag-demo postgres >&2 || true
  fi
  if [[ "${MAINSPRING_TEST_KEEP_STACK:-false}" == "true" ]]; then
    printf 'Test stack retained: project=%s url=%s\n' "$project" "$MAINSPRING_SMOKE_URL"
  else
    "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  exit "$status"
}
trap cleanup EXIT

run_case() {
  local name="$1"
  local script="$2"
  local case_started
  case_started="$(date +%s)"
  printf '\n==> %s\n' "$name"
  bash "$script_dir/$script"
  printf '<== %s (%ss)\n' "$name" "$(( $(date +%s) - case_started ))"
}

force_deterministic_agents() {
  "${compose[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U mainspring -d tenant_demo \
    -c "UPDATE personas SET provider = 'inherit', model = '', reasoning_effort = 'inherit', updated_at = now() WHERE enabled = true" >/dev/null
}

for command in docker bash curl; do
  command -v "$command" >/dev/null || { printf 'Required command not found: %s\n' "$command" >&2; exit 2; }
done

printf 'Starting isolated Mainspring test stack: project=%s url=%s\n' "$project" "$MAINSPRING_SMOKE_URL"
"${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
"${compose[@]}" up --build --detach --wait --wait-timeout 300

run_case 'Owner bootstrap' bootstrap-smoke-test.sh
run_case 'MCP surface' mcp-smoke-test.sh
run_case 'Unified onboarding' onboarding-smoke-test.sh
force_deterministic_agents
run_case 'Unknown-answer escalation' unknown-answer-smoke-test.sh
run_case 'Missing credential acquisition' missing-credential-smoke-test.sh
run_case 'Recoverable web page read' web-read-recovery-smoke-test.sh
run_case 'Document library' documents-smoke-test.sh
run_case 'Document attachment and recall' document-recall-smoke-test.sh
run_case 'Core boardroom and schedules' smoke-test.sh
run_case 'Email integration' email-smoke-test.sh
run_case 'Work queue' workqueue-smoke-test.sh
run_case 'Agent customization' agent-customization-smoke-test.sh
run_case 'Agent tool platform' agent-platform-smoke-test.sh
run_case 'RAG boundary' rag-smoke-test.sh
run_case 'Execution capacity' capacity-smoke-test.sh
run_case 'Documented business baseline onboarding' baseline-onboarding-smoke-test.sh
run_case 'Startup onboarding variant' startup-onboarding-smoke-test.sh
run_case 'SaaS onboarding variant' saas-onboarding-smoke-test.sh
run_case 'MSP onboarding variant' msp-onboarding-smoke-test.sh

printf '\nFull Mainspring test suite passed in %ss.\n' "$(( $(date +%s) - started_at ))"
