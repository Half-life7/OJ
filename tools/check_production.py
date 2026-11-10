"""Create and verify real production-stack fixtures before/after restore."""
import argparse
import datetime as dt
import http.cookiejar
import io
import json
import secrets
import subprocess
import time
import urllib.request
import urllib.error
import zipfile
import ssl
from pathlib import Path

p=argparse.ArgumentParser();p.add_argument('--env-file',required=True);p.add_argument('--project',required=True);p.add_argument('--stage',choices=['create','update','restore'],required=True);p.add_argument('--state',default='.cache/5-production-state.json');p.add_argument('--ca-file');a=p.parse_args()
assert a.project.startswith('biue-deploy5-')
env=dict(line.split('=',1) for line in Path(a.env_file).read_text().splitlines() if '=' in line and not line.startswith('#'));base=env['PUBLIC_URL'];handlers=[urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar())]
if a.ca_file:handlers.append(urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=a.ca_file)))
opener=urllib.request.build_opener(*handlers)
def call(path,method='GET',data=None,status=200,ctype=None):
    headers={'Accept':'application/json'}
    if method!='GET':headers.update({'X-CSRF-Token':call('/api/auth/csrf')['csrf_token'],'Content-Type':ctype or 'application/json','Origin':base.rstrip('/'),'Referer':base.rstrip('/')+'/'})
    raw=data if isinstance(data,bytes) else None if data is None else json.dumps(data).encode()
    try:r=opener.open(urllib.request.Request(base+path,data=raw,headers=headers,method=method),timeout=30)
    except urllib.error.HTTPError as e:r=e
    with r:
        body=json.loads(r.read());assert r.status==status,(path,r.status,body.get('message'));return body.get('data')
def sql(query):
    shell='MYSQL_PWD="$MYSQL_PASSWORD" exec mysql --user="$MYSQL_USER" --database="$MYSQL_DATABASE" --batch --skip-column-names --execute="$1"'
    r=subprocess.run(['docker','exec',a.project+'-mysql-1','sh','-c',shell,'sh',query],capture_output=True,text=True,timeout=30);assert r.returncode==0,'fixture SQL failed';return r.stdout.strip()
def inspect(service,template):
    r=subprocess.run(['docker','inspect','--format',template,a.project+'-'+service+'-1'],capture_output=True,text=True,timeout=20);assert r.returncode==0,'container inspection failed';return r.stdout.strip()
for service in ('web','worker','runner'):
    mounts=json.loads(inspect(service,'{{json .Mounts}}'));has_socket=any(m['Destination']=='/var/run/docker.sock' for m in mounts);assert has_socket==(service=='runner')
    assert inspect(service,'{{.Config.User}}')=='10001:10001'
for service in ('web','worker','runner','mysql','redis','nginx','dispatcher'):
    assert inspect(service,'{{.HostConfig.RestartPolicy.Name}}')=='unless-stopped'
assert all(name.endswith('_judge') for name in json.loads(inspect('runner','{{json .NetworkSettings.Networks}}')))
def submit(problem,language,source,contest=None):
    body={'problem_id':problem,'language':language,'source_code':source}
    if contest:body['contest_id']=contest
    sid=call('/api/submissions','POST',body,201)['id'];deadline=time.monotonic()+90
    while time.monotonic()<deadline:
        r=call(f'/api/submissions/{sid}')
        if r['status']=='Accepted':return sid
        assert r['status'] in ('Pending','Queued','Compiling','Running'),(language,r['status']);time.sleep(.5)
    raise AssertionError('production judge timeout')
call('/api/auth/login','POST',{'username':'admin','password':env['BIUE_SMOKE_PASSWORD']})
if base.startswith('https://'):
    session=next(c for c in handlers[0].cookiejar if c.name=='biue_session');assert session.secure and 'HttpOnly' in session._rest
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self,*args,**kwargs):return None
    plain='http://127.0.0.1:'+env['PUBLIC_HTTP_PORT']+'/login'
    try:urllib.request.build_opener(NoRedirect()).open(plain,timeout=15)
    except urllib.error.HTTPError as e:assert e.code==301 and e.headers['Location']==base.rstrip('/')+'/login'
    else:raise AssertionError('HTTP ingress did not redirect to HTTPS')
if a.stage=='create':
    stamp=secrets.token_hex(4)
    problem=call('/api/admin/problems','POST',{'title':'Production '+stamp,'description':'Sum integers','input_format':'Two integers','output_format':'Sum','sample_input':'2 3\n','sample_output':'5\n','time_limit_ms':3000,'memory_limit_mb':512,'difficulty':'easy','tags':[],'status':'published','visibility':'public'},201)
    archive=io.BytesIO()
    with zipfile.ZipFile(archive,'w') as z:
        for name,inp,out in [('a','2 3\n','5\n'),('b','4 7\n','11\n')]:z.writestr(name+'.in',inp);z.writestr(name+'.out',out)
    boundary='prod'+stamp;parts=[]
    for key,value in [('revision',str(problem['revision']).encode()),('scores',b'{"a":50,"b":50}'),('archive',archive.getvalue())]:
        filename='; filename="data.zip"' if key=='archive' else '';parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"{filename}\r\n\r\n'.encode()+value+b'\r\n')
    call(f"/api/admin/problems/{problem['id']}/data-versions",'POST',b''.join(parts)+f'--{boundary}--\r\n'.encode(),201,'multipart/form-data; boundary='+boundary)
    sources={'python3':'print(sum(map(int,input().split())))','cpp17':'#include <iostream>\nint main(){long a,b;std::cin>>a>>b;std::cout<<a+b<<"\\n";}','go':'package main\nimport("fmt")\nfunc main(){var a,b int;fmt.Scan(&a,&b);fmt.Println(a+b)}','java':'import java.util.*; public class Main {public static void main(String[] x){Scanner s=new Scanner(System.in);System.out.println(s.nextInt()+s.nextInt());}}'}
    ids=[submit(problem['id'],lang,source) for lang,source in sources.items()]
    user=call('/api/users/me');contests=[]
    for mode in ('OI','ACM','IOI'):
        now=dt.datetime.now(dt.timezone.utc);cid=call('/api/contests','POST',{'title':'Production '+mode,'scoring_mode':mode,'enrollment_policy':'admin_add','approval_required':False,'team_mode':False,'starts_at':(now+dt.timedelta(hours=1)).isoformat(),'ends_at':(now+dt.timedelta(hours=2)).isoformat()})['id']
        for body in ({'action':'add_problem','problem_id':problem['id']},{'action':'add_team','name':'Production admin','members':[user['id']],'participant_type':'official'}):call(f'/api/contests/{cid}/manage','POST',body)
        sql(f'UPDATE contests SET starts_at=UTC_TIMESTAMP(6)-INTERVAL 1 MINUTE,ends_at=UTC_TIMESTAMP(6)+INTERVAL 1 HOUR WHERE id={cid}');ids.append(submit(problem['id'],'python3',sources['python3'],cid));sql(f'UPDATE contests SET ends_at=UTC_TIMESTAMP(6)-INTERVAL 1 MICROSECOND WHERE id={cid}');result=call(f'/api/contests/{cid}/settle','POST',{});assert result[0]['score']==100 and result[0]['rank']==1;contests.append(cid)
    state={'problem':problem['id'],'submissions':ids,'contests':contests};Path(a.state).write_text(json.dumps(state));print('Production TLS Runner: four languages and OI/ACM/IOI real judging/settlement passed')
else:
    state=json.loads(Path(a.state).read_text())
    for sid in state['submissions']:assert call(f'/api/submissions/{sid}')['status']=='Accepted'
    assert call('/api/leaderboards/contest')['items'][0]['value']==300
    if a.stage=='update':
        state['after_backup_document']=call('/api/community','POST',{'kind':'list','title':'after backup marker','body':'must disappear after restore','visibility':'private','problems':[]})['id'];Path(a.state).write_text(json.dumps(state));print('Update/restart preserved submissions, files and settled contest points')
    else:
        call(f"/api/community/{state['after_backup_document']}",status=404)
        submit(state['problem'],'python3','print(sum(map(int,input().split())))')
        print('Restore recovered original records and credentials, removed later state, and completed a new real judge job')
