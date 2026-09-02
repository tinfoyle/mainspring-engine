#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/infiniteocean.net" && pwd)"

for route in index.html spyglass/index.html about/index.html contact/index.html privacy/index.html terms/index.html sms-consent/index.html 404.html; do
  test -s "$root/$route"
  grep -q '<meta name="viewport"' "$root/$route"
  grep -q '<main id="main"' "$root/$route"
done

for route in index.html spyglass/index.html about/index.html contact/index.html privacy/index.html terms/index.html sms-consent/index.html; do
  grep -q 'property="og:title"' "$root/$route"
  grep -q 'property="og:description"' "$root/$route"
  grep -q 'property="og:image" content="https://www.infiniteocean.net/assets/og.png"' "$root/$route"
  grep -q 'name="twitter:card" content="summary_large_image"' "$root/$route"
done

test -s "$root/assets/og.png"
test -s "$root/favicon.svg"
grep -q 'Your mobile information will not be sold or shared with third parties or affiliates for promotional or marketing purposes.' "$root/privacy/index.html"
grep -q 'Reply <strong>STOP</strong> to opt out or <strong>HELP</strong> for help.' "$root/terms/index.html"
grep -q 'Read and check the separate, unchecked consent box.' "$root/sms-consent/index.html"
grep -q 'Consent is not a condition of purchase.' "$root/sms-consent/index.html"
grep -q 'Infinite Ocean makes Spyglass.' "$root/index.html"
grep -q 'Keep the work from getting away from you.' "$root/spyglass/index.html"
grep -q 'AI is most useful when it disappears into the work.' "$root/about/index.html"

if grep -R -E -q 'Boat Shopper|Tack-tician|MMO Sailing|Nautical Software Company|View projects|Useful beats impressive|Small company\. Serious product|Better systems for the people' "$root" --include='*.html'; then
  echo "retired site language remains" >&2
  exit 1
fi

if grep -R -q 'Mainspring' "$root" --include='*.html'; then
  echo "internal platform name is present in public copy" >&2
  exit 1
fi

python3 - "$root" <<'PY'
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import urlparse
import sys

root = Path(sys.argv[1])

class Links(HTMLParser):
    def __init__(self):
        super().__init__()
        self.links = []
    def handle_starttag(self, tag, attrs):
        data = dict(attrs)
        key = "href" if tag in {"a", "link"} else "src" if tag in {"img", "script"} else None
        if key and data.get(key):
            self.links.append(data[key])

missing = []
for page in root.rglob("*.html"):
    parser = Links()
    parser.feed(page.read_text(encoding="utf-8"))
    for value in parser.links:
        parsed = urlparse(value)
        if parsed.scheme or value.startswith(("mailto:", "#")):
            continue
        path = parsed.path
        if not path:
            continue
        target = root / path.lstrip("/") if path.startswith("/") else page.parent / path
        if path.endswith("/"):
            target = target / "index.html"
        if not target.exists():
            missing.append(f"{page.relative_to(root)} -> {value}")
if missing:
    raise SystemExit("missing local links:\n" + "\n".join(missing))
PY

echo "corporate site verification passed"
