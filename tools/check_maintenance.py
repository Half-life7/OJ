"""Filesystem-only safety regression checks for deployment helpers."""
import hashlib
import importlib.util
import io
import tempfile
import tarfile
import gzip
import sys
from pathlib import Path

spec=importlib.util.spec_from_file_location('maintenance',Path(__file__).parents[1]/'deploy/maintenance.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
with tempfile.TemporaryDirectory() as directory:
    root=Path(directory);env=root/'.env';m.generate(env,'/var/lib/biue','0');first=env.read_bytes();m.generate(env,'/var/lib/biue','0');assert env.read_bytes()==first
    values=m.read_env(env);assert len({values[k] for k in ('MYSQL_PASSWORD','MYSQL_ROOT_PASSWORD','REDIS_PASSWORD','JWT_SECRET','CSRF_SECRET','APP_ENCRYPTION_KEY','RUNNER_TOKEN')})==7
    invalid=dict(values);invalid['JWT_SECRET']='interpolate$unsafe'
    try:m.check(invalid)
    except ValueError:pass
    else:raise AssertionError('Compose interpolation allowed in persisted secrets')
    for unsafe in ('../escape','/etc/passwd','config/../../escape','config/evil\\file'):
        folder=root/('backup'+str(len(unsafe)));folder.mkdir(exist_ok=True);(folder/'database.sql.gz').write_bytes(b'dummy')
        with tarfile.open(folder/'files.tar.gz','w:gz') as t:
            entry=tarfile.TarInfo(unsafe);entry.size=1;t.addfile(entry,io.BytesIO(b'x'))
        (folder/'SHA256SUMS').write_text(''.join(hashlib.sha256((folder/name).read_bytes()).hexdigest()+'  '+name+'\n' for name in ('database.sql.gz','files.tar.gz')))
        try:m.verify_archive(folder)
        except ValueError:pass
        else:raise AssertionError('unsafe tar path accepted')
    link=root/'linkbackup';link.mkdir();(link/'database.sql.gz').write_bytes(b'dummy')
    with tarfile.open(link/'files.tar.gz','w:gz') as t:
        entry=tarfile.TarInfo('config/link');entry.type=tarfile.SYMTYPE;entry.linkname='/etc';t.addfile(entry)
    (link/'SHA256SUMS').write_text(''.join(hashlib.sha256((link/name).read_bytes()).hexdigest()+'  '+name+'\n' for name in ('database.sql.gz','files.tar.gz')))
    try:m.verify_archive(link)
    except ValueError:pass
    else:raise AssertionError('symlink accepted')
    retention=root/'retention';retention.mkdir();extra=retention/'unrelated';extra.mkdir();(extra/'keep.txt').write_text('keep')
    for day in ('01','02','03'):
        folder=retention/f'202601{day}T000000Z-00000000';folder.mkdir();(folder/'database.sql.gz').write_bytes(gzip.compress(b'SELECT 1;'))
        with tarfile.open(folder/'files.tar.gz','w:gz') as t:
            entry=tarfile.TarInfo('config/test');entry.size=1;t.addfile(entry,io.BytesIO(b'x'))
        (folder/'SHA256SUMS').write_text(''.join(hashlib.sha256((folder/name).read_bytes()).hexdigest()+'  '+name+'\n' for name in ('database.sql.gz','files.tar.gz')))
    previous=sys.argv
    try:sys.argv=['maintenance.py','prune',str(retention),'1'];m.main()
    finally:sys.argv=previous
    assert (retention/'20260103T000000Z-00000000').is_dir() and not (retention/'20260101T000000Z-00000000').exists() and (extra/'keep.txt').read_text()=='keep'
print('Generated secrets/idempotency and backup manifest/path/link validation passed')
