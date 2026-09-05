"""Verify actual Stage edge collection and public admin denial boundaries.

Run on the Stage VPS with --stage. Uses only synthetic header/query markers;
prints aggregate verification facts, never cookies or credentials.
"""
from pathlib import Path
import json
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

if sys.argv[1:] != ['--stage']:
    raise SystemExit('Usage on the Stage VPS: python3 verify-stage-traffic.py --stage')

directory=Path('/opt/infiniteocean/caddy/data/spyglass-access')
marker='stage-traffic-check-'+uuid.uuid4().hex
started=time.time()
sites={'public':'stage.infiniteocean.net','app':'app.stage.infiniteocean.net','mcp':'mcp.stage.infiniteocean.net','ops':'ops.stage.infiniteocean.net'}
assert directory.stat().st_mode & 0o7777 == 0o2750
assert directory.stat().st_gid == 65532

def request(url,method='GET',origin=None,probe=False):
    headers={}
    if origin: headers['Origin']=origin
    if probe: headers.update({'X-Forwarded-For':'203.0.113.99','Cookie':marker,'Authorization':'Bearer '+marker})
    req=urllib.request.Request(url,method=method,headers=headers,data=b'{}' if method=='POST' else None)
    try:
        with urllib.request.urlopen(req,timeout=20) as response:
            response.read()
            return response.status
    except urllib.error.HTTPError as error:
        error.read()
        return error.code

for name,host in sites.items():
    status=request('https://'+host+('/login' if name=='app' else '/')+'?probe='+marker,probe=True)
    assert status < 500,(host,status)
    path=directory/(name+'.log')
    for attempt in range(20):
        lines=path.read_text().splitlines()
        recent=[json.loads(line) for line in lines[-30:] if line]
        if any(row['ts']>=started and row['request']['host']==host for row in recent): break
        time.sleep(.1)
    else: raise AssertionError('New access record missing: '+host)
    assert marker not in '\n'.join(lines)
    assert path.stat().st_mode & 0o777 == 0o640 and path.stat().st_gid == 65532
    for row in recent:
        assert not {'headers','uri','tls'} & row['request'].keys()
        assert 'resp_headers' not in row
        assert row['request']['remote_ip']!='203.0.113.99'
    print(name, 'appended=True redaction=True spoofed_forwarding_ignored=True', 'HTTP='+str(status))

ops='https://ops.stage.infiniteocean.net'
assert request(ops+'/api/operations/v1/traffic/reports','POST',ops)==401
assert request(ops+'/api/operations/v1/traffic/reports','POST','https://app.stage.infiniteocean.net')==403
assert request('https://app.stage.infiniteocean.net/api/operations/v1/traffic/reports','POST','https://app.stage.infiniteocean.net')==404
print('admin_unauthenticated=401 cross_origin=403 customer_host=404')

image='postgres:17.11-alpine3.24@sha256:18cfe3ef5e6815560c98237d6216d1e5119702fb0f3894c8785dd58b8bbe5d73'
subprocess.run(['docker','run','--rm','--network','none','--read-only','--user','65532:65532','--cap-drop','ALL','--security-opt','no-new-privileges:true','--volumes-from','spyglass-stage-operations-api-1:ro',image,'sh','-c','for name in public app mcp ops; do test -r /var/log/spyglass/access/$name.log || exit 1; done'],check=True,timeout=30)
print('non_root_log_readability=passed')
