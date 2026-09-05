#!/usr/bin/env python3
"""Install content-minimized Stage access logging on the existing Hostinger edge."""
from datetime import datetime, timezone
from pathlib import Path
import os
import re
import shutil
import subprocess

CONFIG = Path('/opt/infiniteocean/caddy/Caddyfile')
DATA = Path('/opt/infiniteocean/caddy/data')
CONTAINER = 'infiniteocean-caddy-1'
SITES = {'stage.infiniteocean.net': 'public', 'app.stage.infiniteocean.net': 'app',
         'mcp.stage.infiniteocean.net': 'mcp'}


def install():
    if os.geteuid() != 0:
        raise SystemExit('Run with sudo on the Stage VPS.')
    snippet = Path(__file__).with_name('Caddyfile.hostinger-access-log').read_text()
    original = CONFIG.read_text()
    if '(spyglass_stage_access)' in original:
        for host, name in SITES.items():
            if not re.search(re.escape(host) + r'\s*\{\s*import spyglass_stage_access ' + name, original):
                raise SystemExit('Existing Stage logging does not match the expected configuration.')
        print('Stage access logging is already installed.')
        return
    updated = original
    for host, name in SITES.items():
        updated, count = re.subn(r'(?m)^' + re.escape(host) + r'\s*\{\s*\n',
                                 host + ' {\n  import spyglass_stage_access ' + name + '\n', updated)
        if count != 1:
            raise SystemExit('Expected exactly one existing site: ' + host)
    # Keep the global options block first, if one exists.
    first_site = re.search(r'(?m)^stage\.infiniteocean\.net\s*\{', updated)
    updated = updated[:first_site.start()] + snippet + '\n' + updated[first_site.start():]
    log_dir = DATA / 'spyglass-access'
    log_dir.mkdir(exist_ok=True)
    os.chown(log_dir, 0, 65532)
    os.chmod(log_dir, 0o2750)
    candidate = DATA / 'spyglass-access-candidate.caddy'
    candidate.write_text(updated)
    os.chmod(candidate, 0o600)
    try:
        subprocess.run(['docker', 'exec', CONTAINER, 'caddy', 'validate', '--config',
                        '/data/spyglass-access-candidate.caddy', '--adapter', 'caddyfile'], check=True)
        backup = CONFIG.with_name('Caddyfile.bak.' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '.pre-stage-access')
        shutil.copy2(CONFIG, backup)
        # Preserve the inode of the host's existing bind-mounted Caddyfile.
        CONFIG.write_text(updated)
        try:
            subprocess.run(['docker', 'exec', CONTAINER, 'caddy', 'reload', '--config',
                            '/etc/caddy/Caddyfile', '--adapter', 'caddyfile'], check=True)
        except subprocess.CalledProcessError:
            CONFIG.write_text(original)
            subprocess.run(['docker', 'exec', CONTAINER, 'caddy', 'reload', '--config',
                            '/etc/caddy/Caddyfile', '--adapter', 'caddyfile'], check=True)
            raise
        print('Stage access logging enabled. Backup: ' + str(backup))
    finally:
        candidate.unlink(missing_ok=True)


if __name__ == '__main__':
    install()
