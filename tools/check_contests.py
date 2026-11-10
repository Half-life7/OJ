"""Batch 3 API/SQL acceptance in an isolated Compose project.

Creates test records; never resets a database. Scores are injected as explicit
database fixtures to test reconstruction independently of sandbox execution.
Requires an initialized admin and the batch-3 web build.
"""
import argparse
import concurrent.futures
import datetime as dt
import http.cookiejar
import io
import json
import secrets
import subprocess
import urllib.error
import urllib.request
import zipfile
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--env-file', default='.cache/2d-acceptance.env')
parser.add_argument('--project', default='biue-batch3')
args = parser.parse_args()
assert args.project.startswith('biue-batch3'), 'Use a dedicated batch-3 project'
env = dict(line.split('=', 1) for line in Path(args.env_file).read_text().splitlines() if '=' in line and not line.startswith('#'))
base_url = env['PUBLIC_URL'].rstrip('/').replace('://localhost:', '://127.0.0.1:')
compose = ['docker', 'compose', '--env-file', args.env_file, '-p', args.project, '-f', 'deploy/docker-compose.yml', '-f', 'tools/contest-test.compose.yml']

def sql(query):
    shell = 'MYSQL_PWD="$MYSQL_PASSWORD" exec mysql --user="$MYSQL_USER" --database="$MYSQL_DATABASE" --batch --skip-column-names'
    p = subprocess.run(compose + ['exec', '-T', 'mysql', 'sh', '-c', shell], input=query, capture_output=True, text=True, timeout=30)
    assert p.returncode == 0, 'SQL fixture operation failed: ' + p.stderr[:300]
    return p.stdout.strip()

class Client:
    def __init__(self):
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    def call(self, path, method='GET', body=None, expected=200, content_type=None):
        headers = {'Accept': 'application/json'}
        if method != 'GET':
            headers['X-CSRF-Token'] = self.call('/api/auth/csrf')['csrf_token']
            headers['Content-Type'] = content_type or 'application/json'
        data = body if isinstance(body, bytes) else None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(base_url + path, data=data, headers=headers, method=method)
        try:
            response = self.opener.open(request, timeout=25)
        except urllib.error.HTTPError as e:
            response = e
        with response:
            result = json.loads(response.read())
            assert response.status == expected, (path, response.status, expected, result.get('message'))
        return result.get('data')

def main():
    stamp = secrets.token_hex(4)
    password = secrets.token_urlsafe(20)
    admin = Client()
    admin.call('/api/auth/login', 'POST', {'username': 'admin', 'password': env['BIUE_SMOKE_PASSWORD']})
    users = []
    for i in range(4):
        c = Client()
        username = f'b3_{stamp}_{i}'
        c.call('/api/auth/register', 'POST', {'username': username, 'password': password, 'password_confirmation': password}, 201)
        c.call('/api/auth/login', 'POST', {'username': username, 'password': password})
        users.append((c, c.call('/api/users/me')))
    one, two, manager, outsider = [x[0] for x in users]
    uid = [x[1]['id'] for x in users]
    person = int(sql(f'SELECT person_id FROM person_accounts WHERE user_id={uid[0]}'))
    change = {'revision': 1, 'display_name': 'Batch Three', 'student_no': 'T' + stamp, 'student_no_status': 'verified', 'organization': 'School', 'reason': 'acceptance'}
    admin.call(f'/api/admin/persons/{person}', 'PUT', change)
    admin.call(f'/api/admin/persons/{person}', 'PUT', change, 409)
    outsider.call(f'/api/admin/persons/{person}', 'PUT', change, 403)
    assert sql(f'SELECT COUNT(*) FROM student_no_history WHERE person_id={person}') == '1'

    p = admin.call('/api/admin/problems', 'POST', {'title': 'Batch 3 ' + stamp, 'description': 'Read a number; print it.', 'input_format': 'Integer', 'output_format': 'Same integer', 'sample_input': '1\n', 'sample_output': '1\n', 'time_limit_ms': 1000, 'memory_limit_mb': 128, 'difficulty': 'easy', 'tags': [], 'status': 'published', 'visibility': 'contest_only'}, 201)
    pid = p['id']
    archive = io.BytesIO()
    with zipfile.ZipFile(archive, 'w') as z:
        for case in ('a', 'b'):
            z.writestr(case + '.in', '1\n')
            z.writestr(case + '.out', '1\n')
    boundary = 'batch3' + stamp
    parts = []
    for key, value in [('revision', str(p['revision']).encode()), ('scores', b'{"a":30,"b":70}'), ('archive', archive.getvalue())]:
        filename = '; filename="data.zip"' if key == 'archive' else ''
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"{filename}\r\n\r\n'.encode() + value + b'\r\n')
    body = b''.join(parts) + f'--{boundary}--\r\n'.encode()
    admin.call(f'/api/admin/problems/{pid}/data-versions', 'POST', body, 201, 'multipart/form-data; boundary=' + boundary)

    def make(mode, team=False, policy='open'):
        now = dt.datetime.now(dt.timezone.utc)
        return admin.call('/api/contests', 'POST', {'title': mode + ' ' + stamp, 'scoring_mode': mode, 'enrollment_policy': policy, 'team_mode': team, 'approval_required': False, 'starts_at': (now + dt.timedelta(hours=1)).isoformat(), 'ends_at': (now + dt.timedelta(hours=2)).isoformat()})['id']
    def manage(cid, **data):
        return admin.call(f'/api/contests/{cid}/manage', 'POST', data)
    def start(cid):
        sql(f'UPDATE contests SET starts_at=UTC_TIMESTAMP(6)-INTERVAL 1 HOUR,ends_at=UTC_TIMESTAMP(6)+INTERVAL 1 HOUR WHERE id={cid}')
    def finish(cid):
        sql(f'UPDATE contests SET ends_at=UTC_TIMESTAMP(6)-INTERVAL 1 MICROSECOND WHERE id={cid}')
    def submit(c,cid):
        return c.call('/api/submissions', 'POST', {'contest_id': cid, 'problem_id': pid, 'language': 'python3', 'source_code': 'print(input())'}, 201)['id']
    def result(sid, a, b):
        status = 'Accepted' if a + b == 100 else 'WrongAnswer'
        pa = int(a == 30) + int(b == 70)
        sql(f"START TRANSACTION; UPDATE submissions SET status='{status}',score={a+b},passed_cases={pa} WHERE id={sid}; UPDATE judge_tasks SET status='{status}' WHERE submission_id={sid}; INSERT INTO submission_results(submission_id,generation,attempt,status,score,execution_time_ms,memory_usage_kb,passed_cases,total_cases,output,error_message,created_at) VALUES ({sid},1,1,'{status}',{a+b},1,1,{pa},2,'','',UTC_TIMESTAMP(6)); INSERT INTO submission_case_results VALUES ({sid},1,'a','{'Accepted' if a else 'WrongAnswer'}',{a},1,1),({sid},1,'b','{'Accepted' if b else 'WrongAnswer'}',{b},1,1); COMMIT;")

    records = []
    for mode in ('OI', 'IOI', 'ACM'):
        cid = make(mode)
        records.append(cid)
        manage(cid, action='add_problem', problem_id=pid)
        manage(cid, action='grant_admin', user_id=uid[2])
        manager.call(f'/api/contests/{cid}/manage', 'POST', {'action': 'grant_admin', 'user_id': uid[3]}, 403)
        outsider.call(f'/api/contests/{cid}/manage', 'POST', {'action': 'remove_problem', 'problem_id': pid}, 403)
        one.call(f'/api/contests/{cid}/enroll', 'POST', {})
        one.call(f'/api/contests/{cid}/enroll', 'POST', {}, 409)
        two.call(f'/api/contests/{cid}/enroll', 'POST', {})
        v = one.call(f'/api/contests/{cid}')
        assert not v['problems'], 'pre-start statements leaked'
        team = next(m['team_id'] for m in v['members'] if m['user_id'] == uid[0])
        start(cid)
        manage(cid, action='team_status', team_id=team, status='withdrawn', participant_type='official')
        one.call('/api/submissions', 'POST', {'contest_id': cid, 'problem_id': pid, 'language': 'python3', 'source_code': 'print(1)'}, 404)
        manage(cid, action='team_status', team_id=team, status='active', participant_type='official')
        s1, s2 = submit(one,cid), submit(one,cid)
        s3 = submit(two,cid)
        outsider.call(f'/api/submissions/{s1}', expected=404)
        manager.call(f'/api/submissions/{s1}')
        result(s2,0,70)
        result(s1,30,0)
        finish(cid)
        admin.call(f'/api/contests/{cid}/settle', 'POST', {}, 409)
        result(s3,30,70)
        if mode == 'ACM':
            # Team one has no AC: retain wrong attempts, exclude their penalty.
            expected_score = 0
        else:
            expected_score = 70 if mode == 'OI' else 100
        v = admin.call(f'/api/contests/{cid}')
        row = next(r for r in v['standings'] if r['team_id'] == team)
        assert row['score'] == expected_score, (mode,row)
        if mode == 'ACM':
            assert row['penalty'] == 0 and row['problems'][str(pid)]['wrong'] == 2
        first = admin.call(f'/api/contests/{cid}/settle', 'POST', {})
        second = admin.call(f'/api/contests/{cid}/settle', 'POST', {})
        assert first == second
        assert sql(f'SELECT COUNT(*) FROM contest_points WHERE contest_id={cid}') == '2'
        assert sql(f'SELECT COUNT(*) FROM contest_snapshots WHERE contest_id={cid}') == '1'
        print(mode + ': authorization, pinned submissions, withdrawal, out-of-order scoring, idempotent settlement passed', flush=True)

    cid = make('OI', team=True, policy='admin_add')
    manage(cid, action='add_team', name='Team A', members=uid[:2], participant_type='official')
    one.call(f'/api/contests/{cid}/enroll', 'POST', {}, 403)
    admin.call(f'/api/contests/{cid}/manage', 'POST', {'action':'add_team','name':'Duplicate','members':[uid[0]],'participant_type':'official'},409)
    temp_name='tmp_'+stamp
    admin.call(f'/api/contests/{cid}/temporary-accounts','POST',{'username':temp_name,'password':password,'display_name':'Temporary'})
    temp=Client();temp.call('/api/auth/login','POST',{'username':temp_name,'password':password})
    temp.call('/api/submissions','POST',{'problem_id':pid,'language':'python3','source_code':'print(1)'},403)
    old_person = int(sql(f'SELECT person_id FROM person_accounts WHERE user_id={uid[1]}'))
    admin.call('/api/admin/identity-links','POST',{'user_id':uid[1],'from_person_id':old_person,'to_person_id':person,'reason':'explicit acceptance link'})
    assert sql(f'SELECT person_id FROM contest_team_members WHERE contest_id={cid} AND user_id={uid[1]}') == str(old_person)
    start(cid)
    finish(cid)
    admin.call(f'/api/contests/{cid}/settle','POST',{})
    assert sql(f'SELECT COUNT(*) FROM contest_points WHERE contest_id={cid} AND person_id={person}') == '1'
    temp_id = temp.call('/api/users/me')['id']
    manage(cid,action='retire_account',user_id=temp_id)
    temp.call('/api/users/me',expected=401)
    print('identity audit/conflict, team uniqueness, temporary login and practice isolation passed', flush=True)
    assert sql('SELECT CONCAT(version, ":", dirty) FROM schema_migrations') == '6:0'
    print('schema 6 clean; SQL scoring fixtures were used (not sandbox execution)', flush=True)

if __name__ == "__main__":
    main()
