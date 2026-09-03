#!/usr/bin/env bash
set -euo pipefail

stack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repository_root="$(cd "$stack_dir/../../.." && pwd)"
temporary="$(mktemp -d)"
network="spyglass-stage-contract-$$"
cleanup() {
  docker network rm "$network" >/dev/null 2>&1 || true
  rm -rf -- "$temporary"
}
trap cleanup EXIT

provider_file="$temporary/stage.providers.env"
cp "$stack_dir/env/stage.providers.example" "$provider_file"
sed -i \
  -e 's/SPYGLASS_STRIPE_WEBHOOK_SECRET=whsec_REPLACE/SPYGLASS_STRIPE_WEBHOOK_SECRET=whsec_stagecontract/' \
  -e 's/SPYGLASS_STRIPE_SECRET_KEY=sk_test_REPLACE/SPYGLASS_STRIPE_SECRET_KEY=sk_test_stagecontract/' \
  -e 's/SPYGLASS_SMTP_ADDRESS=mail.infiniteocean.net:465/SPYGLASS_SMTP_ADDRESS=smtp.test.invalid:587/' \
  -e 's/SPYGLASS_SMTP_SERVER_NAME=mail.infiniteocean.net/SPYGLASS_SMTP_SERVER_NAME=smtp.test.invalid/' \
  -e 's/SPYGLASS_SMTP_USERNAME=REPLACE/SPYGLASS_SMTP_USERNAME=stage_contract/' \
  -e 's/SPYGLASS_SMTP_PASSWORD=REPLACE/SPYGLASS_SMTP_PASSWORD=stage_contract_password/' \
  -e 's/SPYGLASS_SMTP_FROM_ADDRESS=REPLACE/SPYGLASS_SMTP_FROM_ADDRESS=stage@infiniteocean.net/' \
  -e 's/SPYGLASS_SMTP_FROM_NAME=REPLACE/SPYGLASS_SMTP_FROM_NAME=Infinite Ocean Stage/' \
  -e 's/SPYGLASS_TELNYX_API_KEY=REPLACE/SPYGLASS_TELNYX_API_KEY=KEY_stage_contract_sms_token/' \
  -e 's/SPYGLASS_TELNYX_FROM=+1REPLACE/SPYGLASS_TELNYX_FROM=+17575550199/' \
  -e 's/SPYGLASS_OPENAI_API_KEY=REPLACE/SPYGLASS_OPENAI_API_KEY=sk-proj-stagecontract/' \
  -e 's|SPYGLASS_OPENAI_ORIGIN=https://api.openai.com|SPYGLASS_OPENAI_ORIGIN=https://api.openai.com|' \
  -e 's|SPYGLASS_OPENAI_MODEL_PRICING_JSON=REPLACE_WITH_COMPACT_EXACT_MODEL_PRICE_BOOK|SPYGLASS_OPENAI_MODEL_PRICING_JSON={"gpt-test":{"input_micros_per_million_tokens":1000000,"output_micros_per_million_tokens":2000000}}|' \
  -e 's|SPYGLASS_AGENT_EXECUTION_POLICIES_JSON=REPLACE_WITH_COMPACT_FIVE_LEVEL_EXECUTION_MAP|SPYGLASS_AGENT_EXECUTION_POLICIES_JSON={"simple":{"provider":"openai","model":"gpt-test","fallback_models":[],"reasoning_effort":"low"},"efficient":{"provider":"openai","model":"gpt-test","fallback_models":[],"reasoning_effort":"low"},"balanced":{"provider":"openai","model":"gpt-test","fallback_models":[],"reasoning_effort":"medium"},"thorough":{"provider":"openai","model":"gpt-test","fallback_models":[],"reasoning_effort":"high"},"advanced":{"provider":"openai","model":"gpt-test","fallback_models":[],"reasoning_effort":"high"}}|' \
  "$provider_file"
chmod 600 "$provider_file"
google_login_file="$temporary/google-login-client"
printf '%s\n' 'stage-contract-client:stage-contract-secret' >"$google_login_file"
chmod 600 "$google_login_file"

secret_dir="$temporary/secrets"
bash "$stack_dir/prepare-stage-secrets.sh" "$provider_file" "$secret_dir" "$network" "" "$google_login_file"
# OpenSSL timestamps certificates to whole seconds. Give the verifier a full
# clock tick so fast local filesystems cannot observe a just-issued certificate
# as not-yet-valid.
sleep 1
env_file="$secret_dir/stage.env"

carried_dir="$temporary/secrets-carried"
bash "$stack_dir/prepare-stage-secrets.sh" "$provider_file" "$carried_dir" "$network" "$env_file"
sed -E -e 's|^SPYGLASS_STAGE_SECRETS_DIRECTORY=.*$|SPYGLASS_STAGE_SECRETS_DIRECTORY=<normalized>|' -e 's|^SPYGLASS_STAGE_GOOGLE_LOGIN_CLIENT_FILE=.*$|SPYGLASS_STAGE_GOOGLE_LOGIN_CLIENT_FILE=<normalized>|' "$env_file" >"$temporary/original.normalized"
sed -E -e 's|^SPYGLASS_STAGE_SECRETS_DIRECTORY=.*$|SPYGLASS_STAGE_SECRETS_DIRECTORY=<normalized>|' -e 's|^SPYGLASS_STAGE_GOOGLE_LOGIN_CLIENT_FILE=.*$|SPYGLASS_STAGE_GOOGLE_LOGIN_CLIENT_FILE=<normalized>|' "$carried_dir/stage.env" >"$temporary/carried.normalized"
cmp "$temporary/original.normalized" "$temporary/carried.normalized"
cmp "$secret_dir/integration-source/cursor.key" "$carried_dir/integration-source/cursor.key"
test "$(stat -c %s "$secret_dir/integration-source/cursor.key")" = 32
test "$(stat -c %a "$secret_dir/integration-source/cursor.key")" = 640

legacy_env="$temporary/legacy-stage.env"
cp "$env_file" "$legacy_env"
sed -i '/^SPYGLASS_SCHEDULE_EXECUTION_WORKER_DATABASE_PASSWORD=/d;/^SPYGLASS_CELL_[AB]_SCHEDULE_EXECUTION_DATABASE_URL=/d;/^SPYGLASS_AFFILIATE_RETENTION_WORKER_DATABASE_PASSWORD=/d;/^SPYGLASS_AFFILIATE_RETENTION_WORKER_DATABASE_URL=/d;/^SPYGLASS_ANALYTICS_REPORTER_DATABASE_PASSWORD=/d;/^SPYGLASS_OPERATIONS_.*_DATABASE_PASSWORD=/d;/^SPYGLASS_OPERATIONS_.*_DATABASE_URL=/d' "$legacy_env"
legacy_upgrade_dir="$temporary/secrets-legacy-upgrade"
bash "$stack_dir/prepare-stage-secrets.sh" "$provider_file" "$legacy_upgrade_dir" "$network" "$legacy_env"
test "$(sed -n 's/^SPYGLASS_GLOBAL_DATABASE_PASSWORD=//p' "$legacy_env")" = "$(sed -n 's/^SPYGLASS_GLOBAL_DATABASE_PASSWORD=//p' "$legacy_upgrade_dir/stage.env")"
test -n "$(sed -n 's/^SPYGLASS_SCHEDULE_EXECUTION_WORKER_DATABASE_PASSWORD=//p' "$legacy_upgrade_dir/stage.env")"
test -n "$(sed -n 's/^SPYGLASS_AFFILIATE_RETENTION_WORKER_DATABASE_PASSWORD=//p' "$legacy_upgrade_dir/stage.env")"
test -n "$(sed -n 's/^SPYGLASS_ANALYTICS_REPORTER_DATABASE_PASSWORD=//p' "$legacy_upgrade_dir/stage.env")"
for operations_role in IDENTITY PROJECTION BILLING PRIVACY AFFILIATE; do
  test -n "$(sed -n "s/^SPYGLASS_OPERATIONS_${operations_role}_DATABASE_PASSWORD=//p" "$legacy_upgrade_dir/stage.env")"
  test -n "$(sed -n "s/^SPYGLASS_OPERATIONS_${operations_role}_DATABASE_URL=//p" "$legacy_upgrade_dir/stage.env")"
done

if bash "$stack_dir/prepare-stage-secrets.sh" "$provider_file" "$secret_dir" "$network" >/dev/null 2>&1; then
  echo 'stage secret preparation overwrote an initialized target' >&2
  exit 1
fi
test ! -e "$secret_dir/workload-ca/ca.key"
test -z "$(find "$secret_dir" -maxdepth 1 \( -name '*.csr' -o -name '*.cnf' \) -print -quit)"
docker network create "$network" >/dev/null
docker run --rm --network none --read-only \
  --mount "type=bind,src=$stack_dir/Caddyfile.stage,dst=/etc/caddy/Caddyfile,readonly" \
  caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d \
  caddy validate --config /etc/caddy/Caddyfile >/dev/null
for caddy_file in "$stack_dir/Caddyfile.local" "$stack_dir/Caddyfile.stage"; do
  grep -Eq 'baseline-assessments\(\?:/\.\*\)\?' "$caddy_file" || {
    echo "$(basename "$caddy_file") does not route the Baseline Account API through app-router" >&2
    exit 1
  }
  grep -Eq 'marketing\(\?:/\.\*\)\?' "$caddy_file" || {
    echo "$(basename "$caddy_file") does not route the Marketing Account API through app-router" >&2
    exit 1
  }
done

release_file="$repository_root/deploy/releases/0.3.0-rc.6.env"
bash "$stack_dir/verify-stage.sh" "$release_file" "$env_file"

printf '\nSPYGLASS_APPLICATION_IMAGE=ghcr.io/tinfoyle/spyglass-engine@sha256:%064d\n' 1 >>"$env_file"
if bash "$stack_dir/verify-stage.sh" "$release_file" "$env_file" >/dev/null 2>&1; then
  echo 'stage verifier accepted a secret-file image override' >&2
  exit 1
fi
echo 'stage release/secret separation verified'
