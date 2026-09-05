#!/usr/bin/env python3
"""Add the reviewed admin host to the existing edge after deploying its services."""
from datetime import datetime, timezone
from pathlib import Path
import os
import shutil
import subprocess

CONFIG = Path('/opt/infiniteocean/caddy/Caddyfile')
CONTAINER = 'infiniteocean-caddy-1'
BLOCK = '''ops.stage.infiniteocean.net {
  import spyglass_stage_access ops
  import security_headers
  reverse_proxy spyglass-stage-edge:80
}
'''

def install():
    if os.geteuid() != 0:
        raise SystemExit('Run with sudo on the Stage VPS.')
    original = CONFIG.read_text()
    if BLOCK in original:
        print('Stage admin host is already installed.')
        return
    if 'ops.stage.infiniteocean.net' in original or '(spyglass_stage_access)' not in original:
        raise SystemExit('Unexpected host configuration; inspect before changing it.')
    updated = original.rstrip() + '\n\n' + BLOCK
    candidate = Path('/opt/infiniteocean/caddy/data/spyglass-admin-candidate.caddy')
    candidate.write_text(updated)
    os.chmod(candidate, 0o600)
    def caddy(action, path):
        subprocess.run(['docker', 'exec', CONTAINER, 'caddy', action, '--config', path, '--adapter', 'caddyfile'], check=True, timeout=60)
    try:
        caddy('validate', '/data/spyglass-admin-candidate.caddy')
        backup = CONFIG.with_name('Caddyfile.bak.' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '.pre-stage-admin')
        shutil.copy2(CONFIG, backup)
        CONFIG.write_text(updated)
        try:
            caddy('reload', '/etc/caddy/Caddyfile')
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
            CONFIG.write_text(original)
            caddy('reload', '/etc/caddy/Caddyfile')
            raise
        print('Stage admin host enabled. Backup: ' + str(backup))
    finally:
        candidate.unlink(missing_ok=True)

if __name__ == '__main__':
    install()
