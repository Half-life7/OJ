"""Real Docker judging and concurrent contest operations; no result injection.

Use only the dedicated biue-batch3 project, after fixture tests, with worker,
dispatcher and runner running. Existing records and volumes are preserved.
"""
import concurrent.futures
import datetime as dt
import secrets
import time
from check_contests import Client, sql, env

admin=Client()
admin.call('/api/auth/login','POST',{'username':'admin','password':env['BIUE_SMOKE_PASSWORD']})
stamp=secrets.token_hex(5)
pid=int(sql('SELECT problem_id FROM testcases GROUP BY problem_id HAVING COUNT(*)=2 ORDER BY problem_id LIMIT 1'))
now=dt.datetime.now(dt.timezone.utc)
cid=admin.call('/api/contests','POST',{'title':'Runtime '+stamp,'scoring_mode':'IOI','enrollment_policy':'admin_add','team_mode':False,'approval_required':False,'starts_at':(now+dt.timedelta(hours=1)).isoformat(),'ends_at':(now+dt.timedelta(hours=2)).isoformat()})['id']
admin.call(f'/api/contests/{cid}/manage','POST',{'action':'add_problem','problem_id':pid})
username='runtime_'+stamp;password=secrets.token_urlsafe(20)
uid=admin.call(f'/api/contests/{cid}/temporary-accounts','POST',{'username':username,'password':password,'display_name':'Runtime entrant'})['user_id']
entrant=Client();entrant.call('/api/auth/login','POST',{'username':username,'password':password})
sql(f'UPDATE contests SET starts_at=UTC_TIMESTAMP(6)-INTERVAL 1 MINUTE,ends_at=UTC_TIMESTAMP(6)+INTERVAL 1 HOUR WHERE id={cid}')
body={'contest_id':cid,'problem_id':pid,'language':'python3','source_code':'print(input())'}
sid=entrant.call('/api/submissions','POST',body,201)['id']
def wait_result(submission):
    deadline=time.monotonic()+90
    while time.monotonic()<deadline:
        result=entrant.call(f'/api/submissions/{submission}')
        if result['status'] not in ('Pending','Queued','Compiling','Running'):
            assert result['status']=='Accepted' and result['score']==100,result['status']
            return
        time.sleep(.3)
    raise AssertionError('real judging timeout')
wait_result(sid)
view=admin.call(f'/api/contests/{cid}')
team=next(m['team_id'] for m in view['members'] if m['user_id']==uid)
row=next(r for r in view['standings'] if r['team_id']==team)
assert row['score']==100 and row['solved']==1
def withdraw():
    admin.call(f'/api/contests/{cid}/manage','POST',{'action':'team_status','team_id':team,'status':'withdrawn','participant_type':'official'})
def racing_submit():
    try:
        return entrant.call('/api/submissions','POST',body,201)['id']
    except AssertionError as e:
        # Withdrawal may win the admission lock; either result must be safe.
        assert e.args[0][1]==404,e
        return None
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    a,b=pool.submit(withdraw),pool.submit(racing_submit)
    a.result();raced=b.result()
entrant.call('/api/submissions','POST',body,404)
if raced is not None:wait_result(raced)
assert next(r for r in admin.call(f'/api/contests/{cid}')['standings'] if r['team_id']==team)['rank']==0
admin.call(f'/api/contests/{cid}/manage','POST',{'action':'team_status','team_id':team,'status':'active','participant_type':'official'})
sql(f'UPDATE contests SET ends_at=UTC_TIMESTAMP(6)-INTERVAL 1 MICROSECOND WHERE id={cid}')
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
    futures=[pool.submit(admin.call,f'/api/contests/{cid}/settle','POST',{}) for _ in range(4)]
    results=[f.result() for f in futures]
assert all(r==results[0] for r in results)
assert sql(f'SELECT COUNT(*) FROM contest_snapshots WHERE contest_id={cid}')=='1'
assert sql(f'SELECT COUNT(*) FROM contest_points WHERE contest_id={cid}')=='1'
assert sql(f'SELECT points FROM contest_points WHERE contest_id={cid}')=='100'
assert sql(f'SELECT COUNT(*) FROM submission_case_results WHERE submission_id={sid}')=='2'
assert sql(f"SELECT COUNT(*) FROM audit_logs WHERE object_type='contest' AND object_id={cid} AND action='contest.settle' AND request_id<>''")=='1'
print('Real Python sandbox -> persisted cases -> IOI standings -> settlement passed')
print('Concurrent withdrawal/submission and four concurrent settlements passed; one snapshot and points entry')
