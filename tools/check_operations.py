"""Batch 5 management API acceptance against dedicated test project."""
import csv
import datetime as dt
import io
import secrets
import time
from check_contests import Client,sql,env,base_url

admin=Client();admin.call('/api/auth/login','POST',{'username':'admin','password':env['BIUE_SMOKE_PASSWORD']})
stamp=secrets.token_hex(4);password=secrets.token_urlsafe(20);clients=[];users=[]
for i in range(3):
    c=Client();name=f'b5_{stamp}_{i}';c.call('/api/auth/register','POST',{'username':name,'password':password,'password_confirmation':password},201);c.call('/api/auth/login','POST',{'username':name,'password':password});clients.append(c);users.append(c.call('/api/users/me'))
one,two,outsider=clients
outsider.call('/api/admin/operations/metrics',expected=403)
rows=admin.call('/api/admin/operations/users?q='+users[0]['username']);assert len(rows)==1 and 'password_hash' not in rows[0]
admin.call(f"/api/admin/operations/users/{users[2]['id']}/status",'POST',{'status':'disabled','reason':'acceptance disable'})
outsider.call('/api/users/me',expected=401)
admin.call(f"/api/admin/operations/users/{users[2]['id']}/status",'POST',{'status':'active','reason':'acceptance restore'})
outsider.call('/api/users/me',expected=401)
outsider.call('/api/auth/login','POST',{'username':users[2]['username'],'password':password})
admin_id=admin.call('/api/users/me')['id'];admin.call(f'/api/admin/operations/users/{admin_id}/status','POST',{'status':'disabled','reason':'invalid self disable'},400)
now=dt.datetime.now(dt.timezone.utc)
cid=admin.call('/api/contests','POST',{'title':'Operations '+stamp,'scoring_mode':'OI','enrollment_policy':'admin_add','team_mode':False,'approval_required':False,'starts_at':(now+dt.timedelta(hours=1)).isoformat(),'ends_at':(now+dt.timedelta(hours=2)).isoformat()})['id']
path=f'/api/contests/{cid}'
raw='username,participant_type\n'+users[0]['username']+',official\n'+users[1]['username']+',official\n'
outsider.call(path+'/csv/preview','POST',{'csv':raw},403)
bad=admin.call(path+'/csv/preview','POST',{'csv':'username,student_no\n'+users[0]['username']+',wrong\n'});assert not bad['valid'] and bad['rows'][0]['error']
admin.call(path+'/csv/confirm','POST',{'token':bad['token']},409)
preview=admin.call(path+'/csv/preview','POST',{'csv':raw});assert preview['valid']
admin.call(path+'/csv/confirm','POST',{'token':preview['token']});admin.call(path+'/csv/confirm','POST',{'token':preview['token']})
assert sql(f'SELECT COUNT(*) FROM contest_team_members WHERE contest_id={cid}')=='2'
pending=admin.call(path+'/csv/preview','POST',{'csv':'username\n'+users[2]['username']+'\n'});assert pending['valid']
sql(f"UPDATE persons p JOIN person_accounts pa ON pa.person_id=p.id SET p.revision=p.revision+1 WHERE pa.user_id={users[2]['id']}")
admin.call(path+'/csv/confirm','POST',{'token':pending['token']},409)
pid=int(sql("SELECT p.id FROM problems p JOIN testcases t ON t.problem_id=p.id AND t.data_version=p.data_version WHERE p.status='published' AND p.visibility='public' GROUP BY p.id HAVING COUNT(*)=1 ORDER BY p.id LIMIT 1"))
admin.call(path+'/manage','POST',{'action':'add_problem','problem_id':pid})
sql(f'UPDATE contests SET starts_at=UTC_TIMESTAMP(6)-INTERVAL 1 MINUTE,ends_at=UTC_TIMESTAMP(6)+INTERVAL 1 HOUR WHERE id={cid}')
admin.call(path+'/manage','POST',{'action':'freeze'});admin.call(path+'/manage','POST',{'action':'freeze'})
assert sql(f'SELECT COUNT(*) FROM contest_freezes WHERE contest_id={cid}')=='1'
sid=one.call('/api/submissions','POST',{'contest_id':cid,'problem_id':pid,'language':'python3','source_code':'print(input())'},201)['id']
deadline=time.monotonic()+90
while time.monotonic()<deadline:
    r=one.call(f'/api/submissions/{sid}')
    if r['status']=='Accepted':break
    assert r['status'] in ('Pending','Queued','Compiling','Running'),r['status'];time.sleep(.3)
else:raise AssertionError('judging timeout')
public=one.call(path);live=admin.call(path);assert public['frozen'] and live['frozen'];assert max(r['score'] for r in public['standings'])==0;assert max(r['score'] for r in live['standings'])==100
admin.call(path+'/manage','POST',{'action':'unfreeze'});assert max(r['score'] for r in one.call(path)['standings'])==100
teams=[t['id'] for t in live['teams']]
admin.call(path+'/manage','POST',{'action':'bulk_status','team_ids':teams,'status':'active','participant_type':'star'});assert all(r['rank']==0 for r in one.call(path)['standings'])
admin.call(path+'/manage','POST',{'action':'bulk_status','team_ids':teams,'status':'active','participant_type':'official'})
with admin.opener.open(base_url+path+'/export.csv') as r:
    exported=list(csv.reader(io.StringIO(r.read().decode())));assert exported[0][0]=='rank';assert any(users[0]['username'] in row for row in exported)
admin.call(path+'/manage','POST',{'action':'freeze'});admin.call(path+'/manage','POST',{'action':'unfreeze'});assert sql(f'SELECT COUNT(*) FROM contest_freezes WHERE contest_id={cid}')=='2'
sql(f'UPDATE contests SET ends_at=UTC_TIMESTAMP(6)-INTERVAL 1 MICROSECOND WHERE id={cid}');admin.call(path+'/settle','POST',{})
admin.call(path+'/manage','POST',{'action':'grant_admin','user_id':users[2]['id']});assert outsider.call(path)['manager']
outsider.call(path+'/manage','POST',{'action':'grant_admin','user_id':users[0]['id']},403)
admin.call(path+'/manage','POST',{'action':'revoke_admin','user_id':users[2]['id']});assert not outsider.call(path)['manager'];outsider.call(f'/api/submissions/{sid}',expected=404)
metrics=admin.call('/api/admin/operations/metrics');assert metrics['redis_ready'] and metrics['worker_ready'] and metrics['runner_via_worker_ready']
admin.call('/api/admin/operations/audits');admin.call('/api/admin/operations/dead-letters');admin.call('/api/admin/operations/quarantine')
assert any(r['id']==sid for r in admin.call(f"/api/admin/operations/users/{users[0]['id']}/submissions"))
version,dirty=sql('SELECT CONCAT(version,":",dirty) FROM schema_migrations').split(':')
assert int(version)>=8 and dirty=='0'
print('Schema >=8; user disable/revoke/restore; scoped CSV preview/conflict/idempotency; live sandbox while frozen; retained freeze history; bulk status/export; metrics/audit/queue passed')
