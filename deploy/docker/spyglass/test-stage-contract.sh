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

secret_dir="$temporary/secrets"
for workload in admission-api app-router app-api-a app-api-b; do
  mkdir -p "$secret_dir/workload/$workload"
  for file in ca.crt tls.crt tls.key; do
    printf 'stage-contract-fixture\n' >"$secret_dir/workload/$workload/$file"
  done
  chmod 600 "$secret_dir/workload/$workload/tls.key"
done

env_file="$temporary/stage.env"
sed \
  -e 's/REPLACE/stage_contract/g' \
  -e "s|SPYGLASS_HOST_EDGE_NETWORK=infiniteocean_public|SPYGLASS_HOST_EDGE_NETWORK=$network|" \
  -e "s|SPYGLASS_STAGE_SECRETS_DIRECTORY=/opt/spyglass-stage/secrets|SPYGLASS_STAGE_SECRETS_DIRECTORY=$secret_dir|" \
  "$stack_dir/env/stage.example" >"$env_file"
chmod 600 "$env_file"
docker network create "$network" >/dev/null

release_file="$repository_root/deploy/releases/0.2.5-rc.2.env"
bash "$stack_dir/verify-stage.sh" "$release_file" "$env_file"

printf '\nSPYGLASS_APPLICATION_IMAGE=ghcr.io/tinfoyle/spyglass-engine@sha256:%064d\n' 1 >>"$env_file"
if bash "$stack_dir/verify-stage.sh" "$release_file" "$env_file" >/dev/null 2>&1; then
  echo 'stage verifier accepted a secret-file image override' >&2
  exit 1
fi
echo 'stage release/secret separation verified'
