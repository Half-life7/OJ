"""Strict configuration and archive helpers used by deploy.sh (no shell eval)."""
import argparse
import hashlib
import ipaddress
import os
from pathlib import Path
import re
import secrets
import sys
import tarfile
from urllib.parse import urlsplit

class MaintenanceError(ValueError):
    pass

def read_env(path):
    values={}
    for raw in Path(path).read_text().splitlines():
        if not raw or raw.startswith('#'):continue
        key,sep,value=raw.partition('=')
        if not sep or not re.fullmatch(r'[A-Z][A-Z0-9_]*',key) or any(c in value for c in '\r\n\x00'):
            raise MaintenanceError('invalid env file')
        values[key]=value
    return values

def write_env(path,values):
    # Generated values contain no interpolation or shell syntax. Existing files
    # are parsed as data, never sourced or expanded by a shell.
    if any(any(c in str(v) for c in '\r\n\x00$#\'"') for v in values.values()):
        raise MaintenanceError('unsupported env quoting; use plain generated values')
    path=Path(path);temporary=path.with_suffix('.pending')
    fd=os.open(temporary,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
    with os.fdopen(fd,'w') as f:f.write(''.join(f'{k}={v}\n' for k,v in values.items()))
    os.chmod(temporary,0o600);temporary.replace(path)

def check(values):
    if any(any(c in str(v) for c in '\r\n\x00$#\'"') for v in values.values()):
        raise MaintenanceError('env values must use the generated unquoted format')
    for key in ('MYSQL_DATABASE','MYSQL_USER'):
        if not re.fullmatch(r'[A-Za-z][A-Za-z0-9_]{0,31}',values[key]):raise MaintenanceError('invalid database identifier')
    url=urlsplit(values['PUBLIC_URL'])
    if url.scheme not in ('http','https') or not url.hostname or url.username or url.password or url.path not in ('','/') or url.query or url.fragment or re.search(r'[\s;{}"\'$\\]',values['PUBLIC_URL']):raise MaintenanceError('invalid PUBLIC_URL')
    for key in ('PUBLIC_HTTP_PORT','PUBLIC_HTTPS_PORT'):
        if not 1<=int(values[key])<=65535:raise MaintenanceError('invalid public port')
    ipaddress.ip_network(values['EDGE_SUBNET']);ipaddress.ip_address(values['NGINX_IP']);ipaddress.ip_address(values['PUBLIC_BIND'])
    if not re.fullmatch(r'/[A-Za-z0-9_./-]+',values['DATA_DIR']):raise MaintenanceError('invalid data path')

def generate(path,root,gid):
    if Path(path).exists():check(read_env(path));return
    v={'APP_ENV':'production','APP_PORT':'8080','PUBLIC_HTTP_PORT':os.getenv('OJ_HTTP_PORT','80'),'PUBLIC_HTTPS_PORT':os.getenv('OJ_HTTPS_PORT','443'),'PUBLIC_BIND':os.getenv('OJ_PUBLIC_BIND','0.0.0.0'),'PUBLIC_URL':os.getenv('OJ_PUBLIC_URL','http://127.0.0.1'),'TIMEZONE':os.getenv('OJ_TIMEZONE','Asia/Shanghai'),'LOG_LEVEL':'info','MYSQL_DATABASE':'biue','MYSQL_USER':'biue','MYSQL_HOST':'mysql','MYSQL_PORT':'3306','REDIS_ADDR':'redis:6379','REDIS_DB':'0','AUTH_SESSION_TTL':'12h','BCRYPT_COST':'12','PROBLEM_IMPORT_CONCURRENCY':'2','JUDGE_QUEUE_CAPACITY':'1000','JUDGE_CONCURRENCY':os.getenv('OJ_JUDGE_CONCURRENCY','2'),'DOCKER_GID':gid,'DATA_DIR':root,'EDGE_SUBNET':os.getenv('OJ_EDGE_SUBNET','172.29.42.0/24'),'NGINX_IP':os.getenv('OJ_NGINX_IP','172.29.42.10'),'TRUSTED_PROXY_CIDRS':os.getenv('OJ_NGINX_IP','172.29.42.10')+'/32','RUNNER_URL':'https://runner:8083'}
    for key in ('MYSQL_PASSWORD','MYSQL_ROOT_PASSWORD','REDIS_PASSWORD','JWT_SECRET','CSRF_SECRET','APP_ENCRYPTION_KEY','RUNNER_TOKEN'):v[key]=secrets.token_hex(32)
    check(v);write_env(path,v)

def public_info(path):
    v=read_env(path);check(v)
    url=urlsplit(v['PUBLIC_URL'])
    origin=f"{url.scheme}://{url.netloc}"
    print(f"PUBLIC_URL={v['PUBLIC_URL']}")
    print(f"EXPECTED_ORIGIN={origin}")
    print(f"BROWSER_SCHEME={url.scheme}")
    print(f"BROWSER_HOST={url.hostname}")
    print(f"BROWSER_PORT={url.port or ('443' if url.scheme == 'https' else '80')}")
    print("修改公网来源：sudo bash deploy.sh public-url https://你的域名")
    print("外部 TLS 代理必须保留浏览器 Host，并转发 X-Forwarded-Proto；PUBLIC_URL 必须写浏览器实际访问的协议、主机和端口。")

def verify_archive(directory):
    directory=Path(directory).resolve();manifest=(directory/'SHA256SUMS').read_text().splitlines()
    expected={'database.sql.gz','files.tar.gz'};seen=set()
    for line in manifest:
        digest,sep,name=line.partition('  ')
        if not sep or name not in expected or name in seen or not re.fullmatch('[a-f0-9]{64}',digest):raise MaintenanceError('invalid backup manifest')
        file=directory/name
        if file.is_symlink() or not file.is_file():raise MaintenanceError('invalid backup member')
        digest_state=hashlib.sha256()
        with file.open('rb') as f:
            for chunk in iter(lambda:f.read(1<<20),b''):digest_state.update(chunk)
        actual=digest_state.hexdigest()
        if actual!=digest:raise MaintenanceError('backup digest mismatch')
        seen.add(name)
    if seen!=expected:raise MaintenanceError('incomplete backup')
    with tarfile.open(directory/'files.tar.gz','r:gz') as archive:
        for m in archive:
            parts=Path(m.name).parts
            if not parts or parts[0] not in ('config','problem-data','runner-data','uploads') or '..' in parts or Path(m.name).is_absolute() or '\\' in m.name or not (m.isdir() or m.isfile()):raise MaintenanceError('unsafe backup path or link')

def restore_sql(path):
    v=read_env(path);check(v)
    def quote(value):return "'"+value.replace('\\','\\\\').replace("'","''")+"'"
    db,user=v['MYSQL_DATABASE'],v['MYSQL_USER']
    print("SET SESSION sql_mode='';")
    print(f'DROP DATABASE IF EXISTS `{db}`; CREATE DATABASE `{db}` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;')
    print(f"CREATE USER IF NOT EXISTS {quote(user)}@'%' IDENTIFIED BY {quote(v['MYSQL_PASSWORD'])};")
    print(f"ALTER USER {quote(user)}@'%' IDENTIFIED BY {quote(v['MYSQL_PASSWORD'])}; GRANT ALL ON `{db}`.* TO {quote(user)}@'%';")
    print(f"ALTER USER 'root'@'localhost' IDENTIFIED BY {quote(v['MYSQL_ROOT_PASSWORD'])};")

def main():
    a=sys.argv[1:];command=a[0]
    if command=='generate':generate(*a[1:])
    elif command=='get':print(read_env(a[1])[a[2]])
    elif command=='keys':print('\n'.join(read_env(a[1]).keys()))
    elif command=='validate':check(read_env(a[1]))
    elif command=='relocate':
        v=read_env(a[1]);v['DATA_DIR']=a[2];v['DOCKER_GID']=a[3];check(v);write_env(a[1],v)
    elif command=='public-url':
        v=read_env(a[1]);v['PUBLIC_URL']=a[2];check(v);write_env(a[1],v)
    elif command=='public-info':public_info(a[1])
    elif command=='verify':verify_archive(a[1])
    elif command=='restore-sql':restore_sql(a[1])
    elif command=='prune':
        root=Path(a[1]).resolve();keep=int(a[2])
        if not 1<=keep<=3650:raise MaintenanceError('invalid retention count')
        backups=sorted((p for p in root.iterdir() if re.fullmatch(r'\d{8}T\d{6}Z-[a-f0-9]{8}',p.name) and p.is_dir() and not p.is_symlink()),reverse=True)
        for p in backups[keep:]:
            if p.resolve().parent!=root or {x.name for x in p.iterdir()}!={'database.sql.gz','files.tar.gz','SHA256SUMS'}:raise MaintenanceError('unexpected backup layout')
            verify_archive(p)
            for name in ('database.sql.gz','files.tar.gz','SHA256SUMS'):(p/name).unlink()
            p.rmdir();print('Removed backup '+p.name)
    elif command=='nginx':
        v=read_env(a[1]);check(v);s=Path(a[2]).read_text()
        if v['PUBLIC_URL'].startswith('https://'):
            s=s.replace('listen 8080;','listen 8443 ssl;\n        ssl_certificate /run/public-tls/server.pem;\n        ssl_certificate_key /run/public-tls/server.key;\n        ssl_protocols TLSv1.2 TLSv1.3;')
            pos=s.rfind('}')
            s=s[:pos]+f'    server {{ listen 8080; location = /nginx-health {{ return 200 "ok\\n"; }} location / {{ return 301 {v["PUBLIC_URL"].rstrip("/")}$request_uri; }} }}\n'+s[pos:]
        Path(a[3]).write_text(s)
    else:raise MaintenanceError('unknown maintenance command')

if __name__=='__main__':
    try:main()
    except MaintenanceError as e:print('maintenance validation failed: '+str(e),file=sys.stderr);sys.exit(1)
    except KeyError as e:print('missing configuration key: '+str(e.args[0]),file=sys.stderr);sys.exit(1)
    except Exception as e:print('maintenance validation failed: '+type(e).__name__,file=sys.stderr);sys.exit(1)
