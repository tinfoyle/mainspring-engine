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


def updated_config(original, snippet):
    """Upgrade only the recognized logging snippet, preserving all site blocks."""
    if '(spyglass_stage_access)' in original:
        for host, name in SITES.items():
            if not re.search(re.escape(host) + r'\s*\{\s*import spyglass_stage_access ' + name, original):
                raise ValueError('Existing Stage logging does not match the expected configuration.')
        if 'ops.stage.infiniteocean.net' in original and not re.search(
                r'ops\.stage\.infiniteocean\.net\s*\{\s*import spyglass_stage_access ops', original):
            raise ValueError('Existing admin host is missing its logging import.')
        pattern = r'(?m)^\(spyglass_stage_access\) \{\n.*?^\}'
        matches = list(re.finditer(pattern, original, re.S))
        desired = re.search(pattern, snippet, re.S)
        if len(matches) != 1 or desired is None:
            raise ValueError('Expected exactly one recognized logging snippet.')
        old = desired.group().replace('  log_append user_agent {http.request.header.User-Agent}\n', '')
        current = matches[0]
        if current.group() not in (old, desired.group()):
            raise ValueError('Logging snippet has local changes; inspect before upgrading.')
        return original[:current.start()] + desired.group() + original[current.end():]
    updated = original
    for host, name in SITES.items():
        updated, count = re.subn(r'(?m)^' + re.escape(host) + r'\s*\{\s*\n',
                                 host + ' {\n  import spyglass_stage_access ' + name + '\n', updated)
        if count != 1:
            raise ValueError('Expected exactly one existing site: ' + host)
    # Keep the global options block first, if one exists.
    first_site = re.search(r'(?m)^stage\.infiniteocean\.net\s*\{', updated)
    return updated[:first_site.start()] + snippet + '\n' + updated[first_site.start():]


def install():
    if os.geteuid() != 0:
        raise SystemExit('Run with sudo on the Stage VPS.')
    snippet = Path(__file__).with_name('Caddyfile.hostinger-access-log').read_text()
    original = CONFIG.read_text()
    try:
        updated = updated_config(original, snippet)
    except ValueError as error:
        raise SystemExit(str(error)) from error
    if updated == original:
        print('Stage access logging including user agents is already installed.')
        return
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
