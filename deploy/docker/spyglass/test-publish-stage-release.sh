#!/usr/bin/env bash
set -euo pipefail

stack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
publisher="$stack_dir/publish-stage-release.sh"

bash -n "$publisher"
help="$(bash "$publisher" --help)"
grep -Fq 'Build, attest, scan and push' <<<"$help"
grep -Fq 'protected Stage environment file is never read or modified' <<<"$help"

if bash "$publisher" definitely-not-semver >/dev/null 2>&1; then
  echo 'publisher accepted an invalid version' >&2
  exit 1
fi

grep -Fq 'docker buildx imagetools inspect' "$publisher"
grep -Fq 'temporary Docker credentials hid the Buildx plugin' "$publisher"
grep -Fq -- '--provenance mode=max' "$publisher"
grep -Fq -- '--sbom true' "$publisher"
grep -Fq -- '--scanners vuln,secret' "$publisher"
grep -Fq 'deploy/releases' "$publisher"
! grep -Eq '(^|[^[:alnum:]])latest([^[:alnum:]]|$)' "$publisher"

echo 'stage release publisher contract verified'
