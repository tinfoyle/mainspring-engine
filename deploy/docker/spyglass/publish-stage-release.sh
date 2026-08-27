#!/usr/bin/env bash
set -euo pipefail
shopt -s inherit_errexit

stack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repository_root="$(cd "$stack_dir/../../.." && pwd)"
release_directory="$repository_root/deploy/releases"

application_image=ghcr.io/tinfoyle/spyglass-engine
website_image=ghcr.io/tinfoyle/infinite-ocean-public-ui
private_ui_image=ghcr.io/tinfoyle/infinite-ocean-private-ui
trivy_image='docker.io/aquasec/trivy@sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969'

usage() {
  cat <<'EOF'
Usage: publish-stage-release.sh [--dry-run] VERSION

Build, attest, scan and push the matched Spyglass application/public/private
image set to GHCR, then write deploy/releases/VERSION.env with exact digests.

Environment:
  SPYGLASS_GHCR_TOKEN_FILE       Native-Linux mode-600 token file
                                (default: ~/.config/spyglass/ghcr-token)
  SPYGLASS_GHCR_USERNAME         GHCR account name (default: tinfoyle)
  SPYGLASS_RELEASE_PLATFORMS     Comma-separated BuildKit platforms
                                (default: linux/amd64)

The protected Stage environment file is never read or modified by this tool.
The generated release manifest must be reviewed and committed before deployment.
EOF
}

die() {
  echo "release publication refused: $*" >&2
  exit 1
}

dry_run=false
case "${1:-}" in
  -h|--help)
    usage
    exit 0
    ;;
  --dry-run)
    dry_run=true
    shift
    ;;
esac

version="${1:-}"
test "$#" = 1 || { usage >&2; exit 2; }
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || die "VERSION must be SemVer without a v prefix"
(( ${#version} <= 64 )) || die "VERSION is longer than 64 characters"

revision="$(git -C "$repository_root" rev-parse HEAD)"
branch="$(git -C "$repository_root" branch --show-current)"
release_file="$release_directory/$version.env"
platforms="${SPYGLASS_RELEASE_PLATFORMS:-linux/amd64}"
ghcr_username="${SPYGLASS_GHCR_USERNAME:-tinfoyle}"
token_file="${SPYGLASS_GHCR_TOKEN_FILE:-${HOME}/.config/spyglass/ghcr-token}"

test "$branch" = main || die "releases must be cut from main, not $branch"
git -C "$repository_root" diff --quiet || die "tracked worktree changes must be committed"
git -C "$repository_root" diff --cached --quiet || die "staged worktree changes must be committed"
test -z "$(git -C "$repository_root" ls-files --others --exclude-standard)" || die "untracked files must be removed or committed"
test "$(git -C "$repository_root" rev-parse origin/main)" = "$revision" || die "HEAD must equal origin/main"
test ! -e "$release_file" || die "$release_file already exists"
[[ "$platforms" =~ ^linux/(amd64|arm64)(,linux/(amd64|arm64))*$ ]] || die "SPYGLASS_RELEASE_PLATFORMS contains an unsupported platform"

if "$dry_run"; then
  cat <<EOF
Release publication dry run passed.
  version:   $version
  revision:  $revision
  platforms: $platforms
  output:    $release_file
EOF
  exit 0
fi

for command in docker jq; do
  command -v "$command" >/dev/null || die "$command is required"
done
docker buildx version >/dev/null 2>&1 || die "Docker Buildx is required"

test "${token_file#/}" != "$token_file" || die "SPYGLASS_GHCR_TOKEN_FILE must be absolute"
test -f "$token_file" && test ! -L "$token_file" || die "GHCR token must be a regular non-symlink file"
case "$(readlink -f -- "$token_file")" in
  /mnt/*) die "GHCR token must be stored on the native UbuntuRojo filesystem" ;;
esac
test "$(stat -c %a "$token_file")" = 600 || die "GHCR token file must have mode 600"
test -s "$token_file" || die "GHCR token file is empty"
test "$(wc -l < "$token_file")" -le 1 || die "GHCR token file must contain one line"

temporary_directory="$(mktemp -d)"
test -n "$temporary_directory" && test "$temporary_directory" != / || die "could not create a safe temporary directory"
cleanup() {
  rm -rf -- "$temporary_directory"
}
trap cleanup EXIT

original_docker_config="${DOCKER_CONFIG:-$HOME/.docker}"
buildx_plugin="$original_docker_config/cli-plugins/docker-buildx"
export DOCKER_CONFIG="$temporary_directory/docker-config"
mkdir -m 700 "$DOCKER_CONFIG"
if test -x "$buildx_plugin"; then
  mkdir -m 700 "$DOCKER_CONFIG/cli-plugins"
  ln -s "$buildx_plugin" "$DOCKER_CONFIG/cli-plugins/docker-buildx"
fi
docker buildx version >/dev/null 2>&1 || die "temporary Docker credentials hid the Buildx plugin"
ghcr_token="$(tr -d '\r\n' < "$token_file")"
test -n "$ghcr_token" || die "GHCR token is empty after newline removal"
printf '%s' "$ghcr_token" | docker login ghcr.io --username "$ghcr_username" --password-stdin >/dev/null

for image in "$application_image" "$website_image" "$private_ui_image"; do
  for tag in "$version" "sha-$revision"; do
    if docker buildx imagetools inspect "$image:$tag" >/dev/null 2>&1; then
      die "immutable tag already exists: $image:$tag"
    fi
  done
done

created="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

build_image() {
  local name="$1"
  local image="$2"
  local context="$3"
  local dockerfile="$4"
  local target="$5"
  local metadata_file="$temporary_directory/$name-metadata.json"
  local -a target_argument=()
  if test -n "$target"; then
    target_argument=(--target "$target")
  fi

  echo "Building and pushing $name image..." >&2
  docker buildx build \
    --file "$dockerfile" \
    "${target_argument[@]}" \
    --platform "$platforms" \
    --push \
    --provenance mode=max \
    --sbom true \
    --metadata-file "$metadata_file" \
    --tag "$image:$version" \
    --tag "$image:sha-$revision" \
    --build-arg "VERSION=$version" \
    --build-arg "REVISION=$revision" \
    --build-arg "CREATED=$created" \
    "$context" >&2

  jq -er '."containerimage.digest" | select(test("^sha256:[0-9a-f]{64}$"))' "$metadata_file"
}

application_digest="$(build_image application "$application_image" "$repository_root" "$repository_root/Dockerfile" '')"
website_digest="$(build_image public-ui "$website_image" "$repository_root/ui" "$repository_root/ui/Dockerfile" public-runtime)"
private_ui_digest="$(build_image private-ui "$private_ui_image" "$repository_root/ui" "$repository_root/ui/Dockerfile" app-runtime)"

export TRIVY_USERNAME="$ghcr_username"
export TRIVY_PASSWORD="$ghcr_token"
trivy_cache="${XDG_CACHE_HOME:-$HOME/.cache}/spyglass-release/trivy"
mkdir -p "$trivy_cache"

scan_image() {
  local name="$1"
  local reference="$2"
  local platform
  IFS=',' read -ra selected_platforms <<<"$platforms"
  for platform in "${selected_platforms[@]}"; do
    echo "Scanning $name for $platform..."
    docker run --rm \
      --env TRIVY_USERNAME \
      --env TRIVY_PASSWORD \
      --volume "$trivy_cache:/root/.cache/trivy" \
      "$trivy_image" \
      image \
      --image-src remote \
      --platform "$platform" \
      --scanners vuln,secret \
      --severity HIGH,CRITICAL \
      --exit-code 1 \
      "$reference"
  done
}

scan_image application "$application_image@$application_digest"
scan_image public-ui "$website_image@$website_digest"
scan_image private-ui "$private_ui_image@$private_ui_digest"

release_temporary="$temporary_directory/$version.env"
printf '%s\n' \
  "SPYGLASS_RELEASE_VERSION=$version" \
  "SPYGLASS_RELEASE_REVISION=$revision" \
  "SPYGLASS_APPLICATION_IMAGE=$application_image@$application_digest" \
  "SPYGLASS_WEBSITE_IMAGE=$website_image@$website_digest" \
  "SPYGLASS_PRIVATE_UI_IMAGE=$private_ui_image@$private_ui_digest" \
  >"$release_temporary"
chmod 644 "$release_temporary"
mv "$release_temporary" "$release_file"

cat <<EOF

Stage release publication completed.
  release manifest: $release_file
  source revision:  $revision

Review and commit the generated manifest before running deploy-stage.sh.
EOF
