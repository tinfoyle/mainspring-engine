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
document_file="$work_dir/technician-closeout.md"
unsupported_file="$work_dir/sample.png"

printf '%s\n' '# Technician closeout checklist' '' '- Record labor and materials.' '- Attach job photos.' '- Submit notes before leaving the site.' >"$document_file"
printf '%s\n' 'This is not a supported business document.' >"$unsupported_file"

request() {
  curl --silent --show-error --fail-with-body -H "Host: $tenant_host" "$@"
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

request --cookie "$cookies" --output "$page" "$base_url/documents"
grep -q 'Upload a document' "$page"
csrf_token="$(grep -oE 'name="csrf_token" value="[^"]+' "$page" | head -n 1 | cut -d'"' -f4)"
[[ -n "$csrf_token" ]]

upload_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --dump-header "$headers" --output "$page" --write-out '%{http_code}' \
  --form "csrf_token=$csrf_token" --form 'name=Technician closeout checklist' \
  --form "document=@$document_file;type=text/markdown" "$base_url/documents")"
[[ "$upload_status" == "303" ]]
document_path="$(awk 'tolower($1) == "location:" {gsub("\r", "", $2); print $2}' "$headers" | tail -n 1 | cut -d'?' -f1)"
[[ "$document_path" == /documents/* ]]

request --cookie "$cookies" --output "$page" "$base_url$document_path"
grep -q 'Technician closeout checklist' "$page"
grep -q 'Record labor and materials' "$page"
grep -q 'Document text' "$page"

request --cookie "$cookies" --output "$page" "$base_url/documents"
grep -q 'Technician closeout checklist' "$page"

unsupported_status="$(curl --silent --show-error -H "Host: $tenant_host" --cookie "$cookies" \
  --output "$page" --write-out '%{http_code}' \
  --form "csrf_token=$csrf_token" --form "document=@$unsupported_file;type=image/png" "$base_url/documents")"
[[ "$unsupported_status" == "400" ]]
grep -qi 'supported formats are PDF, DOCX, TXT' "$page"

printf 'Mainspring document smoke test passed: upload, index, list, view, and file-type rejection.\n'
