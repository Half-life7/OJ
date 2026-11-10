"""Batch 4 acceptance: real sandbox practice results, access controls and boards.
Requires the dedicated biue-batch3 Compose environment at migration 7, with
Worker/Runner running. Creates records and clears only rebuildable board caches.
"""
import io
import json
import secrets
import time
import zipfile
import datetime as dt
from check_contests import Client, sql, env

admin=Client();anon=Client()
admin.call('/api/auth/login','POST',{'username':'admin','password':env['BIUE_SMOKE_PASSWORD']})
stamp=secrets.token_hex(4);password=secrets.token_urlsafe(20)
clients=[];accounts=[]
for i in range(2):
    c=Client();name=f'b4_{stamp}_{i}'
    c.call('/api/auth/register','POST',{'username':name,'password':password,'password_confirmation':password},201)
    c.call('/api/auth/login','POST',{'username':name,'password':password});clients.append(c);accounts.append(c.call('/api/users/me'))
one,two=clients
pids=[]
for i in range(2):
    p=admin.call('/api/admin/problems','POST',{'title':f'Community {stamp} {i}','description':'Echo integer','input_format':'Integer','output_format':'Same','sample_input':'1\n','sample_output':'1\n','time_limit_ms':1000,'memory_limit_mb':128,'difficulty':'easy','tags':['category:graph','echo'],'status':'published','visibility':'public'},201)
    pids.append(p['id']);archive=io.BytesIO()
    with zipfile.ZipFile(archive,'w') as z:
        z.writestr('a.in','1\n');z.writestr('a.out','1\n')
    boundary='b4'+stamp;parts=[]
    for key,value in [('revision',str(p['revision']).encode()),('scores',b'{"a":100}'),('archive',archive.getvalue())]:
        filename='; filename="data.zip"' if key=='archive' else ''
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"{filename}\r\n\r\n'.encode()+value+b'\r\n')
    admin.call(f"/api/admin/problems/{p['id']}/data-versions",'POST',b''.join(parts)+f'--{boundary}--\r\n'.encode(),201,'multipart/form-data; boundary='+boundary)
a,b=pids
solution={'kind':'solution','title':'Solution '+stamp,'body':'# Safe\n\n<script>alert(1)</script>\n\n```python\nprint(input())\n```','visibility':'public','problem_id':b}
two.call('/api/community','POST',solution,403)
def submit(c,pid,kind='Submit'):
    path='/api/submissions' if kind=='Submit' else '/api/runs'
    sid=c.call(path,'POST',{'problem_id':pid,'language':'python3','source_code':'print(input())'},201)['id']
    deadline=time.monotonic()+90
    while time.monotonic()<deadline:
        r=c.call(f'/api/submissions/{sid}')
        if r['status']=='Accepted':return sid
        assert r['status'] in ('Pending','Queued','Compiling','Running'),r['status']
        time.sleep(.3)
    raise AssertionError('sandbox timeout')
submit(one,a);submit(one,a);submit(one,b);submit(one,a,'Run');submit(two,a)
assert one.call(f'/api/practice/problems/{a}')['people']==2
assert one.call(f'/api/practice/problems/{a}')['solved']
assert anon.call(f'/api/practice/problems/{a}')['acceptance_rate']==1
filtered=anon.call('/api/problems?category=graph&tag=echo')['items'];assert {a,b}.issubset({p['id'] for p in filtered})
assert not anon.call('/api/problems?category=missing_'+stamp)['items']
def get(cid,c=one):return c.call(f'/api/community/{cid}')
def action(cid,act,c=one,expected=200):
    d=get(cid,c);return c.call(f'/api/community/{cid}/actions','POST',{'action':act,'revision':d['revision']},expected)
def board(kind):return anon.call('/api/leaderboards/'+kind)['items']
def own_value(kind):return next((r['value'] for r in board(kind) if r['username']==accounts[0]['username']),0)
assert own_value('practice')==2
listing={'kind':'list','title':'List '+stamp,'body':'Ordered practice','visibility':'private','problems':[b,a]}
lid=one.call('/api/community','POST',listing)['id'];anon.call(f'/api/community/{lid}',expected=404);two.call(f'/api/community/{lid}',expected=404)
assert [p['id'] for p in get(lid)['problems']]==[b,a]
listing.update(visibility='public',revision=1);one.call(f'/api/community/{lid}','PUT',listing)
one.call(f'/api/community/{lid}','PUT',listing,409)
action(lid,'favorite',two);action(lid,'favorite',two);assert get(lid)['favorites']==1
assert any(d['id']==lid for d in two.call('/api/community?kind=list&favorites=1')['items'])
action(lid,'unfavorite',two);assert get(lid)['favorites']==0
discussion={'kind':'discussion','title':'Discuss '+stamp,'body':'Hello','visibility':'public','problem_id':a}
did=one.call('/api/community','POST',discussion)['id'];reply=dict(discussion,parent_id=did,title='Reply')
rid=two.call('/api/community','POST',reply)['id'];reply['problem_id']=b;two.call('/api/community','POST',reply,400)
action(did,'like',two);action(did,'like',two);assert get(did)['likes']==1
action(did,'unlike',two);assert get(did)['likes']==0
action(did,'delete',two,403);action(did,'delete',admin);anon.call(f'/api/community/{rid}',expected=404)
cid=admin.call('/api/contests')['items'][0]['id'] if isinstance(admin.call('/api/contests'),dict) else admin.call('/api/contests')[0]['id']
cd=admin.call('/api/community','POST',dict(discussion,problem_id=None,contest_id=cid))['id'];two.call(f'/api/community/{cd}',expected=404)
sid=one.call('/api/community','POST',solution)['id'];anon.call(f'/api/community/{sid}',expected=404)
assert '<script' not in get(sid)['html'] and 'language-python' in get(sid)['html']
two.call(f'/api/community/{sid}/actions','POST',{'action':'approve','revision':get(sid)['revision']},404);action(sid,'approve',admin);anon.call(f'/api/community/{sid}');assert own_value('solutions')==1
sid2=one.call('/api/community','POST',solution)['id'];action(sid2,'approve',admin);assert own_value('solutions')==1
solution.update(visibility='private',revision=get(sid)['revision']);one.call(f'/api/community/{sid}','PUT',solution);anon.call(f'/api/community/{sid}',expected=404)
action(sid2,'delete');assert own_value('solutions')==0
now=dt.datetime.now(dt.timezone.utc)
contests=[]
for label in ('official','excluded'):
    contest=admin.call('/api/contests','POST',{'title':'Board '+label+' '+stamp,'scoring_mode':'OI','enrollment_policy':'admin_add','team_mode':False,'approval_required':False,'starts_at':(now+dt.timedelta(hours=1)).isoformat(),'ends_at':(now+dt.timedelta(hours=2)).isoformat()})['id'];contests.append(contest)
    for index,account in enumerate(accounts):
        admin.call(f'/api/contests/{contest}/manage','POST',{'action':'add_team','name':account['username'],'members':[account['id']],'participant_type':'star' if label=='excluded' and index==1 else 'official'})
    if label=='excluded':
        team=next(m['team_id'] for m in admin.call(f'/api/contests/{contest}')['members'] if m['user_id']==accounts[0]['id'])
        admin.call(f'/api/contests/{contest}/manage','POST',{'action':'team_status','team_id':team,'status':'withdrawn','participant_type':'official'})
    sql(f'UPDATE contests SET starts_at=UTC_TIMESTAMP(6)-INTERVAL 2 HOUR,ends_at=UTC_TIMESTAMP(6)-INTERVAL 1 HOUR WHERE id={contest}')
    admin.call(f'/api/contests/{contest}/settle','POST',{})
assert own_value('contest')==100
assert sql(f'SELECT COUNT(*) FROM contest_points WHERE contest_id={contests[1]}')=='0'
person1=int(sql(f"SELECT person_id FROM person_accounts WHERE user_id={accounts[0]['id']}"));person2=int(sql(f"SELECT person_id FROM person_accounts WHERE user_id={accounts[1]['id']}"))
admin.call('/api/admin/identity-links','POST',{'user_id':accounts[1]['id'],'from_person_id':person2,'to_person_id':person1,'reason':'community identity dedup test'})
assert own_value('practice')==2 and one.call(f'/api/practice/problems/{a}')['people']==1
assert own_value('contest')==100
before={k:board(k) for k in ('practice','contest','solutions')};sql('DELETE FROM global_board_cache');after={k:board(k) for k in before};assert before==after
anon.call('/api/leaderboards/fourth',expected=400)
action(lid,'delete');anon.call(f'/api/community/{lid}',expected=404)
version,dirty=sql('SELECT CONCAT(version,":",dirty) FROM schema_migrations').split(':')
assert int(version)>=7 and dirty=='0'
print('Schema >=7; real daily sandbox AC/Run exclusion; categories/stats; ordered private lists/favorites; discussion reply/context/moderation; sanitized reviewed solutions; three-board dedup/cache reconstruction passed')
