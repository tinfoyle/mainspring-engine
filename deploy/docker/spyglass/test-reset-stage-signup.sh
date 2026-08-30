#!/usr/bin/env bash
set -euo pipefail

stack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
temporary="$(mktemp -d)"
cleanup() { rm -rf -- "$temporary"; }
trap cleanup EXIT

script="$stack_dir/reset-stage-signup.sh"
fake_bin="$stack_dir/testdata/reset-stage-signup"
account_id=22222222-2222-4222-8222-222222222222
email=fixture@example.com

bash -n "$script"
bash -n "$fake_bin/docker"
"$script" --help | grep -F 'Stage-only reset' >/dev/null

if "$script" inspect INVALID@example.com >"$temporary/invalid.log" 2>&1; then
  echo 'reset accepted a non-normalized email' >&2
  exit 1
fi
grep -F 'must already be lowercase' "$temporary/invalid.log" >/dev/null

export PATH="$fake_bin:$PATH"
export SPYGLASS_RESET_FAKE_STATE="$temporary/docker-state"
export SPYGLASS_STAGE_RESET_EVIDENCE_ROOT="$temporary/evidence"

"$script" inspect "$email" >"$temporary/inspect.log"
grep -F 'No data was changed.' "$temporary/inspect.log" >/dev/null
test ! -e "$temporary/evidence"

printf '0\n' >"$SPYGLASS_RESET_FAKE_STATE"
if SPYGLASS_STAGE_RESET_PROJECT_NAME=anything-else "$script" inspect "$email" >"$temporary/wrong-project.log" 2>&1; then
  echo 'reset accepted a non-Stage Compose project' >&2
  exit 1
fi
grep -F 'exact spyglass-stage Compose project' "$temporary/wrong-project.log" >/dev/null

printf '0\n' >"$SPYGLASS_RESET_FAKE_STATE"
if "$script" execute "$email" 33333333-3333-4333-8333-333333333333 "$email" >"$temporary/wrong-account.log" 2>&1; then
  echo 'reset accepted a mismatched Account confirmation' >&2
  exit 1
fi
grep -F 'does not match' "$temporary/wrong-account.log" >/dev/null

printf '0\n' >"$SPYGLASS_RESET_FAKE_STATE"
"$script" execute "$email" "$account_id" "$email" >"$temporary/execute.log"
grep -F 'synthetic Stage signup fixture reset' "$temporary/execute.log" >/dev/null
test "$(find "$temporary/evidence" -type f -name '*.log' | wc -l)" -eq 1
grep -F 'result=completed' "$temporary"/evidence/*.log >/dev/null

grep -F "account_type='inactive'" "$stack_dir/reset-stage-signup-global.sql" >/dev/null
grep -F "jsonb_array_length(effective_packages)=0" "$stack_dir/reset-stage-signup-global.sql" >/dev/null
grep -F "contains %s Account reference(s)" "$stack_dir/reset-stage-signup-global.sql" >/dev/null
grep -F "contains %s User reference(s)" "$stack_dir/reset-stage-signup-global.sql" >/dev/null
grep -F "cell table %I.%I contains" "$stack_dir/reset-stage-signup-cell.sql" >/dev/null

echo 'Stage signup reset safety contract verified'
