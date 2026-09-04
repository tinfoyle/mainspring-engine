#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repository_root="$(cd -- "$script_dir/../../.." && pwd)"

if [[ $# -ne 0 && !( $# -eq 2 && $1 == "--out" ) ]]; then
  printf '%s\n' "usage: $0 [--out HOST_REPORT_PATH]" >&2
  exit 2
fi

printf '%s\n' "Checking Baseline and Knowledge domain/API regressions..."
(
  cd "$repository_root"
  go test \
    ./cmd/openai-fixture \
    ./cmd/agent-journey-cert \
    ./internal/modules/agents \
    ./internal/application/knowledge \
    ./internal/modules/knowledge \
    ./internal/application/baseline \
    ./internal/modules/baseline \
    ./internal/transport/cellapi
)

printf '%s\n' "Checking the adaptive customer-visible Baseline conversation..."
docker run --rm --ipc=host \
  --env CI=1 \
  --volume "$repository_root/ui:/workspace/ui" \
  --volume spyglass-baseline-ui-node-modules:/workspace/ui/node_modules \
  --workdir /workspace/ui \
  mcr.microsoft.com/playwright@sha256:dcc5531e97840b9b5e794f2814476b21571c5124a3fca2267d73041f56e7580e \
  bash -lc 'npm ci && npm run build --workspace=@spyglass/app && cd apps/app && ../../node_modules/.bin/vitest run src/views/BaselineView.test.ts'

printf '%s\n' "Checking the connected signup, subscription, Knowledge, Baseline, Work, and database workflow..."
bash "$script_dir/verify-commercial-journey.sh" "$@"

printf '%s\n' "local Business Baseline journey certification passed"
