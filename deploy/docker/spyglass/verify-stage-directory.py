#!/usr/bin/env python3
"""Verify Stage directory projections and public denial boundaries.
Prints aggregate facts only. Each projection read creates a staff audit event.
"""
import argparse
import json
import subprocess
import urllib.error
import urllib.request
import uuid

parser = argparse.ArgumentParser()
parser.add_argument('--stage', action='store_true', required=True)
parser.add_argument('--staff-user-id', type=uuid.UUID, required=True)
args = parser.parse_args()

def sql(query):
    result = subprocess.run(['docker', 'exec', 'spyglass-stage-global-db-1', 'psql',
        '-X', '-qAt', '-U', 'spyglass_migrator', '-d', 'spyglass', '-v', 'ON_ERROR_STOP=1',
        '-c', query], capture_output=True, text=True, timeout=30)
    if result.returncode:
        raise RuntimeError('Directory database verification failed; inspect privileged server logs.')
    return result.stdout.strip()

for kind, table in [('users', 'users'), ('teams', 'accounts')]:
    expected = int(sql('SELECT count(*) FROM public.' + table))
    seen = set()
    for page in [1, 2]:
        event = uuid.uuid4()
        result = json.loads(sql(f"SET ROLE spyglass_operations_projection; SELECT spyglass_operations_directory('{event}','{args.staff_user_id}','{kind}',{page},1,'OPS-DIRECTORY-CHECK','Verify paginated directory release.','stage')"))
        assert result['total'] == expected and result['page'] == page and result['page_size'] == 1
        rows = result[kind]
        assert len(rows) == (1 if expected >= page else 0)
        for row in rows:
            assert row['id'] not in seen
            seen.add(row['id'])
            fields = {'id','display_name','email','state','email_verified','team_count','created_at'} if kind == 'users' else {'id','display_name','slug','state','account_type','member_count','created_at'}
            assert set(row) == fields
        assert sql(f"SELECT count(*) FROM operations_access_events WHERE id='{event}' AND action='directory_viewed'") == '1'
    print(f'{kind}: total={expected} pagination=passed audit=passed field_allowlist=passed')

def post(host, origin):
    request = urllib.request.Request(host + '/api/operations/v1/directory',
        data=b'{"kind":"users","page":1,"page_size":25,"ticket":"OPS-CHECK","reason":"Verify public directory denial."}',
        headers={'Origin': origin, 'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            return response.status
    except urllib.error.HTTPError as error:
        return error.code

ops = 'https://ops.stage.infiniteocean.net'
app = 'https://app.stage.infiniteocean.net'
assert post(ops, ops) == 401
assert post(ops, app) == 403
assert post(app, app) == 404
print('anonymous=401 cross_origin=403 customer_host=404')
