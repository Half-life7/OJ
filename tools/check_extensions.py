"""Batch 6: real Node/SPJ/output-only, SSE, rejudge and period-board acceptance."""
import datetime as dt
import io
import json
import secrets
import time
import urllib.error
import zipfile
from check_contests import Client,sql,env,base_url

admin=Client();anon=Client();one=Client();other=Client();judge_user=Client();stamp=secrets.token_hex(4);password=secrets.token_urlsafe(20)
admin.call('/api/auth/login','POST',{'username':'admin','password':env['BIUE_SMOKE_PASSWORD']})
for i,c in enumerate((one,other,judge_user)):
    name=f'b6_{stamp}_{i}';c.call('/api/auth/register','POST',{'username':name,'password':password,'password_confirmation':password},201);c.call('/api/auth/login','POST',{'username':name,'password':password})
user=one.call('/api/users/me')
judge_name=judge_user.call('/api/users/me')['username']
def make(kind='programming',mode='tokens',checker='',floating=False,empty=False):
    p=admin.call('/api/admin/problems','POST',{'title':'Extension '+stamp,'description':'Case outputs','input_format':'Numbers','output_format':'Answer','sample_input':'2 3\n','sample_output':'5\n','time_limit_ms':3000,'memory_limit_mb':256,'difficulty':'easy','tags':[],'status':'published','visibility':'public','problem_type':kind,'checker_mode':mode,'checker_source':checker},201)
    archive=io.BytesIO()
    with zipfile.ZipFile(archive,'w') as z:
        for name,inp,out in [('a','2 3\n','5\n'),('b','4 7\n','11\n')]:z.writestr(name+'.in','' if empty else inp);z.writestr(name+'.out','' if empty else '0.333333\n' if floating else out)
    boundary='ext'+stamp;parts=[]
    for key,value in [('revision',str(p['revision']).encode()),('scores',b'{"a":50,"b":50}'),('archive',archive.getvalue())]:
        filename='; filename="data.zip"' if key=='archive' else '';parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"{filename}\r\n\r\n'.encode()+value+b'\r\n')
    admin.call(f"/api/admin/problems/{p['id']}/data-versions",'POST',b''.join(parts)+f'--{boundary}--\r\n'.encode(),201,'multipart/form-data; boundary='+boundary)
    return p['id']
def submit(c,pid,lang,source,contest=None,expected=201):
    body={'problem_id':pid,'language':lang,'source_code':source}
    if contest:body['contest_id']=contest
    r=c.call('/api/submissions','POST',body,expected);return r['id'] if expected==201 else None
def wait(c,sid,status='Accepted'):
    deadline=time.monotonic()+100
    while time.monotonic()<deadline:
        r=c.call(f'/api/submissions/{sid}')
        if r['status'] not in ('Pending','Queued','Compiling','Running'):
            assert r['status']==status,(sid,r['status'],status);return r
        time.sleep(.3)
    raise AssertionError('judge timeout')
standard=make();js=submit(judge_user,standard,'javascript',"const f=require('fs');console.log(f.readFileSync(0,'utf8').trim().split(/\\s+/).map(Number).reduce((a,b)=>a+b,0));");wait(admin,js)
run=judge_user.call('/api/runs','POST',{'problem_id':standard,'language':'javascript','source_code':"const f=require('fs');console.log(f.readFileSync(0,'utf8').trim().split(/\\s+/).map(Number).reduce((a,b)=>a+b,0));"},201)['id'];ran=wait(admin,run);assert ran['kind']=='Run' and ran['score']==0 and ran['output'].strip()=='5'
secret='CHECKER_PRIVATE_'+stamp
checker=f'''# {secret}
import sys,json,base64
d=json.load(sys.stdin)
try:
    expected=float(base64.b64decode(d['expected']))
    output=float(base64.b64decode(d['output']))
    print('AC' if abs(expected-output)<0.001 else 'WA')
except Exception:
    print('WA')
'''
spj=make(mode='spj',checker=checker,floating=True)
assert 'checker_source' not in anon.call(f'/api/problems/{spj}')
sid=submit(judge_user,spj,'python3',"import os\nassert not any('answer' in x for x in os.listdir('/work'))\nprint('0.3334')");wait(admin,sid)
full=admin.call(f'/api/admin/problems/{spj}');editable={k:full[k] for k in ('title','description','input_format','output_format','sample_input','sample_output','time_limit_ms','memory_limit_mb','difficulty','tags','status','visibility','revision','problem_type','checker_mode','checker_source') if k in full};editable['checker_source']="print('WA')";admin.call(f'/api/admin/problems/{spj}','PUT',editable)
new=submit(judge_user,spj,'python3',"print('0.3334')");wait(admin,new,'WrongAnswer')
out=make('output_only');assert anon.call(f'/api/problems/{out}')['output_cases']==['a','b'];oid=submit(one,out,'text','{"a":"5\\n","b":"11\\n"}');wait(one,oid)
submit(one,out,'python3','print(5)',expected=400);submit(one,out,'text','{"unknown":"5"}',expected=400);submit(one,standard,'text','{"a":"5"}',expected=400)
submit(one,out,'text','{"a":null}',expected=400)
ospj=make('output_only','spj',checker,True);osid=submit(judge_user,ospj,'text','{"a":"0.3334","b":"0.3334"}');wait(admin,osid)
empty_checker="import json,sys,base64\nd=json.load(sys.stdin)\nassert all(isinstance(d[k],str) for k in ('input','expected','output'))\nprint('AC' if base64.b64decode(d['expected'])==base64.b64decode(d['output']) else 'WA')"
empty=make('output_only','spj',empty_checker,empty=True);emptyid=submit(judge_user,empty,'text','{"a":"","b":""}');wait(admin,emptyid)
bad=make(mode='spj',checker="print('SECRET_OUTPUT_MUST_NOT_LEAK')");badid=submit(judge_user,bad,'python3','print(5)');failure=wait(admin,badid,'SystemError');assert failure['output']=='' and failure['error_message']=='' and 'SECRET_OUTPUT' not in json.dumps(failure)
# An SSE stream must send only summaries, including pending -> terminal snapshots.
streamid=submit(one,standard,'python3',"import time\ntime.sleep(1)\nprint(sum(map(int,input().split())))")
events=[];received=[]
with one.opener.open(base_url+f'/api/submissions/{streamid}/events',timeout=20) as r:
    assert r.headers['Content-Type'].startswith('text/event-stream') and r.headers['Cache-Control']=='no-store'
    for raw in r:
        line=raw.decode()
        if line.startswith('data: '):
            value=json.loads(line[6:]);assert 'source_code' not in value and 'stdin' not in value and 'output' not in value
            if 'status' in value:events.append(value['status']);received.append(time.monotonic())
assert events and events[-1]=='Accepted',events
assert len(events)>1 and received[-1]-received[0]>.3,'SSE was buffered instead of delivered live'
other.call(f'/api/submissions/{streamid}/events',expected=404)
# Disabling an account terminates an already-open stream on its next check.
revoked=submit(one,standard,'python3',"import time\ntime.sleep(1)\nprint(sum(map(int,input().split())))")
seen=False
with one.opener.open(base_url+f'/api/submissions/{revoked}/events',timeout=20) as r:
    for raw in r:
        line=raw.decode()
        if line.startswith('data: ') and not seen:
            admin.call(f"/api/admin/operations/users/{user['id']}/status",'POST',{'status':'disabled','reason':'SSE revocation check'});seen=True
        if line.startswith('event: unauthorized'):break
    else:raise AssertionError('open SSE did not report revoked permissions')
admin.call(f"/api/admin/operations/users/{user['id']}/status",'POST',{'status':'active','reason':'SSE revocation restore'});one.call('/api/auth/login','POST',{'username':user['username'],'password':password});wait(one,revoked)
admin.call('/api/leaderboards/practice')
request={'key':'rejudge_'+stamp,'reason':'fixed-version reconstruction','items':[{'id':js,'generation':1},{'id':sid,'generation':1}]}
other.call('/api/admin/operations/rejudge','POST',request,403)
tasks=admin.call('/api/admin/operations/rejudge','POST',request);assert len(tasks)==2
assert sql('SELECT COUNT(*) FROM global_board_cache')=='0'
assert admin.call('/api/admin/operations/rejudge','POST',request)==tasks
for item in (js,sid):
    result=wait(admin,item);assert result['generation']==2
    assert sql(f'SELECT COUNT(*) FROM submission_results WHERE submission_id={item}')=='2'
assert admin.call('/api/admin/operations/rejudge','POST',request)==tasks
admin.call('/api/admin/operations/rejudge','POST',dict(request,reason='different request'),409)
admin.call('/api/admin/operations/rejudge','POST',{'key':'rollback_'+stamp,'reason':'atomic conflict','items':[{'id':js,'generation':2},{'id':sid,'generation':999}]},409)
assert admin.call(f'/api/submissions/{js}')['generation']==2
assert sql(f'SELECT COUNT(*) FROM judge_tasks WHERE submission_id={js}')=='2'
assert admin.call(f'/api/submissions/{sid}')['statement_version']==1
# All/week/month are separate caches of exactly the existing three board kinds.
def value(period):
    page=1
    while True:
        result=anon.call('/api/leaderboards/practice?period='+period+'&page='+str(page))
        for row in result['items']:
            if row['username']==judge_name:return row['value']
        if not result['items']:return 0
        page+=1
before={period:value(period) for period in ('all','week','month')}
sql(f'UPDATE submissions SET created_at=UTC_TIMESTAMP(6)-INTERVAL 40 DAY WHERE id={js}; DELETE FROM global_board_cache')
assert value('all')==before['all'] and value('week')==before['week']-1 and value('month')==before['month']-1
anon.call('/api/leaderboards/practice?period=bad',expected=400)
now=dt.datetime.now(dt.timezone.utc);cid=admin.call('/api/contests','POST',{'title':'Extension contest '+stamp,'scoring_mode':'IOI','enrollment_policy':'admin_add','team_mode':False,'approval_required':False,'starts_at':(now+dt.timedelta(hours=1)).isoformat(),'ends_at':(now+dt.timedelta(hours=2)).isoformat()})['id']
for data in ({'action':'add_problem','problem_id':standard},{'action':'add_team','name':'SSE participant','members':[user['id']],'participant_type':'official'},{'action':'grant_admin','user_id':other.call('/api/users/me')['id']}):admin.call(f'/api/contests/{cid}/manage','POST',data)
admin.call(f'/api/contests/{cid}/manage','POST',{'action':'add_problem','problem_id':spj})
assert secret not in json.dumps(other.call(f'/api/contests/{cid}')) and 'checker_source' not in json.dumps(other.call(f'/api/contests/{cid}'))
sql(f'UPDATE contests SET starts_at=UTC_TIMESTAMP(6)-INTERVAL 1 MINUTE,ends_at=UTC_TIMESTAMP(6)+INTERVAL 1 HOUR WHERE id={cid}')
contestid=submit(one,standard,'javascript',"const fs=require('fs');console.log(fs.readFileSync(0,'utf8').trim().split(/\\s+/).map(Number).reduce((a,b)=>a+b,0))",cid);wait(one,contestid)
sql(f'UPDATE contests SET ends_at=UTC_TIMESTAMP(6)-INTERVAL 1 MICROSECOND WHERE id={cid}');admin.call(f'/api/contests/{cid}/settle','POST',{})
admin.call('/api/admin/operations/rejudge','POST',{'key':'settled_'+stamp,'reason':'must reject','items':[{'id':contestid,'generation':1}]},409)
assert sql('SELECT CONCAT(version,":",dirty) FROM schema_migrations')=='9:0'
print('Migration 9; real JavaScript, SPJ AC/WA/redacted failure, output-only; SSE terminal/revocation/privacy; atomic idempotent pinned rejudge/history/cache/settled protection; week/month boards passed')
