"""Self-cleaning Stage certificate for consent, event ingestion and reporting.

Run on the Stage VPS with --stage. Creates five anonymous test browsers and
erases their privacy subjects in finally; never touches an ordinary account.
"""
import http.cookiejar, json, subprocess, sys, urllib.request, urllib.error, uuid
from datetime import datetime, timezone, timedelta

if sys.argv[1:] != ['--stage']:
    raise SystemExit('Usage on the Stage VPS: python3 certify-stage-analytics.py --stage')

PUBLIC='https://stage.infiniteocean.net'
APP='https://app.stage.infiniteocean.net'
clients=[]
event_ids=[]
start=(datetime.now(timezone.utc)-timedelta(seconds=1)).isoformat()

def sql(query):
    result=subprocess.run(['docker','exec','spyglass-stage-global-db-1','psql','-X','-qAt','-U','spyglass_migrator','-d','spyglass','-v','ON_ERROR_STOP=1','-c',query],capture_output=True,text=True,check=True,timeout=30)
    return result.stdout.strip()

def call(client,origin,method,path,body=None):
    data=None if body is None else json.dumps(body).encode()
    request=urllib.request.Request(origin+path,data=data,method=method,headers={'Origin':origin,'Content-Type':'application/json'})
    try:
        with client.open(request,timeout=20) as response:return response.status,response.read()
    except urllib.error.HTTPError as error:return error.code,error.read()

def event(client,origin,name):
    event_id=str(uuid.uuid4());event_ids.append(event_id)
    return call(client,origin,'POST','/api/v1/analytics/events',{'event_id':event_id,'name':name,'occurred_at':datetime.now(timezone.utc).isoformat(),'fields':{'offer_code':'team-monthly-v2'}})[0]

try:
    for index in range(5):
        policy=http.cookiejar.DefaultCookiePolicy(strict_ns_domain=http.cookiejar.DefaultCookiePolicy.DomainStrictNonDomain)
        client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar(policy=policy)))
        clients.append(client)
        assert event(client,PUBLIC,'signup_handoff_started')==403,'ingestion without consent was not denied'
        status,_=call(client,PUBLIC,'PUT','/api/v1/privacy/consent',{'analytics':True,'marketing':False})
        assert status==200,('public consent',status)
        assert event(client,PUBLIC,'signup_handoff_started')==204,'public ingestion failed'
        status,_=call(client,APP,'PUT','/api/v1/privacy/consent',{'analytics':True,'marketing':False})
        assert status==200,('private consent',status)
        assert event(client,APP,'registration_started')==204,'private ingestion failed'
    end=(datetime.now(timezone.utc)+timedelta(seconds=1)).isoformat()
    rows=json.loads(sql("SET ROLE spyglass_analytics_reporter; SELECT coalesce(json_agg(r),'[]'::json) FROM spyglass_analytics_funnel_report('"+start+"','"+end+"','day','none',5) r;"))
    for surface in ['public','private','conversion']:
        matching=[row for row in rows if row['surface']==surface and row['event_name'] in ['signup_handoff_started','registration_started']]
        assert matching and sum(row['event_count'] for row in matching)>=5,('missing report surface',surface,rows)
    print(json.dumps({'consent_denial':'passed','public_private_ingestion':'passed','cohort_report_via_restricted_role':rows}))
    for client in clients:
        for origin,name in [(PUBLIC,'signup_handoff_started'),(APP,'registration_started')]:
            assert call(client,origin,'PUT','/api/v1/privacy/consent',{'analytics':False,'marketing':False})[0]==200
            assert event(client,origin,name)==403,'withdrawn consent still accepted events'
    print('withdrawal_blocks_ingestion: passed')
finally:
    failures=[]
    for index,client in enumerate(clients):
        for origin in [APP,PUBLIC]:
            try:
                status,_=call(client,origin,'DELETE','/api/v1/privacy/data')
                if status!=204: failures.append([index,origin,status])
            except Exception as error: failures.append([index,origin,type(error).__name__])
    print('synthetic_subject_cleanup:', 'passed' if not failures else failures)
    if event_ids:
        remaining=sql("SELECT count(*) FROM analytics_events WHERE event_id IN ("+','.join("'"+value+"'::uuid" for value in event_ids)+");")
        print('remaining_synthetic_raw_events:',remaining)
        assert remaining=='0'
        remaining_conversion=sql("SELECT count(*) FROM analytics_conversion_events WHERE receipt_event_id IN ("+','.join("'"+value+"'::uuid" for value in event_ids)+");")
        print('remaining_synthetic_conversion_events:',remaining_conversion)
        assert remaining_conversion=='0'
    assert not failures
